package app

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Conv-AI/convai-cli/internal/config"
	"gopkg.in/yaml.v3"
)

func newOnPremFixture(t *testing.T) *oauthFixture {
	f := newOAuthFixture(t)
	f.custom = true
	handler := f.rest.server.Config.Handler
	f.rest.server.Close()
	f.rest.server = httptest.NewTLSServer(handler)
	t.Cleanup(f.rest.server.Close)
	f.auth.resource = f.rest.server.URL + "/authoring"
	ca := filepath.Join(t.TempDir(), "customer-ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.auth.server.Certificate().Raw})
	if err := os.WriteFile(ca, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f.configs.edit(t, func(cfg *config.Config) {
		p := cfg.Profiles["default"]
		p.APIURL, p.CABundle = f.rest.server.URL, ca
		p.OAuth = config.OAuthTrust{APIURL: p.APIURL, Issuer: f.auth.server.URL, Resource: f.auth.resource}
		cfg.Profiles["default"] = p
	})
	return f
}

func TestOnPremApprovedProfileOAuthLifecycle(t *testing.T) {
	f := newOnPremFixture(t)
	p := f.configs.current(t).Profiles["default"]
	data, err := yaml.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "approved-profile.yaml")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.exec("config", "import", "--file", file, "--profile", "customer"); err != nil {
		t.Fatal(err)
	}
	if f.configs.current(t).Profiles["customer"] != p {
		t.Fatal("import did not preserve exact trust")
	}
	for _, command := range [][]string{{"config", "get", "--output", "json"}, {"auth", "login", "--read-only"}, {"character", "list"}, {"doctor"}, {"auth", "status", "--check"}} {
		if _, _, err := f.exec(append(command, "--profile", "customer")...); err != nil {
			t.Fatal(err)
		}
	}
	first, ok := f.store.stored("customer")
	if !ok || first.Session.Audience != f.auth.resource || first.Session.Resource != f.auth.resource || len(first.Session.CAFingerprint) != 64 {
		t.Fatal("managed record missing resource or CA binding")
	}
	f.advance(time.Hour)
	if _, _, err := f.exec("doctor", "--profile", "customer"); err != nil {
		t.Fatal(err)
	}
	next, _ := f.store.stored("customer")
	if next.Value == first.Value || next.Session.RefreshToken == first.Session.RefreshToken {
		t.Fatal("refresh did not rotate")
	}
	if _, refresh := f.auth.counts(); refresh != 1 {
		t.Fatalf("refresh count = %d", refresh)
	}
	if _, _, err := f.exec("auth", "logout", "--profile", "customer"); err != nil {
		t.Fatal(err)
	}
	if f.auth.active(first.Value) || f.auth.active(next.Value) {
		t.Fatal("logout left access active")
	}
	if _, ok := f.store.stored("customer"); ok {
		t.Fatal("logout kept keychain record")
	}
}

func TestOnPremTrustMismatchNeverSendsAccessOrRefresh(t *testing.T) {
	for _, field := range []string{"issuer", "audience", "resource", "client", "host", "ca", "profile issuer", "profile resource", "profile CA"} {
		t.Run(field, func(t *testing.T) {
			f := newOnPremFixture(t)
			if _, _, err := f.exec("auth", "login"); err != nil {
				t.Fatal(err)
			}
			value, _ := f.store.stored("default")
			session := *value.Session
			value.Session = &session
			switch field {
			case "issuer":
				session.Issuer = "https://other.test"
			case "audience":
				session.Audience = "wrong"
			case "resource":
				session.Resource += "/other"
			case "client":
				session.ClientID = "other-client"
			case "host":
				session.APIURL = "https://other.test"
			case "ca":
				session.CAFingerprint = "wrong"
			default:
				f.configs.edit(t, func(cfg *config.Config) {
					p := cfg.Profiles["default"]
					switch field {
					case "profile issuer":
						p.OAuth.Issuer = "https://other.test"
					case "profile resource":
						p.OAuth.Resource += "/other"
					case "profile CA":
						data, err := os.ReadFile(p.CABundle)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(p.CABundle, append(data, '\n'), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					cfg.Profiles["default"] = p
				})
			}
			if err := f.store.Set("default", value); err != nil {
				t.Fatal(err)
			}
			before := len(f.rest.requests())
			for _, expiry := range []time.Duration{0, time.Hour} {
				f.advance(expiry)
				for _, command := range [][]string{{"character", "list"}, {"doctor"}, {"auth", "status", "--check"}, {"auth", "logout"}} {
					if _, _, err := f.exec(command...); err == nil {
						t.Fatalf("%v accepted mismatched %s", command, field)
					}
				}
			}
			if _, refresh := f.auth.counts(); refresh != 0 || len(f.rest.requests()) != before {
				t.Fatal("mismatched trust sent a credential")
			}
			if len(f.auth.revokedTokens()) != 0 {
				t.Fatal("mismatched trust sent a revocation secret")
			}
			if _, _, err := f.exec("character", "list", "--api-url", "https://other.test"); err == nil {
				t.Fatal("API override transferred custom trust")
			}
			if _, _, err := f.exec("auth", "logout", "--local"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOnPremTrustChangesDuringConsentAndRefresh(t *testing.T) {
	for _, phase := range []string{"consent", "refresh"} {
		t.Run(phase, func(t *testing.T) {
			f := newOnPremFixture(t)
			change := func() {
				f.configs.edit(t, func(cfg *config.Config) {
					p := cfg.Profiles["default"]
					p.OAuth.Resource += "/changed"
					cfg.Profiles["default"] = p
				})
			}
			if phase == "consent" {
				f.browser = func(raw string) error { change(); go f.auth.visit(raw); return nil }
				if _, _, err := f.exec("auth", "login"); err == nil || !strings.Contains(err.Error(), "changed") {
					t.Fatalf("error = %v", err)
				}
			} else {
				if _, _, err := f.exec("auth", "login"); err != nil {
					t.Fatal(err)
				}
				f.auth.refreshReply = func(http.ResponseWriter) bool { change(); return false }
				f.advance(time.Hour)
				if _, _, err := f.exec("doctor"); err == nil || !strings.Contains(err.Error(), "changed") {
					t.Fatalf("error = %v", err)
				}
			}
			if _, ok := f.store.stored("default"); ok {
				t.Fatal("saved a grant with changed trust")
			}
			if len(f.auth.revokedTokens()) != 2 {
				t.Fatal("did not revoke unsaved token pair")
			}
		})
	}
}

func TestOnPremPersonalKeyImportUsesCustomerCAAndDirectREST(t *testing.T) {
	f := newOnPremFixture(t)
	const key = "personal-key-0123456789abcdef"
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("CONVAI-API-KEY") != key || r.Header.Get("Authorization") != "" {
			t.Error("incorrect key header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"total":0,"limit":1,"offset":0}`))
	}))
	defer server.Close()
	f.configs.edit(t, func(cfg *config.Config) {
		p := cfg.Profiles["default"]
		p.APIURL, p.OAuth = server.URL, config.OAuthTrust{}
		cfg.Profiles["default"] = p
	})
	if _, _, err := f.run(context.Background(), key+"\n", "auth", "login", "--token-stdin"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.exec("doctor"); err != nil {
		t.Fatal(err)
	}
	value, ok := f.store.stored("default")
	if !ok || value.Managed() || value.Type != config.CredentialAPIKey || requests != 2 {
		t.Fatal("key import did not reuse direct REST credential path")
	}
	for _, runtime := range []string{"eyJabc.eyJdef.signature", "cvort_0123456789abcdef0123456789"} {
		if _, _, err := f.run(context.Background(), runtime+"\n", "auth", "login", "--token-stdin"); err == nil {
			t.Fatal("accepted runtime/refresh token")
		}
	}
	if requests != 2 {
		t.Fatal("rejected import sent a request")
	}
	if _, _, err := f.exec("auth", "login"); err == nil {
		t.Fatal("inferred issuer for API-key-only profile")
	}
}

func TestOnPremConcurrentRefreshAndUntrustedCA(t *testing.T) {
	f := newOnPremFixture(t)
	if _, _, err := f.exec("auth", "login"); err != nil {
		t.Fatal(err)
	}
	f.advance(time.Hour)
	var wait sync.WaitGroup
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, _, err := f.exec("doctor"); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if _, refresh := f.auth.counts(); refresh != 1 {
		t.Fatalf("refreshes = %d", refresh)
	}
	if _, _, err := f.exec("auth", "logout"); err != nil {
		t.Fatal(err)
	}
	f.configs.edit(t, func(cfg *config.Config) {
		p := cfg.Profiles["default"]
		p.CABundle = ""
		cfg.Profiles["default"] = p
	})
	if _, _, err := f.exec("auth", "login"); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("untrusted CA error = %v", err)
	}
	if token, _ := f.auth.counts(); token != 2 {
		t.Fatal("untrusted issuer received code or refresh")
	}
}

func TestOAuthRefreshPreservesEquivalentStoredAPIOrigin(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "SaaS"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			var f *oauthFixture
			if custom {
				f = newOnPremFixture(t)
			} else {
				f = newOAuthFixture(t)
			}
			if _, _, err := f.exec("auth", "login"); err != nil {
				t.Fatal(err)
			}
			first, _ := f.store.stored("default")
			f.configs.edit(t, func(cfg *config.Config) {
				p := cfg.Profiles["default"]
				p.APIURL += "/"
				cfg.Profiles["default"] = p
			})
			f.advance(time.Hour)
			if _, _, err := f.exec("doctor"); err != nil {
				t.Fatal(err)
			}
			next, ok := f.store.stored("default")
			if !ok || next.Value == first.Value || next.Session.APIURL != first.Session.APIURL || len(f.auth.revokedTokens()) != 0 {
				t.Fatal("equivalent origin discarded the rotated session")
			}
			if _, refresh := f.auth.counts(); refresh != 1 {
				t.Fatalf("refresh count = %d", refresh)
			}
			if !strings.HasSuffix(f.configs.current(t).Profiles["default"].APIURL, "/") {
				t.Fatal("refresh rewrote profile metadata")
			}
			if _, _, err := f.exec("auth", "logout"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
