package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Conv-AI/convai-cli/internal/credential"
)

// ErrDenied reports that the user declined consent or Auth returned an error.
var ErrDenied = errors.New("Convai sign-in was denied or failed in the browser")

// Random returns a URL-safe value with 256 bits of entropy, used for state and
// as a 43-character PKCE verifier.
func Random() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate sign-in secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// Challenge derives the S256 PKCE challenge for a verifier.
func Challenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

type callbackResult struct {
	code string
	err  error
}

// Callback is a single-use loopback redirect receiver bound to 127.0.0.1 on a
// random port. It accepts exactly one GET of the exact callback path, for the
// exact Host it is listening on, carrying the expected state.
type Callback struct {
	RedirectURI string
	server      *http.Server
	result      chan callbackResult
}

func StartCallback(state, issuer string, requireIssuer bool) (*Callback, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open loopback sign-in callback: %w", err)
	}
	host := listener.Addr().String()
	callback := &Callback{
		RedirectURI: "http://" + host + CallbackPath,
		result:      make(chan callbackResult, 1),
	}
	callback.server = &http.Server{
		Handler:           callbackHandler(host, state, issuer, requireIssuer, callback.result),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = callback.server.Serve(listener) }()
	return callback, nil
}

// Wait returns the authorization code, or an error on denial, malformed
// callback, cancellation, or timeout.
func (c *Callback) Wait(ctx context.Context) (string, error) {
	select {
	case result := <-c.result:
		return result.code, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (c *Callback) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c.server.Shutdown(ctx) != nil {
		_ = c.server.Close()
	}
}

func callbackHandler(host, state, issuer string, requireIssuer bool, result chan<- callbackResult) http.Handler {
	var consumed atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch {
		case r.Method != http.MethodGet:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		case r.URL.Path != CallbackPath || r.URL.RawPath != "":
			w.WriteHeader(http.StatusNotFound)
			return
		case r.Host != host:
			// Rejects DNS-rebinding and any request not addressed to this listener.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query["state"]) != 1 || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
			// An unrelated local request must not end the pending sign-in.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !consumed.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		outcome := parseCallback(query, issuer, requireIssuer)
		result <- outcome
		if outcome.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintln(w, "Convai CLI sign-in was not completed. Return to the terminal for details.")
			return
		}
		// The code is not exchanged yet; the terminal reports the final result.
		_, _ = fmt.Fprintln(w, "Convai CLI authorization received. Return to the terminal to finish signing in; you can close this window.")
	})
}

func parseCallback(query url.Values, issuer string, requireIssuer bool) callbackResult {
	issuers := query["iss"]
	if len(issuers) > 1 || (len(issuers) == 1 && issuers[0] != issuer) {
		return callbackResult{err: errors.New("Convai sign-in callback came from an unexpected issuer")}
	}
	if values, ok := query["error"]; ok {
		code := ""
		if len(values) == 1 && isProtocolToken(values[0]) {
			code = values[0]
		}
		if code == "" {
			return callbackResult{err: ErrDenied}
		}
		return callbackResult{err: fmt.Errorf("%w: %s", ErrDenied, code)}
	}
	codes := query["code"]
	if len(codes) != 1 || codes[0] == "" {
		return callbackResult{err: errors.New("Convai sign-in callback was malformed")}
	}
	if requireIssuer && len(issuers) != 1 {
		return callbackResult{err: errors.New("Convai sign-in callback omitted its issuer")}
	}
	return callbackResult{code: codes[0]}
}

// isProtocolToken accepts an RFC 6749 error code shape that cannot carry a
// credential-shaped value.
func isProtocolToken(value string) bool {
	return value != "" && len(value) <= 64 && strings.Trim(value, "abcdefghijklmnopqrstuvwxyz_") == "" &&
		credential.Redact(value) == value
}
