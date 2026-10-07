package app

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"gopkg.in/yaml.v3"
)

func TestProfileOriginValidationNeverPersistsInvalidTrustOrTouchesCredentials(t *testing.T) {
	store := &config.FileStore{Path: filepath.Join(t.TempDir(), "config.yaml")}
	if err := store.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	old := credential.Credential{Type: config.CredentialAPIKey, Value: "synthetic-protocol-key"}
	credentials := &memoryCredentialStore{values: map[string]credential.Credential{"default": old}}
	run := func(args ...string) error {
		root, err := New(Options{
			Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
			ConfigStore: store, CredentialStore: credentials, Locker: testLocker(t),
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("profile metadata validation sent a network request")
				return nil, errors.New("unexpected request")
			})},
		})
		if err != nil {
			t.Fatal(err)
		}
		root.SetArgs(args)
		return root.Execute()
	}
	requireUnchanged := func() {
		after, err := os.ReadFile(store.Path)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatal("invalid profile modified persisted metadata", err)
		}
		if len(credentials.values) != 1 || credentials.values["default"] != old {
			t.Fatal("metadata command changed credentials")
		}
	}
	importProfile := func(p config.Profile) error {
		data, err := yaml.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "profile.yaml")
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return run("config", "import", "--file", file, "--profile", "default")
	}
	for _, origin := range []string{
		"https://api.customer.test:0", "https://api.customer.test:65536", "https://api.customer.test:",
		"https://api.customer.test:abc", "https://api.customer.test:99999999999999999999999",
		"https://[::1]:", "https://[::1]:0", "https://::1", "https://[api.customer.test]",
		"https://api.customer.test?", "https://api.customer.test#", "https://api.customer.test#value",
		"https://api.customer.test/%2f", "https://user:synthetic@api.customer.test",
	} {
		t.Run(origin, func(t *testing.T) {
			if err := run("config", "set", "api-url", origin); err == nil {
				t.Fatal("config set accepted invalid API authority")
			}
			requireUnchanged()
			if err := importProfile(config.Profile{APIURL: origin, CredentialType: config.CredentialAPIKey}); err == nil {
				t.Fatal("config import accepted invalid API authority")
			}
			requireUnchanged()
		})
	}
	for _, field := range []string{"issuer", "resource"} {
		for _, origin := range []string{"https://auth.customer.test:0", "https://auth.customer.test:65536", "https://auth.customer.test:", "https://auth.customer.test#", "https://auth.customer.test?"} {
			t.Run(field+"/"+origin, func(t *testing.T) {
				p := config.Profile{APIURL: "https://api.customer.test", CredentialType: config.CredentialAPIKey,
					OAuth: config.OAuthTrust{APIURL: "https://api.customer.test", Issuer: "https://auth.customer.test", Resource: "https://api.customer.test/authoring"}}
				if field == "issuer" {
					p.OAuth.Issuer = origin
				} else {
					p.OAuth.Resource = origin
				}
				if err := importProfile(p); err == nil {
					t.Fatal("config import accepted invalid custom trust")
				}
				requireUnchanged()
			})
		}
	}
	for _, origin := range []string{config.DefaultAPIURL, config.StagingAPIURL, config.PreviewAPIURL} {
		p := config.Profile{APIURL: origin + ":0443", CredentialType: config.CredentialAPIKey,
			OAuth: config.OAuthTrust{APIURL: origin + ":0443", Issuer: "https://auth.customer.test", Resource: "https://api.customer.test/authoring"}}
		if err := importProfile(p); err == nil {
			t.Fatal("config import replaced a protected SaaS issuer")
		}
		requireUnchanged()
	}
	for raw, want := range map[string]string{
		"HTTPS://API2.CONVAI.COM:0443/":   "https://api2.convai.com",
		"http://127.0.0.1:0080":           "http://127.0.0.1",
		"http://[::1]:0080/":              "http://[::1]",
		"https://api.customer.test:08443": "https://api.customer.test:8443",
		"https://[2001:db8::1]:08443":     "https://[2001:db8::1]:8443",
	} {
		if err := run("config", "set", "api-url", raw); err != nil {
			t.Fatal("healthy canonicalization rejected", err)
		}
		cfg, err := store.Load()
		if err != nil || cfg.Profiles["default"].APIURL != want {
			t.Fatalf("persisted origin %q = %q, %v; want %q", raw, cfg.Profiles["default"].APIURL, err, want)
		}
		if len(credentials.values) != 1 || credentials.values["default"] != old {
			t.Fatal("healthy metadata canonicalization rebound a credential")
		}
	}
}
