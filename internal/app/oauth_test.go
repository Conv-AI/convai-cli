package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"github.com/Conv-AI/convai-cli/internal/oauth"
)

// fakeAuth is a local Auth stub with Convai's OAuth semantics: PKCE S256
// codes, single-use rotating refresh tokens, paired revocation, and an
// introspection view for the REST stub.
type fakeAuth struct {
	t      *testing.T
	server *httptest.Server

	mu            sync.Mutex
	seq           int
	codes         map[string]fakeCode
	refresh       map[string]*fakeGrant
	access        map[string]*fakeGrant
	revoked       []string
	authorizeURLs []string
	tokenRequests int
	refreshCalls  int
	revokeStatus  int
	deny          bool
	grant         func(requested []string) []string
	refreshReply  func(http.ResponseWriter) bool
}

type fakeCode struct {
	challenge, redirect string
	scopes              []string
}

type fakeGrant struct {
	access, refresh string
	scopes          []string
	consumed        bool
	revoked         bool
}

func newFakeAuth(t *testing.T) *fakeAuth {
	t.Helper()
	f := &fakeAuth{t: t, codes: map[string]fakeCode{}, refresh: map[string]*fakeGrant{}, access: map[string]*fakeGrant{}, revokeStatus: http.StatusOK}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeAuth) serve(w http.ResponseWriter, r *http.Request) {
	issuer := f.server.URL
	switch r.URL.Path {
	case "/.well-known/oauth-authorization-server":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/auth/oauth/authorize",
			"token_endpoint": issuer + "/auth/oauth/token", "revocation_endpoint": issuer + "/auth/oauth/revoke",
			"code_challenge_methods_supported":               []string{"S256"},
			"authorization_response_iss_parameter_supported": true,
		})
	case "/auth/oauth/token":
		f.token(w, r)
	case "/auth/oauth/revoke":
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.PostForm.Get("client_id") != oauth.ClientID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		token := r.PostForm.Get("token")
		f.revoked = append(f.revoked, token)
		if f.revokeStatus != http.StatusOK {
			w.WriteHeader(f.revokeStatus)
			return
		}
		if grant := f.refresh[token]; grant != nil {
			grant.revoked = true
		}
		if grant := f.access[token]; grant != nil {
			grant.revoked = true
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAuth) issue(scopes []string) *fakeGrant {
	f.seq++
	grant := &fakeGrant{
		access:  fmt.Sprintf("cvoat_%032x_YWNjZXNzLXNlY3JldA", f.seq),
		refresh: fmt.Sprintf("cvort_%032x_cmVmcmVzaC1zZWNyZXQ", f.seq),
		scopes:  scopes,
	}
	f.access[grant.access] = grant
	f.refresh[grant.refresh] = grant
	return grant
}

func (f *fakeAuth) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	form := r.PostForm
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenRequests++
	fail := func(code string) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	if form.Get("client_id") != oauth.ClientID || form.Has("resource") || form.Has("client_secret") {
		f.t.Errorf("token request binding = %v", form)
		fail("invalid_request")
		return
	}
	var grant *fakeGrant
	switch form.Get("grant_type") {
	case "authorization_code":
		code, ok := f.codes[form.Get("code")]
		delete(f.codes, form.Get("code"))
		digest := sha256.Sum256([]byte(form.Get("code_verifier")))
		if !ok || code.redirect != form.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(digest[:]) != code.challenge {
			fail("invalid_grant")
			return
		}
		scopes := code.scopes
		if f.grant != nil {
			scopes = f.grant(scopes)
		}
		grant = f.issue(scopes)
	case "refresh_token":
		f.refreshCalls++
		old := f.refresh[form.Get("refresh_token")]
		if old == nil || old.consumed || old.revoked {
			fail("invalid_grant")
			return
		}
		old.consumed = true
		if access := f.access[old.access]; access != nil {
			access.revoked = true
		}
		grant = f.issue(old.scopes)
		if f.refreshReply != nil && f.refreshReply(w) {
			return
		}
	default:
		fail("unsupported_grant_type")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": grant.access, "token_type": "Bearer", "expires_in": 3600,
		"refresh_token": grant.refresh, "scope": strings.Join(grant.scopes, " "),
	})
}

// visit plays the user's browser: it validates the authorization request,
// records consent, and follows the loopback redirect.
func (f *fakeAuth) visit(raw string) {
	parsed, err := url.Parse(raw)
	if err != nil {
		f.t.Errorf("authorization URL: %v", err)
		return
	}
	query := parsed.Query()
	f.mu.Lock()
	f.authorizeURLs = append(f.authorizeURLs, raw)
	redirect := query.Get("redirect_uri")
	callback := url.Values{"state": {query.Get("state")}}
	if parsed.Path != "/auth/oauth/authorize" || query.Get("client_id") != oauth.ClientID || query.Get("response_type") != "code" ||
		query.Get("code_challenge_method") != "S256" || len(query.Get("code_challenge")) != 43 || query.Has("resource") ||
		!strings.HasPrefix(redirect, "http://127.0.0.1:") || !strings.HasSuffix(redirect, "/callback") {
		f.t.Errorf("authorization request = %s", raw)
	}
	if f.deny {
		callback.Set("error", "access_denied")
	} else {
		f.seq++
		code := fmt.Sprintf("cvoac_%032x_Y29kZQ", f.seq)
		f.codes[code] = fakeCode{challenge: query.Get("code_challenge"), redirect: redirect, scopes: strings.Fields(query.Get("scope"))}
		callback.Set("code", code)
		callback.Set("iss", f.server.URL)
	}
	f.mu.Unlock()
	response, err := http.Get(redirect + "?" + callback.Encode())
	if err == nil {
		response.Body.Close()
	}
}

