package e2e

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type callbackResult struct{ code, failure string }

func oauthCallback(state string, received chan<- callbackResult) http.Handler {
	var consumed atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/done" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprint(w, "Consent received. CLI staging tests are running; temporary tokens will be revoked.")
			return
		}
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query["state"]) != 1 || query.Get("state") != state {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result := callbackResult{code: query.Get("code")}
		if len(query["code"]) != 1 || result.code == "" || len(query["error"]) != 0 {
			result = callbackResult{failure: "OAuth consent denied or malformed callback"}
		}
		if !consumed.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		select {
		case received <- result:
			http.Redirect(w, r, "/done", http.StatusSeeOther)
		default:
			w.WriteHeader(http.StatusConflict)
		}
	})
}

func verifyOAuthEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "login-stg.convai.com" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || u.Path == "" || u.RawPath != "" {
		return "", errors.New("OAuth metadata contains an unexpected staging endpoint")
	}
	return u.Path, nil
}

func (c managementClient) authorizeOAuth(t *testing.T, scopes []string) (issuedCredential, error) {
	t.Helper()
	var metadata struct {
		Issuer    string `json:"issuer"`
		Authorize string `json:"authorization_endpoint"`
		Token     string `json:"token_endpoint"`
	}
	if err := c.request("GET", "/.well-known/oauth-authorization-server", "", "", nil, 200, &metadata); err != nil {
		return issuedCredential{}, err
	}
	if metadata.Issuer != stagingIssuer {
		return issuedCredential{}, errors.New("unexpected staging OAuth issuer")
	}
	if _, err := verifyOAuthEndpoint(metadata.Authorize); err != nil {
		return issuedCredential{}, err
	}
	tokenPath, err := verifyOAuthEndpoint(metadata.Token)
	if err != nil {
		return issuedCredential{}, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return issuedCredential{}, errors.New("cannot open loopback callback")
	}
	state, verifier := rand.Text(), rand.Text()+rand.Text()
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	redirect := "http://" + listener.Addr().String() + "/callback"
	received := make(chan callbackResult, 1)
	server := &http.Server{Handler: oauthCallback(state, received), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	parameters := url.Values{
		"response_type": {"code"}, "client_id": {"codex"}, "redirect_uri": {redirect},
		"scope": {strings.Join(scopes, " ")}, "state": {state},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {stagingResource},
	}
	t.Logf("Complete staging browser consent: %s?%s", metadata.Authorize, parameters.Encode())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var result callbackResult
	select {
	case result = <-received:
	case <-ctx.Done():
		return issuedCredential{}, errors.New("staging browser consent timed out")
	}
	if result.failure != "" {
		return issuedCredential{}, errors.New(result.failure)
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"codex"}, "redirect_uri": {redirect},
		"code_verifier": {verifier}, "code": {result.code}, "resource": {stagingResource},
	}
	return c.exchangeOAuth(tokenPath, form)
}
