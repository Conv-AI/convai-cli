package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

func TestCredentialAndAuthorizationMatrix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, token, header string
		kind                config.CredentialType
	}{
		{"API key", "01234567890123456789012345678901", "CONVAI-API-KEY", config.CredentialAPIKey},
		{"PAT", "cvpat_01234567890123456789012345678901", "Authorization", config.CredentialPAT},
		{"OAuth", "cvoat_01234567890123456789012345678901", "Authorization", config.CredentialOAuth},
	} {
		for _, status := range []int{200, 401, 403, 404, 409, 422, 429, 502} {
			t.Run(tc.name+"/"+http.StatusText(status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					expected := tc.token
					if tc.header == "Authorization" {
						expected = "Bearer " + tc.token
					}
					if r.Header.Get(tc.header) != expected {
						t.Error("wrong credential header")
					}
					other := "Authorization"
					if tc.header == other {
						other = "CONVAI-API-KEY"
					}
					if r.Header.Get(other) != "" {
						t.Error("mixed credential modes")
					}
					if r.URL.Query().Get("workspace_id") != "df076c58-43f5-11f1-a81f-42010a080006" {
						t.Error("missing Workspace boundary")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Request-ID", "request-123")
					w.WriteHeader(status)
					if status == 200 {
						_, _ = io.WriteString(w, `{"items":[],"total":0}`)
					} else {
						_, _ = io.WriteString(w, `{"message":"denied `+tc.token+`"}`)
					}
				}))
				defer server.Close()
				client := newTestClient(t, server, credential.Credential{Type: tc.kind, Value: tc.token}).WithWorkspace("df076c58-43f5-11f1-a81f-42010a080006")
				_, _, err := client.ListCharacters(context.Background(), 1, 0)
				if status == 200 {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var failure *Error
				if !errors.As(err, &failure) || failure.StatusCode != status || failure.RequestID != "request-123" {
					t.Fatalf("wrong API error: %v", err)
				}
				if strings.Contains(err.Error(), tc.token) {
					t.Fatal("credential leaked")
				}
			})
		}
	}
}

func TestCredentialsNeverFollowRedirects(t *testing.T) {
	t.Parallel()
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := newTestClient(t, server, credential.Credential{Type: config.CredentialAPIKey, Value: "01234567890123456789012345678901"})
	_, _, err := client.ListCharacters(context.Background(), 1, 0)
	if err == nil || leaked {
		t.Fatalf("redirect result error=%v, leaked=%v", err, leaked)
	}
}

func TestLongCredentialRedactedBeforeMessageTruncation(t *testing.T) {
	t.Parallel()
	secret := "cvpat_" + strings.Repeat("a", 500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"message":"denied `+secret+`"}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, credential.Credential{Type: config.CredentialPAT, Value: secret})
	_, _, err := client.ListCharacters(context.Background(), 1, 0)
	if err == nil || strings.Contains(err.Error(), "cvpat_") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatal("long credential leaked or not redacted")
	}
}