func (f *fakeAuth) active(access string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	grant := f.access[access]
	return grant != nil && !grant.revoked
}

func (f *fakeAuth) revokedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.revoked)
}

func (f *fakeAuth) counts() (token, refresh int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenRequests, f.refreshCalls
}

func (f *fakeAuth) issued() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	values := []string{}
	for token := range f.access {
		values = append(values, token)
	}
	for token := range f.refresh {
		values = append(values, token)
	}
	for code := range f.codes {
		values = append(values, code)
	}
	return values
}

// fakeREST is a Character API stub that accepts only active OAuth access
// tokens, mirroring introspection.
type fakeREST struct {
	server *httptest.Server
	mu     sync.Mutex
	bearer []string
	status int
}

func newFakeREST(t *testing.T, auth *fakeAuth) *fakeREST {
	t.Helper()
	rest := &fakeREST{}
	rest.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		rest.mu.Lock()
		rest.bearer = append(rest.bearer, token)
		status := rest.status
		rest.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if (strings.HasPrefix(token, "cvoat_") && !auth.active(token)) || token == "" {
			status = http.StatusUnauthorized
		}
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":"denied"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[],"total":0,"limit":1,"offset":0}`))
	}))
	t.Cleanup(rest.server.Close)
	return rest
}

func (r *fakeREST) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bearer)
}

// sharedStore is a concurrency-safe keychain stand-in with failure injection.
type sharedStore struct {
	mu        sync.Mutex
	values    map[string]credential.Credential
	setErr    error
	deleteErr error
	getErr    error
}

func (s *sharedStore) Get(profile string) (credential.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return credential.Credential{}, s.getErr
	}
	value, ok := s.values[profile]
	if !ok {
		return credential.Credential{}, credential.ErrNotFound
	}
	return value, nil
}

func (s *sharedStore) Set(profile string, value credential.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.values[profile] = value
	return nil
}

func (s *sharedStore) Delete(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.values[profile]; !ok {
		return credential.ErrNotFound
	}
	delete(s.values, profile)
	return nil
}

func (s *sharedStore) stored(profile string) (credential.Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[profile]
	return value, ok
}

// fileConfigStore is the real FileStore in a temporary directory, including its
// cross-process lock, with save-failure injection.
type fileConfigStore struct {
	*config.FileStore
	mu      sync.Mutex
	saveErr error
}

func (s *fileConfigStore) Save(cfg config.Config) error {
	s.mu.Lock()
	err := s.saveErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.FileStore.Save(cfg)
}

func (s *fileConfigStore) failSaves(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveErr = err
}

