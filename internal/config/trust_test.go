package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitCustomTrustValidation(t *testing.T) {
	valid := Profile{APIURL: "https://api.customer.test:8443", CredentialType: CredentialAPIKey,
		OAuth:    OAuthTrust{APIURL: "https://api.customer.test:8443", Issuer: "https://login.customer.test", Resource: "https://api.customer.test/authoring"},
		CABundle: filepath.Join(t.TempDir(), "ca.pem")}
	if err := ValidateTrust(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name string
		edit func(*Profile)
	}{
		{"wrong host", func(p *Profile) { p.APIURL = "https://other.customer.test" }},
		{"missing issuer", func(p *Profile) { p.OAuth.Issuer = "" }},
		{"missing resource", func(p *Profile) { p.OAuth.Resource = "" }},
		{"issuer HTTP", func(p *Profile) { p.OAuth.Issuer = "http://127.0.0.1" }},
		{"issuer path", func(p *Profile) { p.OAuth.Issuer += "/auth" }},
		{"issuer query", func(p *Profile) { p.OAuth.Issuer += "?" }},
		{"issuer credentials", func(p *Profile) { p.OAuth.Issuer = "https://user:password@login.customer.test" }},
		{"resource HTTP", func(p *Profile) { p.OAuth.Resource = "http://127.0.0.1/authoring" }},
		{"resource credentials", func(p *Profile) { p.OAuth.Resource = "https://user:password@api.customer.test" }},
		{"resource query", func(p *Profile) { p.OAuth.Resource += "?" }},
		{"resource encoded path", func(p *Profile) { p.OAuth.Resource += "/%2e%2e" }},
		{"resource fragment", func(p *Profile) { p.OAuth.Resource += "#fragment" }},
		{"relative CA", func(p *Profile) { p.CABundle = "ca.pem" }},
		{"SaaS substitution", func(p *Profile) { p.APIURL = DefaultAPIURL; p.OAuth.APIURL = DefaultAPIURL }},
		{"HTTP with CA", func(p *Profile) { p.APIURL = "http://127.0.0.1"; p.OAuth = OAuthTrust{} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			p := valid
			change.edit(&p)
			if err := ValidateTrust(p); err == nil {
				t.Fatal("accepted invalid trust")
			}
		})
	}
}

func TestProfileImportIsStrictAndNonSecret(t *testing.T) {
	valid := "api_url: https://api.customer.test\noauth:\n  api_url: https://api.customer.test\n  issuer: https://login.customer.test\n  resource: https://api.customer.test/authoring\n"
	p, err := DecodeProfile([]byte(valid))
	if err != nil || p.CredentialType != CredentialAPIKey {
		t.Fatalf("profile = %+v, %v", p, err)
	}
	for _, data := range []string{
		"null", "[]", valid + "---\n" + valid, valid + "api_url: https://other.test\n",
		valid + "token: super-secret-value\n", valid + "insecure_skip_verify: true\n",
		strings.Replace(valid, "issuer:", "inferred_issuer:", 1),
	} {
		if _, err := DecodeProfile([]byte(data)); err == nil || strings.Contains(err.Error(), "super-secret-value") {
			t.Fatalf("accepted malformed/secret profile or echoed secret: %v", err)
		}
	}
	store := &FileStore{Path: filepath.Join(t.TempDir(), "config.yaml")}
	cfg := Default()
	cfg.Profiles["customer"] = p
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Profiles["customer"] != p {
		t.Fatalf("round trip = %+v, %v", loaded, err)
	}
}
