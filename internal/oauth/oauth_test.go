package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Conv-AI/convai-cli/internal/config"
)

const (
	testAccess  = "cvoat_0123456789abcdef0123456789abcdef_c2VjcmV0LWFjY2Vzcw"
	testRefresh = "cvort_0123456789abcdef0123456789abcdef_c2VjcmV0LXJlZnJlc2g"
)

func TestProductionEnvironmentsAreExactTrustedMappings(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"https://api2-stg.convai.com":     "https://login-stg.convai.com",
		"https://api2-preview.convai.com": "https://login-preview.convai.com",
		"https://api2.convai.com":         "https://login.convai.com",
	}
	if len(Environments) != len(want) {
		t.Fatalf("trusted environments = %d", len(Environments))
	}
	for api, issuer := range want {
		if got, ok := IssuerFor(Environments, api); !ok || got != issuer {
			t.Fatalf("IssuerFor(%s) = %q, %v", api, got, ok)
		}
	}
	for _, api := range []string{"https://api2-stg.convai.com/", "https://API2.convai.com", "http://api2.convai.com", "https://api2.convai.com:443", "https://example.com", "http://127.0.0.1:8080"} {
		if _, ok := IssuerFor(Environments, api); ok {
			t.Fatalf("untrusted API %q mapped to an issuer", api)
		}
	}
	if config.DefaultAPIURL != "https://api2.convai.com" || config.StagingAPIURL != "https://api2-stg.convai.com" {
		t.Fatal("trusted mapping depends on changed API constants")
	}
}

func TestPKCEChallengeAndRandomValues(t *testing.T) {
	t.Parallel()
	// RFC 7636 Appendix B.
	if got := Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge = %s", got)
	}
	first, err := Random()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Random()
	if len(first) != 43 || first == second || strings.ContainsAny(first, "+/=") {
		t.Fatal("random values must be distinct 43-character base64url strings")
	}
}

type metadataServer struct {
	server   *httptest.Server
	mutate   func(map[string]any)
	redirect bool
}

func newMetadataServer(t *testing.T) *metadataServer {
	t.Helper()
	m := &metadataServer{}
	m.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		if m.redirect {
			http.Redirect(w, r, "https://login.convai.com/.well-known/oauth-authorization-server", http.StatusFound)
			return
		}
		issuer := m.server.URL
		document := map[string]any{
			"issuer":                           issuer,
			"authorization_endpoint":           issuer + "/auth/oauth/authorize",
			"token_endpoint":                   issuer + "/auth/oauth/token",
			"revocation_endpoint":              issuer + "/auth/oauth/revoke",
			"code_challenge_methods_supported": []string{"S256"},
			"authorization_response_iss_parameter_supported": true,
		}
		if m.mutate != nil {
			m.mutate(document)
		}
		_ = json.NewEncoder(w).Encode(document)
	}))
	t.Cleanup(m.server.Close)
	return m
}