// edit plays another process changing the configuration under its lock.
func (s *fileConfigStore) edit(t *testing.T, change func(*config.Config)) {
	t.Helper()
	unlock, err := s.FileStore.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cfg := s.current(t)
	change(&cfg)
	if err := s.FileStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func (s *fileConfigStore) current(t *testing.T) config.Config {
	t.Helper()
	cfg, err := s.FileStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

type oauthFixture struct {
	t           *testing.T
	auth        *fakeAuth
	rest        *fakeREST
	other       *fakeREST
	store       *sharedStore
	configs     *fileConfigStore
	lockDir     string
	now         time.Time
	nowMu       sync.Mutex
	interactive bool
	browser     func(string) error
	timeout     time.Duration
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	auth := newFakeAuth(t)
	f := &oauthFixture{
		t: t, auth: auth, rest: newFakeREST(t, auth), other: newFakeREST(t, auth),
		store:   &sharedStore{values: map[string]credential.Credential{}},
		lockDir: t.TempDir(), now: time.Now(), interactive: true,
	}
	f.browser = func(raw string) error { go auth.visit(raw); return nil }
	cfg := config.Default()
	profile := cfg.Profiles["default"]
	profile.APIURL = f.rest.server.URL
	cfg.Profiles["default"] = profile
	f.configs = &fileConfigStore{FileStore: &config.FileStore{Path: filepath.Join(t.TempDir(), "convai", "config.yaml")}}
	if err := f.configs.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *oauthFixture) advance(d time.Duration) {
	f.nowMu.Lock()
	defer f.nowMu.Unlock()
	f.now = f.now.Add(d)
}

func (f *oauthFixture) clock() time.Time {
	f.nowMu.Lock()
	defer f.nowMu.Unlock()
	return f.now
}

// run executes one CLI invocation; separate invocations model separate
// processes sharing the keychain, config, and lock directory.
func (f *oauthFixture) run(ctx context.Context, stdin string, args ...string) (string, string, error) {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	root, err := New(Options{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
		ConfigStore: f.configs, CredentialStore: f.store, HTTPClient: f.auth.server.Client(),
		Locker: credential.FileLocker{Dir: f.lockDir}, OpenBrowser: f.browser,
		Interactive: func() bool { return f.interactive }, Now: f.clock, LoginTimeout: f.timeout,
		OAuthEnvironments: []oauth.Environment{
			{APIURL: f.rest.server.URL, Issuer: f.auth.server.URL},
			{APIURL: f.other.server.URL, Issuer: f.auth.server.URL},
		},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	root.SetArgs(args)
	err = root.ExecuteContext(ctx)
	f.assertNoSecrets(stdout.String(), stderr.String(), err)
	return stdout.String(), stderr.String(), err
}

func (f *oauthFixture) exec(args ...string) (string, string, error) {
	f.t.Helper()
	return f.run(context.Background(), "", args...)
}

func (f *oauthFixture) assertNoSecrets(outputs ...any) {
	f.t.Helper()
	for _, value := range outputs {
		text := fmt.Sprint(value)
		if err, ok := value.(error); ok && err != nil {
			text = err.Error() + " " + RedactError(err)
		}
		for _, secret := range f.auth.issued() {
			if strings.Contains(text, secret) {
				f.t.Fatalf("CLI output exposed an issued credential: %q", text)
			}
		}
		if strings.Contains(text, "cvort_") || strings.Contains(text, "cvoac_") {
			f.t.Fatalf("CLI output exposed a refresh token or code: %q", text)
		}
	}
}

func (f *oauthFixture) session() credential.Credential {
	f.t.Helper()
	value, ok := f.store.stored("default")
	if !ok || !value.Managed() {
		f.t.Fatalf("no managed session stored: %+v", value)
	}
	return value
}

func (f *oauthFixture) login(args ...string) {
	f.t.Helper()
	if _, stderr, err := f.exec(append([]string{"auth", "login"}, args...)...); err != nil {
		f.t.Fatalf("login: %v (%s)", err, stderr)
	}
}

func TestBrowserLoginRequestRefreshRotationAndLogout(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	stdout, stderr, err := f.exec("auth", "login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(stdout, "Signed in with browser login for profile default (scopes: character:read character:write)") ||
		!strings.Contains(stderr, f.auth.server.URL+"/auth/oauth/authorize?") {
		t.Fatalf("login output = %q / %q", stdout, stderr)
	}
	first := f.session()
	if f.configs.current(t).Profiles["default"].CredentialType != config.CredentialOAuth || first.Session.ClientID != oauth.ClientID ||
		first.Session.Audience != oauth.Audience || first.Session.Issuer != f.auth.server.URL || first.Session.APIURL != f.rest.server.URL {
		t.Fatalf("session binding = %+v", first.Session)
	}
	authorize, _ := url.Parse(f.auth.authorizeURLs[0])
	if authorize.Query().Get("scope") != "character:read character:write" {
		t.Fatalf("requested scope = %q", authorize.Query().Get("scope"))
	}
	if requests := f.rest.requests(); len(requests) != 1 || requests[0] != first.Value {
		t.Fatal("login must verify with the new access token")
	}

	if _, _, err := f.exec("character", "list", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	if _, refreshes := f.auth.counts(); refreshes != 0 {
		t.Fatal("a fresh session was refreshed")
	}
	f.advance(59 * time.Minute)
	if _, _, err := f.exec("character", "list", "-o", "json"); err != nil {
		t.Fatalf("expiring session: %v", err)
	}
	second := f.session()
	if _, refreshes := f.auth.counts(); refreshes != 1 || second.Value == first.Value || second.Session.RefreshToken == first.Session.RefreshToken {
		t.Fatal("expiring session did not rotate exactly once")
	}
	if requests := f.rest.requests(); requests[len(requests)-1] != second.Value || f.auth.active(first.Value) {
		t.Fatal("REST must receive only the rotated access token")
	}

	status, _, err := f.exec("auth", "status", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if json.Unmarshal([]byte(status), &metadata) != nil || metadata["login"] != "browser" || metadata["client_id"] != oauth.ClientID || metadata["source"] != "keyring" {
		t.Fatalf("status = %s", status)
	}

	stdout, _, err = f.exec("auth", "logout")
	if err != nil || !strings.Contains(stdout, "Revoked browser login") {
		t.Fatalf("logout = %q, %v", stdout, err)
	}
	if revoked := f.auth.revokedTokens(); !slices.Equal(revoked, []string{second.Session.RefreshToken, second.Value}) {
		t.Fatalf("logout revoked %d tokens", len(revoked))
	}
	if _, ok := f.store.stored("default"); ok {
		t.Fatal("logout kept the keychain entry")
	}
	if _, _, err := f.exec("character", "list"); err == nil {
		t.Fatal("logged-out profile still authenticated")
	}
}

func TestEquivalentStoredAPIURLUsesTrustedMappingAndBinding(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	// A legacy or hand-edited profile keeps its trailing slash until saved.
	trailingSlash := func(cfg *config.Config) {
		profile := cfg.Profiles["default"]
		profile.APIURL = f.rest.server.URL + "/"
		cfg.Profiles["default"] = profile
	}
	f.configs.edit(t, trailingSlash)
	f.login()
	if got := f.session().Session.APIURL; got != f.rest.server.URL {
		t.Fatalf("session bound to %q", got)
	}
	f.configs.edit(t, trailingSlash)
	if _, _, err := f.exec("character", "list"); err != nil {
		t.Fatalf("equivalent API URL broke the session binding: %v", err)
	}
	if _, _, err := f.exec("character", "list", "--api-url", f.rest.server.URL+"/"); err != nil {
		t.Fatalf("equivalent --api-url broke the session binding: %v", err)
	}
	if _, _, err := f.exec("character", "list", "--api-url", strings.Replace(f.rest.server.URL, "http://", "HTTP://", 1)); err != nil {
		t.Fatalf("equivalent scheme case broke the session binding: %v", err)
	}
	// Canonicalization does not widen trust: other origins stay untrusted.
	for _, apiURL := range []string{"https://custom.example.com/", strings.Replace(f.rest.server.URL, "127.0.0.1", "localhost", 1) + "/"} {
		if _, _, err := f.exec("auth", "login", "--api-url", apiURL); err == nil || !strings.Contains(err.Error(), "--method token") {
			t.Fatalf("login for %s = %v", apiURL, err)
		}
	}
	if _, _, err := f.exec("character", "list", "--api-url", "http://api2.convai.com/"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("non-HTTPS origin = %v", err)
	}
}

// Equivalent spellings of a production API URL use its trusted issuer, while
// look-alike origins stay custom. Noninteractive login stops after the trust
// decision, so no network is used.
func TestProductionAPIURLEquivalentsUseTrustedMapping(t *testing.T) {
	t.Parallel()
	for apiURL, trusted := range map[string]bool{
		"https://api2.convai.com":             true,
		"HTTPS://API2.CONVAI.COM/":            true,
		"https://Api2-Stg.Convai.com:443":     true,
		"https://api2-preview.convai.com:443": true,
		"https://api2.convai.com:8443":        false,
		"https://api2.convai.com.":            false,
		"https://api2.convai.com.example.com": false,
	} {
		root, err := New(Options{
			Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
			ConfigStore:     &memoryConfigStore{cfg: config.Default()},
			CredentialStore: &memoryCredentialStore{values: map[string]credential.Credential{}},
			Locker:          testLocker(t), Interactive: func() bool { return false },
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("trust classification used the network")
				return nil, errors.New("unexpected request")
			})},
		})
		if err != nil {
			t.Fatal(err)
		}
		root.SetArgs([]string{"auth", "login", "--api-url", apiURL})
		err = root.Execute()
		if trusted != (err != nil && strings.Contains(err.Error(), "interactive terminal")) {
			t.Fatalf("%s trusted=%v, error = %v", apiURL, trusted, err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// duringConsent runs CLI commands as another process while the user is on
// the consent page. They would block on the configuration lock if login held
// it across consent.
func (f *oauthFixture) duringConsent(commands ...[]string) {
	f.browser = func(raw string) error {
		for _, args := range commands {
			if _, _, err := f.exec(args...); err != nil {
				f.t.Errorf("%v during consent: %v", args, err)
			}
		}
		go f.auth.visit(raw)
		return nil
	}
}

func TestBrowserLoginMergesConfigChangesMadeDuringConsent(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	const workspace = "11111111-1111-4111-8111-111111111111"
	f.duringConsent(
		[]string{"config", "set", "workspace-id", workspace, "--profile", "default"},
		[]string{"config", "set", "api-url", "https://custom.example.com", "--profile", "other"},
		[]string{"config", "use", "other"},
	)
	f.login("--workspace", "personal")
	cfg := f.configs.current(t)
	saved := cfg.Profiles["default"]
	if saved.WorkspaceID != workspace || saved.CredentialType != config.CredentialOAuth || saved.APIURL != f.rest.server.URL {
		t.Fatalf("saved profile = %+v", saved)
	}
	if other, ok := cfg.Profiles["other"]; !ok || other.APIURL != "https://custom.example.com" {
		t.Fatal("login discarded a concurrently created profile")
	}
	if cfg.CurrentProfile != "other" {
		t.Fatal("login overrode a concurrent current-profile change")
	}
	f.session()
}

func TestBrowserLoginRejectsConflictingConfigChangesDuringConsent(t *testing.T) {
	t.Parallel()
	t.Run("API URL changed", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.duringConsent([]string{"config", "set", "api-url", f.other.server.URL, "--profile", "default"})
		_, _, err := f.exec("auth", "login")
		if err == nil || !strings.Contains(err.Error(), "changed to "+f.other.server.URL) || !strings.Contains(err.Error(), "was revoked") {
			t.Fatalf("error = %v", err)
		}
		saved := f.configs.current(t).Profiles["default"]
		if saved.APIURL != f.other.server.URL || saved.CredentialType != config.CredentialPAT {
			t.Fatalf("conflicting change was overwritten: %+v", saved)
		}
		if _, ok := f.store.stored("default"); ok || len(f.auth.revokedTokens()) != 2 {
			t.Fatal("conflicting login was stored or not revoked")
		}
	})
	t.Run("profile removed", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.configs.edit(t, func(cfg *config.Config) {
			cfg.Profiles["spare"] = config.Profile{APIURL: config.DefaultAPIURL, CredentialType: config.CredentialPAT}
		})
		f.browser = func(raw string) error {
			f.configs.edit(t, func(cfg *config.Config) {
				delete(cfg.Profiles, "default")
				cfg.CurrentProfile = "spare"
			})
			go f.auth.visit(raw)
			return nil
		}
		_, _, err := f.exec("auth", "login", "--profile", "default")
		if err == nil || !strings.Contains(err.Error(), "was removed") || !strings.Contains(err.Error(), "was revoked") {
			t.Fatalf("error = %v", err)
		}
		if _, ok := f.configs.current(t).Profiles["default"]; ok {
			t.Fatal("login resurrected a removed profile")
		}
		if _, ok := f.store.stored("default"); ok {
			t.Fatal("login stored a credential for a removed profile")
		}
	})
}

func TestConcurrentConfigWritersKeepEveryChange(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	const writers = 12
	var wait sync.WaitGroup
	for i := range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			profile := fmt.Sprintf("p%d", i)
			workspace := fmt.Sprintf("%08d-1111-4111-8111-111111111111", i)
			if _, _, err := f.exec("config", "set", "workspace-id", workspace, "--profile", profile); err != nil {
				t.Errorf("config set %s: %v", profile, err)
			}
		}()
	}
	// A login for another profile runs concurrently with the writers.
	wait.Add(1)
	go func() {
		defer wait.Done()
		if _, _, err := f.exec("auth", "login"); err != nil {
			t.Errorf("login: %v", err)
		}
	}()
	wait.Wait()
	cfg := f.configs.current(t)
	for i := range writers {
		want := fmt.Sprintf("%08d-1111-4111-8111-111111111111", i)
		if got := cfg.Profiles[fmt.Sprintf("p%d", i)].WorkspaceID; got != want {
			t.Fatalf("profile p%d workspace = %q; a concurrent write was lost", i, got)
		}
	}
	if cfg.Profiles["default"].CredentialType != config.CredentialOAuth {
		t.Fatal("concurrent writers lost the login metadata")
	}
}

func TestSerializedSessionIsNeverImportedAsACredential(t *testing.T) {
	f := newOAuthFixture(t)
	f.login("--read-only")
	session := f.session()
	s := session.Session
	data, err := json.Marshal(map[string]any{
		"convai_credential_version": 1, "kind": "oauth_session", "access_token": session.Value,
		"refresh_token": s.RefreshToken, "expires_at": s.ExpiresAt.Format(time.RFC3339), "scope": "character:read",
		"issuer": s.Issuer, "client_id": s.ClientID, "audience": s.Audience, "api_url": s.APIURL,
	})
	if err != nil || len(data) > 512 || bytes.ContainsAny(data, " \t\n") {
		t.Fatalf("fixture must be a compact record (%d bytes)", len(data))
	}
	before := len(f.rest.requests())
	for _, args := range [][]string{
		{"auth", "login", "--token-stdin"},
		{"auth", "login", "--method", "token", "--token-stdin", "--no-verify"},
	} {
		if _, _, err := f.run(context.Background(), string(data)+"\n", args...); err == nil {
			t.Fatalf("%v imported a serialized session", args)
		}
	}
	t.Setenv(credential.EnvironmentToken, string(data))
	for _, args := range [][]string{{"character", "list"}, {"doctor"}, {"auth", "status", "--check"}} {
		if _, _, err := f.exec(args...); err == nil {
			t.Fatalf("%v used a serialized session from %s", args, credential.EnvironmentToken)
		}
	}
	if len(f.rest.requests()) != before {
		t.Fatal("a serialized session was sent to REST")
	}
	if stored := f.session(); stored.Value != session.Value {
		t.Fatal("a rejected import changed the stored login")
	}
}

func TestBrowserLaunchFailurePrintsURLWithoutCredentials(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	f.browser = func(raw string) error {
		go f.auth.visit(raw) // the user opens the printed URL manually
		return errors.New("no browser")
	}
	_, stderr, err := f.exec("auth", "login")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Could not open a browser automatically.") || !strings.Contains(stderr, f.auth.authorizeURLs[0]) {
		t.Fatalf("stderr = %q", stderr)
	}
	f.session()
}

func TestBrowserLoginRejectsNoninteractiveAndUntrustedAPIsBeforeNetwork(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	f.interactive = false
	_, _, err := f.exec("auth", "login")
	if err == nil || !strings.Contains(err.Error(), "--method token --token-stdin") || !strings.Contains(err.Error(), credential.EnvironmentToken) {
		t.Fatalf("noninteractive error = %v", err)
	}
	f.interactive = true
	_, _, err = f.exec("auth", "login", "--api-url", "https://custom.example.com")
	if err == nil || !strings.Contains(err.Error(), "--method token") {
		t.Fatalf("custom API error = %v", err)
	}
	if token, _ := f.auth.counts(); token != 0 || len(f.auth.authorizeURLs) != 0 {
		t.Fatal("rejected login contacted Auth")
	}
	for _, args := range [][]string{
		{"--method", "browser", "--token-stdin"},
		{"--method", "token", "--scopes", "character:read"},
		{"--method", "token", "--read-only"},
		{"--method", "password"},
		{"--read-only", "--scopes", "character:read"},
		{"--scopes", "chat-history:read"},
		{"--scopes", "admin"},
		{"--scopes", ","},
	} {
		if _, _, err := f.exec(append([]string{"auth", "login"}, args...)...); err == nil {
			t.Fatalf("login %v was accepted", args)
		}
	}
	if len(f.auth.authorizeURLs) != 0 {
		t.Fatal("invalid flags reached the browser")
	}
}

func TestBrowserLoginDenialTimeoutAndCancellationStoreNothing(t *testing.T) {
	t.Parallel()
	t.Run("denied", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.auth.deny = true
		_, _, err := f.exec("auth", "login")
		if !errors.Is(err, oauth.ErrDenied) || !strings.Contains(err.Error(), "access_denied") {
			t.Fatalf("error = %v", err)
		}
		f.assertNothingStored()
	})
	t.Run("timeout", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.timeout = 50 * time.Millisecond
		f.browser = func(string) error { return nil }
		_, _, err := f.exec("auth", "login")
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("error = %v", err)
		}
		f.assertNothingStored()
	})
	t.Run("cancelled", func(t *testing.T) {
		f := newOAuthFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		f.browser = func(string) error { cancel(); return nil }
		_, _, err := f.run(ctx, "", "auth", "login")
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("error = %v", err)
		}
		f.assertNothingStored()
	})
}

