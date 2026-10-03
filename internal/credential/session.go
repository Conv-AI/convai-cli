package credential

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/jsonwire"
)

// sessionRecordVersion identifies the keychain encoding of a managed browser
// login. Raw API keys, PATs, and imported access tokens keep their original
// unversioned encoding so existing keychain entries continue to parse.
const sessionRecordVersion = 1

const sessionRecordKind = "oauth_session"

// Session is the non-access-token part of a managed browser login. The access
// token stays in Credential.Value so every REST caller uses one bearer path.
// Binding fields are not secret; they decide where the tokens may be sent.
type Session struct {
	RefreshToken string
	ExpiresAt    time.Time
	Scopes       []string
	Issuer       string
	ClientID     string
	Audience     string
	APIURL       string
}

type sessionRecord struct {
	Version      int    `json:"convai_credential_version"`
	Kind         string `json:"kind"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    string `json:"expires_at"`
	Scope        string `json:"scope"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	Audience     string `json:"audience"`
	APIURL       string `json:"api_url"`
}

// Managed reports whether the credential is a refreshable browser login.
func (c Credential) Managed() bool {
	return c.Session != nil
}

// NewSession validates a managed browser login before it can be stored or used.
func NewSession(accessToken string, session Session) (Credential, error) {
	credential := Credential{Type: config.CredentialOAuth, Value: accessToken, Session: &session}
	if err := credential.validateSession(); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

func (c Credential) validateSession() error {
	s := c.Session
	access, err := Parse(c.Value)
	if err != nil || access.Type != config.CredentialOAuth || c.Type != config.CredentialOAuth {
		return errors.New("browser login record has an invalid access token")
	}
	if err := ParseRefreshToken(s.RefreshToken); err != nil {
		return err
	}
	if s.ExpiresAt.IsZero() {
		return errors.New("browser login record has no expiry")
	}
	if len(s.Scopes) == 0 || slices.ContainsFunc(s.Scopes, func(scope string) bool {
		return scope == "" || strings.ContainsFunc(scope, isUnsafeRune)
	}) {
		return errors.New("browser login record has invalid scopes")
	}
	if s.Issuer == "" || s.ClientID == "" || s.Audience == "" {
		return errors.New("browser login record is missing its issuer, client, or audience binding")
	}
	if normalized, err := config.NormalizeAPIURL(s.APIURL); err != nil || normalized != s.APIURL {
		return errors.New("browser login record has an invalid API URL binding")
	}
	return nil
}

// ParseRefreshToken checks a refresh token's shape without classifying it as a
// bearer credential. Refresh tokens are only ever sent to the token and
// revocation endpoints of their bound issuer.
func ParseRefreshToken(value string) error {
	if !strings.HasPrefix(value, "cvort_") || len(value) < 20 || len(value) > 512 || strings.ContainsFunc(value, isUnsafeRune) {
		return errors.New("browser login record has an invalid refresh token")
	}
	return nil
}

func encodeSession(credential Credential) (string, error) {
	if err := credential.validateSession(); err != nil {
		return "", err
	}
	s := credential.Session
	data, err := json.Marshal(sessionRecord{
		Version:      sessionRecordVersion,
		Kind:         sessionRecordKind,
		AccessToken:  credential.Value,
		RefreshToken: s.RefreshToken,
		ExpiresAt:    s.ExpiresAt.UTC().Format(time.RFC3339),
		Scope:        strings.Join(s.Scopes, " "),
		Issuer:       s.Issuer,
		ClientID:     s.ClientID,
		Audience:     s.Audience,
		APIURL:       s.APIURL,
	})
	if err != nil {
		return "", fmt.Errorf("encode browser login record: %w", err)
	}
	return string(data), nil
}

// decodeStored accepts both a legacy raw credential and a versioned session
// record. An unknown version fails closed instead of being guessed at.
func decodeStored(value string) (Credential, error) {
	if !strings.HasPrefix(value, "{") {
		return Parse(value)
	}
	var record sessionRecord
	if err := jsonwire.DecodeStrict([]byte(value), &record); err != nil {
		return Credential{}, errors.New("stored credential record is malformed; run `convai auth logout --local` and log in again")
	}
	if record.Version != sessionRecordVersion || record.Kind != sessionRecordKind {
		return Credential{}, errors.New("stored credential record uses an unsupported version; upgrade the CLI or log in again")
	}
	expires, err := time.Parse(time.RFC3339, record.ExpiresAt)
	if err != nil {
		return Credential{}, errors.New("stored browser login record has an invalid expiry")
	}
	return NewSession(record.AccessToken, Session{
		RefreshToken: record.RefreshToken,
		ExpiresAt:    expires,
		Scopes:       strings.Fields(record.Scope),
		Issuer:       record.Issuer,
		ClientID:     record.ClientID,
		Audience:     record.Audience,
		APIURL:       record.APIURL,
	})
}