func TestDiscoverEnforcesIssuerAndEndpointTrust(t *testing.T) {
	t.Parallel()
	m := newMetadataServer(t)
	issuer := m.server.URL
	host := strings.TrimPrefix(issuer, "https://")
	client, err := Discover(context.Background(), m.server.Client(), issuer, time.Now)
	if err != nil {
		t.Fatalf("valid metadata: %v", err)
	}
	authorize, _ := url.Parse(client.AuthorizationURL("http://127.0.0.1:1234/callback", "state", Challenge("verifier"), []string{"character:read"}))
	query := authorize.Query()
	if query.Get("client_id") != ClientID || query.Get("code_challenge_method") != "S256" || query.Get("response_type") != "code" || query.Has("resource") || query.Get("scope") != "character:read" {
		t.Fatalf("authorization URL = %s", authorize)
	}
	for name, mutate := range map[string]func(map[string]any){
		"issuer mismatch":   func(d map[string]any) { d["issuer"] = issuer + "/" },
		"other issuer":      func(d map[string]any) { d["issuer"] = "https://login.convai.com" },
		"http endpoint":     func(d map[string]any) { d["token_endpoint"] = "http://" + host + "/auth/oauth/token" },
		"other host":        func(d map[string]any) { d["token_endpoint"] = "https://login.convai.com/auth/oauth/token" },
		"suffix host":       func(d map[string]any) { d["revocation_endpoint"] = "https://" + host + ".attacker.invalid/revoke" },
		"other port":        func(d map[string]any) { d["token_endpoint"] = "https://127.0.0.1:1/auth/oauth/token" },
		"userinfo":          func(d map[string]any) { d["token_endpoint"] = "https://user:pass@" + host + "/token" },
		"query":             func(d map[string]any) { d["authorization_endpoint"] = issuer + "/authorize?x=1" },
		"empty query":       func(d map[string]any) { d["authorization_endpoint"] = issuer + "/authorize?" },
		"fragment":          func(d map[string]any) { d["revocation_endpoint"] = issuer + "/revoke#x" },
		"encoded path":      func(d map[string]any) { d["token_endpoint"] = issuer + "/%2ftoken" },
		"traversal":         func(d map[string]any) { d["token_endpoint"] = issuer + "/a/../token" },
		"missing endpoint":  func(d map[string]any) { delete(d, "revocation_endpoint") },
		"no S256":           func(d map[string]any) { d["code_challenge_methods_supported"] = []string{"plain"} },
		"wrong field types": func(d map[string]any) { d["issuer"] = 7 },
	} {
		t.Run(name, func(t *testing.T) {
			m := newMetadataServer(t)
			issuer, host = m.server.URL, strings.TrimPrefix(m.server.URL, "https://")
			m.mutate = mutate
			if _, err := Discover(context.Background(), m.server.Client(), m.server.URL, time.Now); err == nil {
				t.Fatal("untrusted metadata was accepted")
			}
		})
	}
	redirecting := newMetadataServer(t)
	redirecting.redirect = true
	if _, err := Discover(context.Background(), redirecting.server.Client(), redirecting.server.URL, time.Now); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("metadata redirect error = %v", err)
	}
	for _, untrusted := range []string{"http://login.convai.com", "https://login.convai.com/", "https://login.convai.com/path", "https://u@login.convai.com"} {
		if _, err := Discover(context.Background(), nil, untrusted, time.Now); err == nil {
			t.Fatalf("issuer %q accepted", untrusted)
		}
	}
}

func TestParseTokensValidatesEveryField(t *testing.T) {
	t.Parallel()
	requested := []string{"character:read", "character:write"}
	issuedAt := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	valid := fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","expires_in":3600,"refresh_token":%q,"scope":"character:read character:write"}`, testAccess, testRefresh)
	tokens, _, err := parseTokens([]byte(valid), requested, issuedAt)
	if err != nil || tokens.AccessToken != testAccess || tokens.RefreshToken != testRefresh || !tokens.ExpiresAt.Equal(issuedAt.Add(time.Hour)) || !slices.Equal(tokens.Scopes, requested) {
		t.Fatalf("valid response = %+v, %v", tokens, err)
	}
	narrowed, _, err := parseTokens([]byte(strings.Replace(valid, `"character:read character:write"`, `"character:read"`, 1)), requested, issuedAt)
	if err != nil || !slices.Equal(narrowed.Scopes, []string{"character:read"}) {
		t.Fatalf("narrowed grant = %+v, %v", narrowed, err)
	}
	omitted, _, err := parseTokens([]byte(strings.Replace(valid, `,"scope":"character:read character:write"`, ``, 1)), requested, issuedAt)
	if err != nil || !slices.Equal(omitted.Scopes, requested) {
		t.Fatalf("omitted scope = %+v, %v", omitted, err)
	}
	lowercase, _, err := parseTokens([]byte(strings.Replace(valid, `"Bearer"`, `"bearer"`, 1)), requested, issuedAt)
	if err != nil || lowercase.AccessToken == "" {
		t.Fatalf("case-insensitive Bearer = %v", err)
	}
	for name, body := range map[string]string{
		"max seconds plus one": strings.Replace(valid, `3600`, `86401`, 1),
		"max int64":            strings.Replace(valid, `3600`, `9223372036854775807`, 1),
		"duration overflow":    strings.Replace(valid, `3600`, `9223372037`, 1),
		"int64 overflow":       strings.Replace(valid, `3600`, `9223372036854775808`, 1),
		"zero lifetime":        strings.Replace(valid, `3600`, `0`, 1),
		"negative lifetime":    strings.Replace(valid, `3600`, `-1`, 1),
		"string lifetime":      strings.Replace(valid, `3600`, `"3600"`, 1),
		"fraction lifetime":    strings.Replace(valid, `3600`, `3600.5`, 1),
		"missing lifetime":     strings.Replace(valid, `"expires_in":3600,`, ``, 1),
		"non-Bearer":           strings.Replace(valid, `"Bearer"`, `"DPoP"`, 1),
		"missing type":         strings.Replace(valid, `"token_type":"Bearer",`, ``, 1),
		"missing refresh":      strings.Replace(valid, `,"refresh_token":"`+testRefresh+`"`, ``, 1),
		"refresh as access":    strings.Replace(valid, `"access_token":"`+testAccess, `"access_token":"`+testRefresh, 1),
		"PAT as access":        strings.Replace(valid, `"access_token":"cvoat_`, `"access_token":"cvpat_`, 1),
		"overbroad scope":      strings.Replace(valid, `"character:read character:write"`, `"character:read knowledge-bank:write"`, 1),
		"empty scope":          strings.Replace(valid, `"character:read character:write"`, `""`, 1),
		"duplicate access":     strings.Replace(valid, `{`, `{"access_token":"cvoat_ffffffffffffffffffffffffffffffff_b3RoZXI",`, 1),
		"trailing document":    valid + `{}`,
		"array":                `[` + valid + `]`,
		"truncated":            valid[:len(valid)-10],
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseTokens([]byte(body), requested, issuedAt); err == nil {
				t.Fatal("invalid token response was accepted")
			}
		})
	}
	_, issued, err := parseTokens([]byte(strings.Replace(valid, `3600`, `"3600"`, 1)), requested, issuedAt)
	if err == nil || !slices.Equal(issued.access, []string{testAccess}) || !slices.Equal(issued.refresh, []string{testRefresh}) {
		t.Fatalf("tokens beside a type error were not retained for revocation: %+v", issued)
	}
	_, issued, _ = parseTokens([]byte(strings.Replace(valid, `{`, `{"access_token":"cvoat_ffffffffffffffffffffffffffffffff_b3RoZXI",`, 1)), requested, issuedAt)
	if len(issued.access) != 2 {
		t.Fatalf("duplicate access tokens were not all retained: %+v", issued)
	}
}

