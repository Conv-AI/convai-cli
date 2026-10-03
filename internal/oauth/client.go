package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"github.com/Conv-AI/convai-cli/internal/jsonwire"
)

const (
	maxResponseBytes = 1 << 20
	maxTokenLifetime = 24 * time.Hour
)

// Metadata is the validated subset of RFC 8414 discovery used by the CLI.
type Metadata struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RevocationEndpoint    string
	// IssuerInCallback reports RFC 9207 support: a successful callback must
	// then carry the exact issuer.
	IssuerInCallback bool
}

// Tokens is a validated token-endpoint response.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scopes       []string
}

// ErrInvalidResponse reports a successful-status token response that the CLI
// rejected. The grant was consumed; any credentials it carried were revoked.
var ErrInvalidResponse = errors.New("Convai sign-in returned an invalid token response")

// Error is an OAuth protocol error returned by the token or revocation
// endpoint. Its server-authored text is redacted of every credential sent in
// the request and of credential-shaped values before it is bounded.
type Error struct {
	Status      int
	Code        string
	Description string
}

func (e *Error) Error() string {
	message := fmt.Sprintf("Convai sign-in returned HTTP %d", e.Status)
	if e.Code != "" {
		message += ": " + e.Code
	}
	if e.Description != "" {
		message += " (" + e.Description + ")"
	}
	return message
}

// InvalidGrant reports that a code or refresh token is expired, revoked, or
// already used. A rejected refresh token must never be retried.
func InvalidGrant(err error) bool {
	var oauthErr *Error
	return errors.As(err, &oauthErr) && oauthErr.Code == "invalid_grant"
}

// Client talks to one discovered, trusted issuer.
type Client struct {
	http     *http.Client
	metadata Metadata
	now      func() time.Time
}

// NewHTTPClient copies a base client and disables redirects. OAuth requests
// carry codes and tokens, so a redirect is a failure, never a hop.
func NewHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	copied := *base
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copied
}

