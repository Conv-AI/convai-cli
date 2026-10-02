package e2e

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOAuthCallbackValidation(t *testing.T) {
	for _, test := range []struct {
		name, method, path string
		status             int
		receives, failure  bool
	}{
		{"valid", "GET", "/callback?state=expected&code=secret-code", 303, true, false},
		{"bad-state", "GET", "/callback?state=wrong&code=secret-code", 400, false, false},
		{"duplicate-state", "GET", "/callback?state=expected&state=expected&code=secret-code", 400, false, false},
		{"duplicate-code", "GET", "/callback?state=expected&code=a&code=b", 303, true, true},
		{"mixed-error", "GET", "/callback?state=expected&code=a&error=&error=access_denied", 303, true, true},
		{"invalid-query", "GET", "/callback?state=expected&code=%zz", 400, false, false},
		{"denied", "GET", "/callback?state=expected&error=access_denied", 303, true, true},
		{"empty-code", "GET", "/callback?state=expected&code=", 303, true, true},
		{"method", "POST", "/callback?state=expected&code=secret-code", 405, false, false},
		{"path", "GET", "/other?state=expected&code=secret-code", 404, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			received := make(chan callbackResult, 1)
			handler := oauthCallback("expected", received)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.status || strings.Contains(response.Body.String(), "secret-code") || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("callback response did not enforce its boundary")
			}
			if len(received) != 0 != test.receives {
				t.Fatal("callback notification mismatch")
			}
			if test.receives {
				result := <-received
				if (result.failure != "") != test.failure {
					t.Fatal("callback result mismatch")
				}
				// Draining the channel must not make a consent callback replayable.
				replay := httptest.NewRecorder()
				handler.ServeHTTP(replay, httptest.NewRequest("GET", "/callback?state=expected&code=second", nil))
				if replay.Code != http.StatusConflict || len(received) != 0 {
					t.Fatal("callback replay was accepted")
				}
			}
		})
	}
}

func TestOAuthMetadataRejectsOtherOrigins(t *testing.T) {
	for _, raw := range []string{
		"http://login-stg.convai.com/auth/oauth/token", "https://login.convai.com/auth/oauth/token",
		"https://login-stg.convai.com.attacker.invalid/token", "https://user:secret@login-stg.convai.com/token",
		"https://login-stg.convai.com:444/token", "https://login-stg.convai.com/token?secret=x",
		"https://login-stg.convai.com/token#fragment", "https://login-stg.convai.com",
		"https://login-stg.convai.com/token?",
		"https://login-stg.convai.com/%2ftoken",
	} {
		if _, err := verifyOAuthEndpoint(raw); err == nil {
			t.Fatal("unexpected issuer endpoint was accepted")
		}
	}
	if path, err := verifyOAuthEndpoint(stagingIssuer + "/auth/oauth/token"); err != nil || path != "/auth/oauth/token" {
		t.Fatal("valid staging endpoint was rejected")
	}
}