func (f *oauthFixture) assertNothingStored() {
	f.t.Helper()
	if _, ok := f.store.stored("default"); ok {
		f.t.Fatal("failed login stored a credential")
	}
	if token, _ := f.auth.counts(); token != 0 {
		f.t.Fatal("failed consent reached the token endpoint")
	}
}

func TestBrowserLoginScopeSelectionAndNarrowing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		args      []string
		grant     func([]string) []string
		requested string
		stored    string
		verifies  bool
	}{
		{"read-only", []string{"--read-only"}, nil, "character:read", "character:read", true},
		{"explicit write only skips read verification", []string{"--scopes", "character:write"}, nil, "character:write", "character:write", false},
		{"explicit list", []string{"--scopes", "character:write, character:read"}, nil, "character:read character:write", "character:read character:write", true},
		{"server narrowing", nil, func([]string) []string { return []string{"character:read"} }, "character:read character:write", "character:read", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOAuthFixture(t)
			f.auth.grant = tc.grant
			f.login(tc.args...)
			authorize, _ := url.Parse(f.auth.authorizeURLs[0])
			if got := authorize.Query().Get("scope"); got != tc.requested {
				t.Fatalf("requested %q", got)
			}
			if got := strings.Join(f.session().Session.Scopes, " "); got != tc.stored {
				t.Fatalf("stored %q", got)
			}
			if verified := len(f.rest.requests()) == 1; verified != tc.verifies {
				t.Fatalf("verification request = %v", verified)
			}
		})
	}
	t.Run("overbroad grant is revoked", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.auth.grant = func([]string) []string { return []string{"character:read", "knowledge-bank:write"} }
		_, _, err := f.exec("auth", "login", "--read-only")
		if !errors.Is(err, oauth.ErrInvalidResponse) {
			t.Fatalf("error = %v", err)
		}
		if len(f.auth.revokedTokens()) != 2 || len(f.rest.requests()) != 0 {
			t.Fatal("overbroad grant was not revoked before use")
		}
		if _, ok := f.store.stored("default"); ok {
			t.Fatal("overbroad grant was stored")
		}
	})
}

