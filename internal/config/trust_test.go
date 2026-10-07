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

func TestCustomTrustPortBoundariesAndProfileImport(t *testing.T) {
	for _, port := range []string{"", ":8443", ":1", ":65535"} {
		for _, host := range []string{"api.customer.test", "[2001:db8::1]"} {
			origin := "https://" + host + port
			p := Profile{APIURL: origin, CredentialType: CredentialAPIKey,
				OAuth: OAuthTrust{APIURL: origin, Issuer: origin, Resource: origin + "/authoring"}}
			if err := ValidateTrust(p); err != nil {
				t.Fatalf("valid explicit origin %q rejected: %v", origin, err)
			}
		}
	}
	for _, origin := range []string{
		"https://api.customer.test:0", "https://api.customer.test:65536", "https://api.customer.test:",
		"https://api.customer.test:99999999999999999999999", "https://api.customer.test:abc",
		"https://api.customer.test:-1", "https://api.customer.test:+443",
		"https://[::1]:0", "https://[::1]:65536", "https://[::1]:", "https://::1",
		"https://api.customer.test?", "https://api.customer.test#", "https://api.customer.test/%2f",
	} {
		for _, field := range []string{"api", "issuer", "resource"} {
			t.Run(field+"/"+origin, func(t *testing.T) {
				p := Profile{APIURL: "https://api.customer.test", CredentialType: CredentialAPIKey,
					OAuth: OAuthTrust{APIURL: "https://api.customer.test", Issuer: "https://login.customer.test", Resource: "https://api.customer.test/authoring"}}
				switch field {
				case "api":
					p.APIURL, p.OAuth.APIURL = origin, origin
				case "issuer":
					p.OAuth.Issuer = origin
				case "resource":
					p.OAuth.Resource = origin
				}
				if err := ValidateTrust(p); err == nil {
					t.Fatal("invalid authority accepted in actual trust validation")
				}
				data := "api_url: " + p.APIURL + "\noauth:\n  api_url: " + p.OAuth.APIURL + "\n  issuer: " + p.OAuth.Issuer + "\n  resource: " + p.OAuth.Resource + "\n"
				if _, err := DecodeProfile([]byte(data)); err == nil {
					t.Fatal("invalid authority accepted by profile import")
				}
				if field == "api" {
					if _, err := DecodeProfile([]byte("api_url: " + origin + "\n")); err == nil {
						t.Fatal("API-only import normalized away an invalid authority")
					}
				}
			})
		}
	}
}

func TestZeroPaddedPortsCannotReplaceFixedSaaSMappings(t *testing.T) {
	for _, api := range []string{DefaultAPIURL, StagingAPIURL, PreviewAPIURL} {
		for _, port := range []string{":0443", ":000443"} {
			raw := api + port
			p := Profile{APIURL: raw, CredentialType: CredentialAPIKey,
				OAuth: OAuthTrust{APIURL: raw, Issuer: "https://login.customer.test", Resource: "https://api.customer.test/authoring"}}
			if err := ValidateTrust(p); err == nil {
				t.Errorf("physical SaaS origin %q accepted custom issuer", raw)
			}
			if got, err := NormalizeAPIURL(raw); err != nil || got != api {
				t.Errorf("physical SaaS origin %q = %q, %v; want %q", raw, got, err, api)
			}
		}
	}
}