// tokenServer is a scripted token/revocation endpoint pair.
type tokenServer struct {
	*metadataServer
	mu      sync.Mutex
	token   http.HandlerFunc
	revoked []string
	revoke  int
}

func newTokenServer(t *testing.T, token http.HandlerFunc) (*tokenServer, *Client) {
	t.Helper()
	s := &tokenServer{token: token, revoke: http.StatusOK}
	s.metadataServer = &metadataServer{}
	s.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			issuer := s.server.URL
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
				"revocation_endpoint": issuer + "/revoke", "code_challenge_methods_supported": []string{"S256"},
			})
		case "/token":
			s.token(w, r)
		case "/revoke":
			_ = r.ParseForm()
			s.mu.Lock()
			s.revoked = append(s.revoked, r.PostForm.Get("token"))
			status := s.revoke
			s.mu.Unlock()
			if r.PostForm.Get("client_id") != ClientID {
				status = http.StatusUnauthorized
			}
			w.WriteHeader(status)
		}
	}))
	t.Cleanup(s.server.Close)
	client, err := Discover(context.Background(), s.server.Client(), s.server.URL, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s, client
}

func (s *tokenServer) revokedTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.revoked)
}

func TestRejectedSuccessfulResponsesRevokeEveryIssuedToken(t *testing.T) {
	t.Parallel()
	pair := fmt.Sprintf(`"access_token":%q,"refresh_token":%q`, testAccess, testRefresh)
	for name, write := range map[string]func(http.ResponseWriter){
		"malformed lifetime type": func(w http.ResponseWriter) { fmt.Fprintf(w, `{%s,"token_type":"Bearer","expires_in":"3600"}`, pair) },
		"non-Bearer":              func(w http.ResponseWriter) { fmt.Fprintf(w, `{%s,"token_type":"MAC","expires_in":3600}`, pair) },
		"overbroad": func(w http.ResponseWriter) {
			fmt.Fprintf(w, `{%s,"token_type":"Bearer","expires_in":3600,"scope":"chat-history:read"}`, pair)
		},
		"duplicate": func(w http.ResponseWriter) {
			fmt.Fprintf(w, `{%s,%s,"token_type":"Bearer","expires_in":3600}`, pair, pair)
		},
		"incomplete": func(w http.ResponseWriter) {
			fmt.Fprintf(w, `{"refresh_token":%q,"token_type":"Bearer","expires_in":3600}`, testRefresh)
		},
		"invalid JSON": func(w http.ResponseWriter) { fmt.Fprintf(w, `{%s,"token_type":}`, pair) },
		"oversized": func(w http.ResponseWriter) {
			fmt.Fprintf(w, `{%s,"padding":"%s"}`, pair, strings.Repeat("x", maxResponseBytes))
		},
		"truncated body": func(w http.ResponseWriter) {
			body := fmt.Sprintf(`{%s,"token_type":"Bearer","expires_in":3600}`, pair)
			w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
			_, _ = w.Write([]byte(body[:len(body)-5]))
		},
		"unexpected 2xx": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{%s,"token_type":"Bearer","expires_in":3600}`, pair)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server, client := newTokenServer(t, func(w http.ResponseWriter, _ *http.Request) { write(w) })
			_, err := client.Refresh(context.Background(), testRefresh, []string{"character:read"})
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
			if strings.Contains(err.Error(), testAccess) || strings.Contains(err.Error(), testRefresh) {
				t.Fatal("error exposed a credential")
			}
			revoked := server.revokedTokens()
			if !slices.Contains(revoked, testRefresh) && name != "incomplete" {
				t.Fatalf("issued refresh token was not revoked: %d revocations", len(revoked))
			}
			if name != "incomplete" && !slices.Contains(revoked, testAccess) {
				t.Fatal("issued access token was not revoked")
			}
			if len(revoked) > 0 && revoked[0] != testRefresh {
				t.Fatal("refresh token must be revoked before its access token")
			}
		})
	}
}

func TestTransportFailureIsReportedAsAmbiguous(t *testing.T) {
	t.Parallel()
	server, client := newTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	})
	_, err := client.Refresh(context.Background(), testRefresh, []string{"character:read"})
	if err == nil || errors.Is(err, ErrInvalidResponse) || InvalidGrant(err) || !strings.Contains(err.Error(), "may or may not") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), testRefresh) || len(server.revokedTokens()) != 0 {
		t.Fatal("ambiguous failure leaked or revoked credentials")
	}
}

func TestProtocolErrorsRedactKnownSecretsBeforeTruncation(t *testing.T) {
	t.Parallel()
	const code = "cvoac_0123456789abcdef0123456789abcdef_Y29kZQ"
	long := strings.Repeat("a", 190) + testRefresh
	for name, body := range map[string]string{
		"refresh in code":        fmt.Sprintf(`{"error":%q}`, testRefresh),
		"lowercase secret code":  `{"error":"knownsecret"}`,
		"refresh in description": fmt.Sprintf(`{"error":"invalid_grant","error_description":"bad %s"}`, testRefresh),
		"code in description":    fmt.Sprintf(`{"error":"invalid_grant","error_description":"code %s"}`, code),
		"truncated secret":       fmt.Sprintf(`{"error":"invalid_grant","error_description":%q}`, long),
		"unprefixed secret":      `{"error":"invalid_grant","error_description":"verifier knownsecret leaked"}`,
	} {
		t.Run(name, func(t *testing.T) {
			err := protocolError(http.StatusBadRequest, []byte(body), testRefresh, code, "knownsecret")
			message := err.Error()
			for _, secret := range []string{testRefresh, code, "knownsecret", testRefresh[:12], code[:12]} {
				if strings.Contains(message, secret) {
					t.Fatalf("error %q exposed %q", message, secret)
				}
			}
		})
	}
	err := protocolError(http.StatusBadRequest, []byte(`{"error":"invalid_grant","error_description":"Refresh token is invalid"}`), testRefresh)
	if !InvalidGrant(err) || !strings.Contains(err.Error(), "Refresh token is invalid") {
		t.Fatalf("safe protocol error was lost: %v", err)
	}
}

func TestRevokeRequiresAcknowledgment(t *testing.T) {
	t.Parallel()
	server, client := newTokenServer(t, nil)
	if err := client.RevokeAll(context.Background(), Tokens{AccessToken: testAccess, RefreshToken: testRefresh}); err != nil {
		t.Fatal(err)
	}
	if got := server.revokedTokens(); !slices.Equal(got, []string{testRefresh, testAccess}) {
		t.Fatalf("revocation order = %d tokens", len(got))
	}
	server.mu.Lock()
	server.revoke = http.StatusServiceUnavailable
	server.mu.Unlock()
	if err := client.Revoke(context.Background(), testRefresh); err == nil || strings.Contains(err.Error(), testRefresh) {
		t.Fatalf("failed revocation = %v", err)
	}
}

func TestCallbackHandlerBoundary(t *testing.T) {
	t.Parallel()
	const host, state, issuer = "127.0.0.1:4567", "expected-state", "https://login.convai.com"
	for _, tc := range []struct {
		name, method, host, target string
		status                     int
		delivered                  bool
		failure                    string
	}{
		{"success", "GET", host, "/callback?state=expected-state&code=secret-code&iss=" + url.QueryEscape(issuer), 200, true, ""},
		{"method", "POST", host, "/callback?state=expected-state&code=secret-code", 405, false, ""},
		{"path", "GET", host, "/callback/extra?state=expected-state&code=secret-code", 404, false, ""},
		{"encoded path", "GET", host, "/%63allback?state=expected-state&code=secret-code", 404, false, ""},
		{"host", "GET", "localhost:4567", "/callback?state=expected-state&code=secret-code", 400, false, ""},
		{"rebinding host", "GET", "attacker.invalid", "/callback?state=expected-state&code=secret-code", 400, false, ""},
		{"missing state", "GET", host, "/callback?code=secret-code", 400, false, ""},
		{"wrong state", "GET", host, "/callback?state=other&code=secret-code", 400, false, ""},
		{"duplicate state", "GET", host, "/callback?state=expected-state&state=expected-state&code=secret-code", 400, false, ""},
		{"invalid query", "GET", host, "/callback?state=expected-state&code=%zz", 400, false, ""},
		{"denied", "GET", host, "/callback?state=expected-state&error=access_denied", 400, true, "access_denied"},
		{"unsafe error", "GET", host, "/callback?state=expected-state&error=cvoat_leak", 400, true, "denied"},
		{"duplicate code", "GET", host, "/callback?state=expected-state&code=a&code=b&iss=" + url.QueryEscape(issuer), 400, true, "malformed"},
		{"empty code", "GET", host, "/callback?state=expected-state&code=&iss=" + url.QueryEscape(issuer), 400, true, "malformed"},
		{"code with error", "GET", host, "/callback?state=expected-state&code=a&error=access_denied", 400, true, "denied"},
		{"missing issuer", "GET", host, "/callback?state=expected-state&code=secret-code", 400, true, "omitted its issuer"},
		{"wrong issuer", "GET", host, "/callback?state=expected-state&code=secret-code&iss=https%3A%2F%2Fattacker.invalid", 400, true, "unexpected issuer"},
		{"duplicate issuer", "GET", host, "/callback?state=expected-state&code=secret-code&iss=" + url.QueryEscape(issuer) + "&iss=" + url.QueryEscape(issuer), 400, true, "unexpected issuer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := make(chan callbackResult, 1)
			handler := callbackHandler(host, state, issuer, true, result)
			request := httptest.NewRequest(tc.method, "http://"+host+tc.target, nil)
			request.Host = tc.host
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || strings.Contains(response.Body.String(), "secret-code") ||
				response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if (len(result) == 1) != tc.delivered {
				t.Fatalf("delivered = %v", len(result) == 1)
			}
			if !tc.delivered {
				return
			}
			outcome := <-result
			if tc.failure == "" {
				if outcome.err != nil || outcome.code != "secret-code" {
					t.Fatalf("outcome = %+v", outcome)
				}
			} else if outcome.err == nil || !strings.Contains(outcome.err.Error(), tc.failure) || strings.Contains(outcome.err.Error(), "cvoat_") {
				t.Fatalf("outcome error = %v", outcome.err)
			}
			replay := httptest.NewRecorder()
			request = httptest.NewRequest("GET", "http://"+host+"/callback?state=expected-state&code=again", nil)
			request.Host = host
			handler.ServeHTTP(replay, request)
			if replay.Code != http.StatusConflict || len(result) != 0 {
				t.Fatal("callback replay was accepted")
			}
		})
	}
}

func TestCallbackListenerWaitsOnLoopbackAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	callback, err := StartCallback("state", "https://login.convai.com", false)
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	redirect, _ := url.Parse(callback.RedirectURI)
	if redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Port() == "" || redirect.Path != CallbackPath {
		t.Fatalf("redirect URI = %s", callback.RedirectURI)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := callback.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout = %v", err)
	}
	go func() {
		response, err := http.Get(callback.RedirectURI + "?state=state&code=from-browser")
		if err == nil {
			response.Body.Close()
		}
	}()
	code, err := callback.Wait(context.Background())
	if err != nil || code != "from-browser" {
		t.Fatalf("Wait = %q, %v", code, err)
	}
}