func TestManagedLoginVerificationPolicy(t *testing.T) {
	t.Parallel()
	t.Run("authorization denial is a warning", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.rest.status = http.StatusForbidden
		_, stderr, err := f.exec("auth", "login")
		if err != nil || !strings.Contains(stderr, "Warning: signed in") || !strings.Contains(stderr, "HTTP 403") {
			t.Fatalf("403 login = %v / %q", err, stderr)
		}
		f.session()
		if len(f.auth.revokedTokens()) != 0 {
			t.Fatal("authorized login was revoked")
		}
	})
	t.Run("authentication rejection revokes", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.rest.status = http.StatusUnauthorized
		_, _, err := f.exec("auth", "login")
		if err == nil || !strings.Contains(err.Error(), "the new browser login was revoked") {
			t.Fatalf("401 login = %v", err)
		}
		if len(f.auth.revokedTokens()) != 2 {
			t.Fatal("rejected login was not revoked")
		}
		if _, ok := f.store.stored("default"); ok {
			t.Fatal("rejected login was stored")
		}
	})
	t.Run("imported credentials keep strict verification", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.rest.status = http.StatusForbidden
		if _, _, err := f.run(context.Background(), "cvpat_0123456789012345678901234567890123456789\n", "auth", "login", "--token-stdin"); err == nil {
			t.Fatal("imported credential bypassed verification")
		}
	})
}

