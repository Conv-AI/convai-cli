package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeAPIURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "https", input: "https://api2.convai.com/", want: "https://api2.convai.com"},
		{name: "loopback", input: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{name: "insecure remote", input: "http://api2.convai.com", wantErr: true},
		{name: "credentials", input: "https://user:secret@api2.convai.com", wantErr: true},
		{name: "path", input: "https://api2.convai.com/character", wantErr: true},
		{name: "query", input: "https://api2.convai.com?token=secret", wantErr: true},
		{name: "hostless", input: "api2.convai.com", wantErr: true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeAPIURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeAPIURL(%q) unexpectedly succeeded", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeAPIURL(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeAPIURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFileStoreRoundTripUsesPrivateFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	store := &FileStore{Path: path}
	cfg := Default()
	cfg.Profiles["staging"] = Profile{
		APIURL:         StagingAPIURL,
		CredentialType: CredentialAPIKey,
	}
	cfg.CurrentProfile = "staging"

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.CurrentProfile != "staging" {
		t.Fatalf("CurrentProfile = %q", loaded.CurrentProfile)
	}
	if loaded.Profiles["staging"].APIURL != StagingAPIURL {
		t.Fatalf("staging profile = %#v", loaded.Profiles["staging"])
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("config permissions = %o, want 600", got)
		}
	}
}

func TestLoadMissingReturnsProductionDefault(t *testing.T) {
	t.Parallel()
	store := &FileStore{Path: filepath.Join(t.TempDir(), "missing.yaml")}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	profile := cfg.Profiles[cfg.CurrentProfile]
	if profile.APIURL != DefaultAPIURL {
		t.Fatalf("default API URL = %q", profile.APIURL)
	}
	if profile.CredentialType != CredentialPAT {
		t.Fatalf("default credential type = %q", profile.CredentialType)
	}
}

func TestValidateRejectsMissingCurrentProfile(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.CurrentProfile = "missing"
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate unexpectedly accepted missing current profile")
	}
}
