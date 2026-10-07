package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCustomResourceBindsAuthorizationExchangeAndRefresh(t *testing.T) {
	const resource = "https://api.customer.test/authoring"
	for _, wrongField := range []string{"", "iss", "resource"} {
		t.Run(wrongField, func(t *testing.T) {
			revoked, calls := 0, 0
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				if r.URL.Path == "/revoke" {
					revoked++
					return
				}
				calls++
				if r.PostForm.Get("resource") != resource || r.PostForm.Get("client_id") != ClientID || r.PostForm.Has("client_secret") {
					t.Error("token request lost resource/client binding")
				}
				body := map[string]any{"access_token": testAccess, "refresh_token": testRefresh, "expires_in": 3600, "token_type": "Bearer", "iss": server.URL, "resource": resource}
				if wrongField != "" {
					body[wrongField] = "https://other.test"
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			client := (&Client{http: NewHTTPClient(server.Client()), now: time.Now, metadata: Metadata{Issuer: server.URL, AuthorizationEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/token", RevocationEndpoint: server.URL + "/revoke"}}).WithResource(resource)
			authorize, _ := url.Parse(client.AuthorizationURL("http://127.0.0.1:1234/callback", "state", "challenge", []string{"character:read"}))
			if authorize.Query().Get("resource") != resource {
				t.Fatal("authorization lost resource")
			}
			for _, grant := range []string{"code", "refresh"} {
				var err error
				if grant == "code" {
					_, err = client.ExchangeCode(context.Background(), "code", "verifier", "redirect", []string{"character:read"})
				} else {
					_, err = client.Refresh(context.Background(), testRefresh, []string{"character:read"})
				}
				if wrongField == "" && err != nil {
					t.Fatal(err)
				}
				if wrongField != "" && !errors.Is(err, ErrInvalidResponse) {
					t.Fatalf("mismatched %s: %v", wrongField, err)
				}
			}
			if calls != 2 || wrongField != "" && revoked != 4 {
				t.Fatalf("calls=%d revocations=%d", calls, revoked)
			}
		})
	}
}

func TestLoopbackCallbackIsPlainNonRedirectingReceipt(t *testing.T) {
	callback, err := StartCallback("state", "https://login.customer.test", true)
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	response, err := http.Get(callback.RedirectURI + "?state=state&code=private-code&iss=https%3A%2F%2Flogin.customer.test")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || response.Header.Get("Location") != "" || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" || strings.Contains(string(body), "private-code") || strings.Contains(string(body), "<script") {
		t.Fatal("callback is not a secret-free, non-redirecting receipt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if code, err := callback.Wait(ctx); err != nil || code != "private-code" {
		t.Fatal("receipt did not deliver code to CLI")
	}
}