// Discover fetches and validates authorization-server metadata for a trusted
// issuer. The returned issuer must match exactly, and every endpoint must stay
// on the issuer's HTTPS origin without credentials, query, or fragment.
func Discover(ctx context.Context, httpClient *http.Client, issuer string, now func() time.Time) (*Client, error) {
	issuerURL, err := url.Parse(issuer)
	if err != nil || issuerURL.Scheme != "https" || issuerURL.Host == "" || issuerURL.User != nil ||
		issuerURL.Path != "" || issuerURL.RawQuery != "" || issuerURL.Fragment != "" {
		return nil, errors.New("Convai sign-in issuer is not a trusted HTTPS origin")
	}
	client := &Client{http: NewHTTPClient(httpClient), now: now}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	data, status, err := client.do(request)
	if err != nil {
		return nil, fmt.Errorf("discover Convai sign-in: %s", credential.Redact(err.Error()))
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("discover Convai sign-in: HTTP %d", status)
	}
	var document struct {
		Issuer                            string   `json:"issuer"`
		AuthorizationEndpoint             string   `json:"authorization_endpoint"`
		TokenEndpoint                     string   `json:"token_endpoint"`
		RevocationEndpoint                string   `json:"revocation_endpoint"`
		CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
		AuthorizationResponseISSSupported bool     `json:"authorization_response_iss_parameter_supported"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, errors.New("discover Convai sign-in: invalid metadata")
	}
	if document.Issuer != issuer {
		return nil, errors.New("discover Convai sign-in: metadata issuer does not match the trusted issuer")
	}
	if !slices.Contains(document.CodeChallengeMethodsSupported, "S256") {
		return nil, errors.New("discover Convai sign-in: issuer does not support PKCE S256")
	}
	for _, endpoint := range []string{document.AuthorizationEndpoint, document.TokenEndpoint, document.RevocationEndpoint} {
		if !sameOriginEndpoint(issuerURL, endpoint) {
			return nil, errors.New("discover Convai sign-in: metadata contains an untrusted endpoint")
		}
	}
	client.metadata = Metadata{
		Issuer:                issuer,
		AuthorizationEndpoint: document.AuthorizationEndpoint,
		TokenEndpoint:         document.TokenEndpoint,
		RevocationEndpoint:    document.RevocationEndpoint,
		IssuerInCallback:      document.AuthorizationResponseISSSupported,
	}
	return client, nil
}

func sameOriginEndpoint(issuer *url.URL, raw string) bool {
	endpoint, err := url.Parse(raw)
	return err == nil && endpoint.Scheme == "https" && endpoint.Host == issuer.Host && endpoint.User == nil &&
		endpoint.RawQuery == "" && !endpoint.ForceQuery && endpoint.Fragment == "" &&
		strings.HasPrefix(endpoint.Path, "/") && endpoint.RawPath == "" && !strings.Contains(endpoint.Path, "..")
}

func (c *Client) Metadata() Metadata { return c.metadata }

// AuthorizationURL builds the browser URL. It carries the PKCE challenge and
// state, never a credential. The optional resource parameter is omitted so
// Auth issues its default audience.
func (c *Client) AuthorizationURL(redirectURI, state, challenge string, scopes []string) string {
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return c.metadata.AuthorizationEndpoint + "?" + values.Encode()
}

// ExchangeCode redeems a single-use authorization code.
func (c *Client) ExchangeCode(ctx context.Context, code, verifier, redirectURI string, requested []string) (Tokens, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}, requested)
}

// Refresh rotates a refresh token exactly once. The caller must persist the
// returned pair or revoke it; the old refresh token is consumed either way.
func (c *Client) Refresh(ctx context.Context, refreshToken string, granted []string) (Tokens, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {ClientID},
		"refresh_token": {refreshToken},
	}, granted)
}

// Revoke revokes one token. Revoking a refresh token also revokes its paired
// access token in Auth; an already-invalid token is acknowledged per RFC 7009.
func (c *Client) Revoke(ctx context.Context, token string) error {
	request, err := c.form(ctx, c.metadata.RevocationEndpoint, url.Values{"token": {token}, "client_id": {ClientID}})
	if err != nil {
		return err
	}
	data, status, err := c.do(request)
	if err != nil {
		return fmt.Errorf("revoke Convai sign-in: %s", credential.Redact(err.Error(), token))
	}
	if status != http.StatusOK {
		return protocolError(status, data, token)
	}
	return nil
}

// RevokeAll revokes the refresh token first, then the access token, so the
// whole grant is withdrawn even if Auth's pairing ever changes.
func (c *Client) RevokeAll(ctx context.Context, tokens Tokens) error {
	return c.revoke(ctx, []string{tokens.RefreshToken}, []string{tokens.AccessToken})
}

func (c *Client) revoke(ctx context.Context, refresh, access []string) error {
	var failures []error
	for _, token := range append(slices.Clone(refresh), access...) {
		if token == "" {
			continue
		}
		if err := c.Revoke(ctx, token); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (c *Client) token(ctx context.Context, form url.Values, requested []string) (Tokens, error) {
	request, err := c.form(ctx, c.metadata.TokenEndpoint, form)
	if err != nil {
		return Tokens{}, err
	}
	secrets := []string{form.Get("code"), form.Get("code_verifier"), form.Get("refresh_token")}
	issuedAt := c.now()
	data, status, err := c.do(request)
	if status < 200 || status >= 300 {
		if err != nil {
			// No successful status was received, so the outcome at Auth is
			// unknown: the grant may or may not have been consumed.
			return Tokens{}, fmt.Errorf("Convai sign-in token request did not complete; Auth may or may not have processed it: %s", credential.Redact(err.Error(), secrets...))
		}
		return Tokens{}, protocolError(status, data, secrets...)
	}
	// Any successful status consumed the code or refresh token, even when the
	// body is unreadable, oversized, or invalid.
	var tokens Tokens
	var issued issuedTokens
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("unexpected HTTP %d", status)
	}
	if err == nil {
		tokens, issued, err = parseTokens(data, requested, issuedAt)
		if err == nil {
			return tokens, nil
		}
	} else {
		// Recover any complete token strings from the bytes that did arrive.
		_, issued, _ = tokenFields(data)
	}
	err = fmt.Errorf("%w: %s", ErrInvalidResponse, credential.Redact(err.Error(), secrets...))
	if cleanup := c.revoke(context.WithoutCancel(ctx), issued.refresh, issued.access); cleanup != nil {
		return Tokens{}, fmt.Errorf("%w; revoking its credentials failed: %v", err, cleanup)
	}
	return Tokens{}, err
}

type issuedTokens struct {
	access  []string
	refresh []string
}

// parseTokens validates a token response strictly. It also returns every
// access- or refresh-token string the response carried, including duplicates
// and values beside malformed fields, so a rejected response can be revoked.
func parseTokens(data []byte, requested []string, issuedAt time.Time) (Tokens, issuedTokens, error) {
	fields, issued, err := tokenFields(data)
	if err != nil {
		return Tokens{}, issued, err
	}
	var tokens Tokens
	var tokenType string
	// An int64 target rejects strings, fractions, exponents, and values that
	// overflow int64 instead of coercing them.
	var seconds int64
	var scope *string
	for name, target := range map[string]any{
		"access_token": &tokens.AccessToken, "refresh_token": &tokens.RefreshToken,
		"token_type": &tokenType, "expires_in": &seconds, "scope": &scope,
	} {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		if jsonwire.Decode(raw, target) != nil {
			return Tokens{}, issued, fmt.Errorf("field %s has an invalid type", name)
		}
	}
	if access, err := credential.Parse(tokens.AccessToken); err != nil || access.Type != config.CredentialOAuth {
		return Tokens{}, issued, errors.New("missing or invalid access token")
	}
	if credential.ParseRefreshToken(tokens.RefreshToken) != nil {
		return Tokens{}, issued, errors.New("missing or invalid refresh token")
	}
	if !strings.EqualFold(tokenType, "Bearer") {
		return Tokens{}, issued, errors.New("token type is not Bearer")
	}
	// Bound integer seconds before converting, so a huge value cannot overflow
	// time.Duration into a small or negative lifetime.
	if seconds <= 0 || seconds > int64(maxTokenLifetime/time.Second) {
		return Tokens{}, issued, errors.New("token lifetime is missing or out of range")
	}
	tokens.Scopes = slices.Clone(requested)
	if scope != nil {
		tokens.Scopes = strings.Fields(*scope)
	}
	if len(tokens.Scopes) == 0 || slices.ContainsFunc(tokens.Scopes, func(granted string) bool { return !slices.Contains(requested, granted) }) {
		return Tokens{}, issued, errors.New("granted scopes were not requested")
	}
	slices.Sort(tokens.Scopes)
	tokens.Scopes = slices.Compact(tokens.Scopes)
	tokens.ExpiresAt = issuedAt.Add(time.Duration(seconds) * time.Second).UTC()
	return tokens, issued, nil
}

// tokenFields reads one top-level JSON object, rejecting duplicate members.
// Token strings are collected as they stream past, so they survive a later
// syntax error, duplicate, or type mismatch.
func tokenFields(data []byte) (map[string]json.RawMessage, issuedTokens, error) {
	var issued issuedTokens
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, issued, errors.New("response is not a JSON object")
	}
	fields := map[string]json.RawMessage{}
	var duplicate error
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, issued, errors.New("response is not valid JSON")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, issued, errors.New("response is not valid JSON")
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			switch name {
			case "access_token":
				issued.access = append(issued.access, value)
			case "refresh_token":
				issued.refresh = append(issued.refresh, value)
			}
		}
		if _, exists := fields[name]; exists {
			duplicate = fmt.Errorf("duplicate field %s", name)
		}
		fields[name] = raw
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, issued, errors.New("response is not valid JSON")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, issued, errors.New("response must contain exactly one JSON document")
	}
	return fields, issued, duplicate
}

// protocolError keeps only a protocol error code and a redacted, bounded
// description. Redaction runs on the full text before truncation.
func protocolError(status int, data []byte, secrets ...string) error {
	var body struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(data, &body)
	// Keep the error code only when it is a plain protocol token that echoes
	// no request secret; otherwise omit it rather than truncating it.
	code := ""
	if isProtocolToken(body.Error) && credential.Redact(body.Error, secrets...) == body.Error {
		code = body.Error
	}
	return &Error{Status: status, Code: code, Description: sanitize(credential.Redact(body.ErrorDescription, secrets...), 200)}
}

// sanitize keeps already-redacted server text printable and bounded.
func sanitize(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	if len(value) > limit {
		value = strings.ToValidUTF8(value[:limit], "")
	}
	return value
}

func (c *Client) form(ctx context.Context, endpoint string, values url.Values) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "convai-cli")
	return request, nil
}

func (c *Client) do(request *http.Request) ([]byte, int, error) {
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, response.StatusCode, errors.New("unexpected redirect from Convai sign-in")
	}
	// On a read failure or oversized body, the bytes received so far are
	// returned with the error so a consumed grant's tokens can be revoked.
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return data, response.StatusCode, fmt.Errorf("read Convai sign-in response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return data[:maxResponseBytes], response.StatusCode, errors.New("Convai sign-in response exceeds 1 MiB")
	}
	return data, response.StatusCode, nil
}