func TestBrowserLoginStorageFailuresRevokeNewGrant(t *testing.T) {
	t.Parallel()
	t.Run("keychain write", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.store.setErr = errors.New("keychain locked")
		_, _, err := f.exec("auth", "login")
		if err == nil || !strings.Contains(err.Error(), "keychain locked") || !strings.Contains(err.Error(), "was revoked") || len(f.auth.revokedTokens()) != 2 {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("config save", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.configs.failSaves(errors.New("disk full"))
		_, _, err := f.exec("auth", "login")
		if err == nil || !strings.Contains(err.Error(), "disk full") || len(f.auth.revokedTokens()) != 2 {
			t.Fatalf("error = %v", err)
		}
		if _, ok := f.store.stored("default"); ok {
			t.Fatal("config failure left the new login stored")
		}
	})
	t.Run("config save and rollback", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.configs.failSaves(errors.New("disk full"))
		f.store.deleteErr = errors.New("keychain unavailable")
		_, _, err := f.exec("auth", "login")
		if err == nil || !strings.Contains(err.Error(), "removing the stored credential failed: keychain unavailable") || !strings.Contains(err.Error(), "was revoked") {
			t.Fatalf("rollback failure was hidden: %v", err)
		}
	})
	t.Run("import rollback restores previous", func(t *testing.T) {
		f := newOAuthFixture(t)
		previous := credential.Credential{Type: config.CredentialAPIKey, Value: "01234567890123456789012345678901"}
		f.store.values["default"] = previous
		f.configs.failSaves(errors.New("disk full"))
		_, _, err := f.run(context.Background(), "cvpat_0123456789012345678901234567890123456789\n", "auth", "login", "--token-stdin", "--no-verify")
		if err == nil || !strings.Contains(err.Error(), "the previous credential was restored") {
			t.Fatalf("error = %v", err)
		}
		if stored, _ := f.store.stored("default"); stored != previous {
			t.Fatal("previous credential was not restored")
		}
	})
}

func TestReplacingBrowserLoginRevokesPreviousGrantFirst(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.login()
		old := f.session()
		f.login("--read-only")
		if next := f.session(); next.Value == old.Value || f.auth.active(old.Value) {
			t.Fatal("previous grant stayed active")
		}
		if revoked := f.auth.revokedTokens(); !slices.Equal(revoked, []string{old.Session.RefreshToken, old.Value}) {
			t.Fatal("previous grant was not revoked exactly")
		}
	})
	t.Run("previous revocation fails", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.login()
		old := f.session()
		f.auth.mu.Lock()
		f.auth.revokeStatus = http.StatusServiceUnavailable
		f.auth.mu.Unlock()
		_, _, err := f.exec("auth", "login")
		if err == nil || !strings.Contains(err.Error(), "the previous login was kept") {
			t.Fatalf("error = %v", err)
		}
		if kept := f.session(); kept.Value != old.Value || kept.Session.RefreshToken != old.Session.RefreshToken {
			t.Fatal("previous revocation handle was discarded")
		}
	})
	t.Run("persistence fails after previous revocation", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.login()
		old := f.session()
		f.configs.failSaves(errors.New("disk full"))
		_, _, err := f.exec("auth", "login")
		if err == nil || !strings.Contains(err.Error(), "the previous browser login had already been revoked") || strings.Contains(err.Error(), "restored") {
			t.Fatalf("error = %v", err)
		}
		if f.auth.active(old.Value) {
			t.Fatal("previous grant should have been revoked")
		}
		if _, ok := f.store.stored("default"); ok {
			t.Fatal("a revoked login was left in the keychain as if usable")
		}
	})
	t.Run("token import replaces browser login", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.login()
		old := f.session()
		if _, _, err := f.run(context.Background(), "01234567890123456789012345678901\n", "auth", "login", "--method", "token", "--token-stdin", "--no-verify"); err != nil {
			t.Fatal(err)
		}
		if f.auth.active(old.Value) || f.store.values["default"].Managed() {
			t.Fatal("token import left the previous browser grant live")
		}
	})
}

