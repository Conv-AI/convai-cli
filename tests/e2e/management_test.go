package e2e

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeManagement(t *testing.T, respond func(*http.Request) (int, string)) managementClient {
	t.Helper()
	client := newManagementClient()
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "login-stg.convai.com" {
			t.Fatal("management credentials left the fixed staging issuer")
		}
		status, body := respond(r)
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	return client
}

func TestManagementPATLifecycle(t *testing.T) {
	var calls []string
	client := fakeManagement(t, func(r *http.Request) (int, string) {
		calls = append(calls, r.Method)
		if r.Header.Get("Authorization") != "Bearer firebase-secret" {
			t.Fatal("missing Firebase management identity")
		}
		switch r.Method {
		case "POST":
			if r.URL.Path != "/auth/personal-access-tokens" || r.Header.Get("Content-Type") != "application/json" {
				t.Fatal("incorrect PAT issuance binding")
			}
			var body struct {
				Name   string
				Scopes []string
				Days   int `json:"expires_in_days"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Name == "" || len(body.Scopes) != 1 || body.Scopes[0] != "character:read" || body.Days != 1 {
				t.Fatal("incorrect least-privilege PAT fixture")
			}
			return 201, `{"token":"cvpat_test-secret","personal_access_token":{"id":"token-id"}}`
		case "DELETE":
			if r.URL.Path != "/auth/personal-access-tokens/token-id" {
				t.Fatal("incorrect revocation ID")
			}
			return 200, `{}`
		default:
			t.Fatal("unexpected PAT request")
			return 500, ""
		}
	})
	credential, err := client.createPAT("firebase-secret", []string{"character:read"})
	if err != nil || credential.ID != "token-id" || credential.AccessToken != "cvpat_test-secret" {
		t.Fatal("PAT response parsing failed")
	}
	if err := client.revokePAT("firebase-secret", credential); err != nil || strings.Join(calls, ",") != "POST,DELETE" {
		t.Fatal("PAT cleanup failed")
	}
}

func TestMalformedPATStillRevokesKnownID(t *testing.T) {
	var revoked bool
	client := fakeManagement(t, func(r *http.Request) (int, string) {
		if r.Method == "DELETE" {
			revoked = r.URL.Path == "/auth/personal-access-tokens/token-id"
			return 200, `{}`
		}
		return 201, `{"token":"unexpected-secret","personal_access_token":{"id":"token-id"}}`
	})
	_, err := client.createPAT("firebase-secret", []string{"character:read"})
	if err == nil || !revoked || strings.Contains(err.Error(), "secret") {
		t.Fatal("malformed PAT response was not safely cleaned up")
	}
}

func TestMalformedCredentialJSONStillRevokes(t *testing.T) {
	t.Run("PAT", func(t *testing.T) {
		var revoked bool
		client := fakeManagement(t, func(r *http.Request) (int, string) {
			if r.Method == "DELETE" {
				revoked = true
				return 200, `{}`
			}
			return 201, `{"token":17,"personal_access_token":{"id":"known-id"}}`
		})
		if _, err := client.createPAT("firebase-secret", []string{"character:read"}); err == nil || !revoked {
			t.Fatal("partially decoded PAT was not revoked")
		}
	})
	t.Run("OAuth", func(t *testing.T) {
		var revoked bool
		client := fakeManagement(t, func(r *http.Request) (int, string) {
			if r.URL.Path == "/auth/oauth/revoke" {
				revoked = true
				return 200, `{}`
			}
			return 200, `{"access_token":"cvoat_secret","refresh_token":17}`
		})
		if _, err := client.exchangeOAuth("/auth/oauth/token", url.Values{}); err == nil || !revoked {
			t.Fatal("partially decoded OAuth credential was not revoked")
		}
	})
}

func TestOAuthRevocationAttemptsBothTokens(t *testing.T) {
	var tokens []string
	client := fakeManagement(t, func(r *http.Request) (int, string) {
		if r.Method != "POST" || r.URL.Path != "/auth/oauth/revoke" || r.Header.Get("Authorization") != "" {
			t.Fatal("incorrect OAuth revocation binding")
		}
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("client_id") != "codex" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatal("incorrect OAuth client binding")
		}
		tokens = append(tokens, form.Get("token"))
		if len(tokens) == 1 {
			return 503, `{"token":"access-secret"}`
		}
		return 200, `{}`
	})
	err := client.revokeOAuth(issuedCredential{AccessToken: "access-secret", RefreshToken: "refresh-secret"})
	if err == nil || strings.Join(tokens, ",") != "access-secret,refresh-secret" || strings.Contains(err.Error(), "secret") {
		t.Fatal("OAuth revocation did not attempt both tokens without leaking errors")
	}
}

func TestManagementErrorsAreBoundedAndRedacted(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"http", 401, "firebase-secret"},
		{"json", 200, "access-secret"},
		{"size", 200, strings.Repeat("x", (1<<20)+1)},
		{"redirect", 302, "refresh-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := fakeManagement(t, func(*http.Request) (int, string) { return test.status, test.body })
			var value any
			err := client.request("GET", "/fixture", "", "firebase-secret", nil, 200, &value)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("management error was not safely bounded/redacted")
			}
		})
	}
	client := newManagementClient()
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("access-secret") })
	if err := client.request("GET", "/fixture", "", "", nil, 200, nil); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("transport diagnostic exposed a credential")
	}
	if client.http.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("management client follows redirects")
	}
}