func TestStoredBrowserLoginIsBoundToItsAPIOrigin(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	f.login()
	for _, args := range [][]string{
		{"character", "list", "--api-url", f.other.server.URL},
		{"doctor", "--api-url", f.other.server.URL},
		{"auth", "status", "--check", "--api-url", f.other.server.URL},
		{"character", "list", "--api-url", "https://custom.example.com"},
	} {
		_, _, err := f.exec(args...)
		if err == nil || !strings.Contains(err.Error(), "run `convai auth login --profile default`") {
			t.Fatalf("%v error = %v", args, err)
		}
	}
	if len(f.other.requests()) != 0 {
		t.Fatal("a stored browser login was forwarded to another API origin")
	}
	stdout, _, err := f.exec("auth", "status", "--api-url", f.other.server.URL)
	if err != nil || !strings.Contains(stdout, "Bound API URL: "+f.rest.server.URL) {
		t.Fatalf("status = %q, %v", stdout, err)
	}
}

func TestBrowserLoginLogoutBindingAndFailureSemantics(t *testing.T) {
	t.Parallel()
	t.Run("revocation failure keeps handle", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.login()
		f.auth.mu.Lock()
		f.auth.revokeStatus = http.StatusServiceUnavailable
		f.auth.mu.Unlock()
		_, _, err := f.exec("auth", "logout")
		if err == nil || !strings.Contains(err.Error(), "--local") {
			t.Fatalf("error = %v", err)
		}
		f.session()
		stdout, stderr, err := f.exec("auth", "logout", "--local")
		if err != nil || !strings.Contains(stdout, "Removed local credential") || !strings.Contains(stderr, "was not revoked") {
			t.Fatalf("local logout = %q / %q / %v", stdout, stderr, err)
		}
		if _, ok := f.store.stored("default"); ok {
			t.Fatal("local logout kept the entry")
		}
	})
	for name, mutate := range map[string]func(*credential.Session){
		"client":   func(s *credential.Session) { s.ClientID = "codex" },
		"audience": func(s *credential.Session) { s.Audience = "https://mcp-api.convai.com/mcp" },
		"issuer":   func(s *credential.Session) { s.Issuer = "https://login.convai.com" },
	} {
		t.Run("mismatched "+name, func(t *testing.T) {
			f := newOAuthFixture(t)
			f.login()
			value := f.session()
			session := *value.Session
			mutate(&session)
			value.Session = &session
			f.store.values["default"] = value
			before := len(f.auth.revokedTokens())
			if _, _, err := f.exec("auth", "logout"); err == nil || !strings.Contains(err.Error(), "--local") {
				t.Fatalf("logout error = %v", err)
			}
			if _, _, err := f.exec("character", "list"); err == nil {
				t.Fatal("mismatched session was used")
			}
			if len(f.auth.revokedTokens()) != before || len(f.rest.requests()) != 1 {
				t.Fatal("mismatched session was sent to Auth or REST")
			}
			if _, _, err := f.exec("auth", "logout", "--local"); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("plain credentials stay local", func(t *testing.T) {
		f := newOAuthFixture(t)
		f.store.values["default"] = credential.Credential{Type: config.CredentialPAT, Value: "cvpat_0123456789012345678901234567890123456789"}
		stdout, _, err := f.exec("auth", "logout")
		if err != nil || !strings.Contains(stdout, "Removed credential for profile default") || len(f.auth.revokedTokens()) != 0 {
			t.Fatalf("logout = %q, %v", stdout, err)
		}
	})
}

func TestRefreshFailuresNeverReplayConsumedTokens(t *testing.T) {
	t.Parallel()
	expire := func(f *oauthFixture) credential.Credential {
		f.login()
		f.advance(2 * time.Hour)
		return f.session()
	}
	t.Run("revoked refresh token", func(t *testing.T) {
		f := newOAuthFixture(t)
		old := expire(f)
		f.auth.mu.Lock()
		f.auth.refresh[old.Session.RefreshToken].revoked = true
		f.auth.mu.Unlock()
		_, _, err := f.exec("character", "list")
		if err == nil || !strings.Contains(err.Error(), "no longer valid and was removed") {
			t.Fatalf("error = %v", err)
		}
		f.assertNoReplay(1)
	})
	for name, reply := range map[string]func(http.ResponseWriter) bool{
		"oversized response": func(w http.ResponseWriter) bool {
			_, _ = w.Write([]byte(`{"padding":"` + strings.Repeat("x", 1<<20) + `"}`))
			return true
		},
		"truncated response": func(w http.ResponseWriter) bool {
			w.Header().Set("Content-Length", "500")
			_, _ = w.Write([]byte(`{"token_type":"Bearer"`))
			return true
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newOAuthFixture(t)
			expire(f)
			f.auth.refreshReply = reply
			_, _, err := f.exec("character", "list")
			if !errors.Is(err, oauth.ErrInvalidResponse) || !strings.Contains(err.Error(), "was removed") {
				t.Fatalf("error = %v", err)
			}
			f.assertNoReplay(1)
		})
	}
	t.Run("rotated tokens cannot be persisted", func(t *testing.T) {
		f := newOAuthFixture(t)
		old := expire(f)
		f.store.setErr = errors.New("keychain locked")
		_, _, err := f.exec("character", "list")
		if err == nil || !strings.Contains(err.Error(), "keychain locked") || !strings.Contains(err.Error(), "run `convai auth login") {
			t.Fatalf("error = %v", err)
		}
		f.auth.mu.Lock()
		rotated := f.auth.refresh[old.Session.RefreshToken]
		f.auth.mu.Unlock()
		revoked := f.auth.revokedTokens()
		if len(revoked) != 2 || slices.Contains(revoked, old.Session.RefreshToken) || !rotated.consumed {
			t.Fatal("unsaved rotated grant was not revoked")
		}
		f.store.setErr = nil
		f.assertNoReplay(1)
	})
	t.Run("ambiguous transport failure is not retried", func(t *testing.T) {
		f := newOAuthFixture(t)
		old := expire(f)
		f.auth.refreshReply = func(w http.ResponseWriter) bool {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return true
		}
		_, _, err := f.exec("character", "list")
		if err == nil || !strings.Contains(err.Error(), "may or may not") || !strings.Contains(err.Error(), "it was not retried") {
			t.Fatalf("error = %v", err)
		}
		if _, refreshes := f.auth.counts(); refreshes != 1 || len(f.rest.requests()) != 1 {
			t.Fatal("ambiguous refresh was retried or reached REST")
		}
		// The token was consumed at Auth, so the next attempt is rejected and
		// the dead entry is removed rather than retried again.
		if kept := f.session(); kept.Session.RefreshToken != old.Session.RefreshToken {
			t.Fatal("ambiguous failure changed the stored login")
		}
		f.auth.refreshReply = nil
		if _, _, err := f.exec("character", "list"); err == nil || !strings.Contains(err.Error(), "was removed") {
			t.Fatalf("next command error = %v", err)
		}
		f.assertNoReplay(2)
	})
}

// assertNoReplay checks that the entry is gone, so a further command cannot
// send the consumed refresh token again.
func (f *oauthFixture) assertNoReplay(wantRefreshes int) {
	f.t.Helper()
	if _, ok := f.store.stored("default"); ok {
		f.t.Fatal("a consumed login was kept")
	}
	if _, _, err := f.exec("character", "list"); err == nil || !errors.Is(err, credential.ErrNotFound) {
		f.t.Fatalf("follow-up error = %v", err)
	}
	if _, refreshes := f.auth.counts(); refreshes != wantRefreshes {
		f.t.Fatalf("refresh grants = %d, want %d", refreshes, wantRefreshes)
	}
}

// Concurrent CLI instances share the keychain and the file-lock directory.
// The OS-level cross-process contention of that lock is proved separately by
// credential.TestFileLockerSerializesProcesses with a child process.
func TestConcurrentCLIInstancesRefreshOnce(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	f.login()
	f.advance(2 * time.Hour)
	const processes = 8
	var wait sync.WaitGroup
	errs := make(chan error, processes)
	for range processes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := f.exec("character", "list")
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent command: %v", err)
		}
	}
	if _, refreshes := f.auth.counts(); refreshes != 1 {
		t.Fatalf("refresh grants = %d, want 1", refreshes)
	}
}

func TestStatusCheckAndDoctorRefreshThroughTheSamePath(t *testing.T) {
	t.Parallel()
	for _, command := range [][]string{{"auth", "status", "--check"}, {"doctor"}} {
		f := newOAuthFixture(t)
		f.login()
		f.advance(2 * time.Hour)
		if _, _, err := f.exec(command...); err != nil {
			t.Fatalf("%v: %v", command, err)
		}
		if _, refreshes := f.auth.counts(); refreshes != 1 {
			t.Fatalf("%v did not refresh", command)
		}
	}
}

func TestEnvironmentCredentialOverridesBrowserLoginWithoutRefresh(t *testing.T) {
	f := newOAuthFixture(t)
	f.login()
	f.advance(2 * time.Hour)
	const pat = "cvpat_0123456789012345678901234567890123456789"
	t.Setenv(credential.EnvironmentToken, pat)
	if _, _, err := f.exec("character", "list"); err != nil {
		t.Fatalf("environment credential: %v", err)
	}
	if requests := f.rest.requests(); requests[len(requests)-1] != pat {
		t.Fatal("environment credential did not take precedence")
	}
	if _, refreshes := f.auth.counts(); refreshes != 0 {
		t.Fatal("environment credential triggered a browser-login refresh")
	}
}

func TestNoMutationReplayAfterUnauthorized(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	f.login()
	f.rest.mu.Lock()
	f.rest.status = http.StatusUnauthorized
	f.rest.mu.Unlock()
	before := len(f.rest.requests())
	_, _, err := f.run(context.Background(), `{"backstory":"Updated."}`, "character", "update", "11111111-1111-4111-8111-111111111111", "--file", "-", "--yes")
	if err == nil {
		t.Fatal("unauthorized update succeeded")
	}
	if ReportError(io.Discard, "json", err) != 3 {
		t.Fatal("HTTP 401 must exit 3")
	}
	if got := len(f.rest.requests()) - before; got != 1 {
		t.Fatalf("mutation sent %d times", got)
	}
	if token, refreshes := f.auth.counts(); refreshes != 0 || token != 1 {
		t.Fatal("HTTP 401 triggered a refresh or retry")
	}
}
