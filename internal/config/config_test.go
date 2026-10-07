package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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
		{name: "host case", input: "HTTPS://API2.Convai.COM", want: "https://api2.convai.com"},
		{name: "default https port", input: "https://api2.convai.com:443/", want: "https://api2.convai.com"},
		{name: "explicit port kept", input: "https://api2.convai.com:8443", want: "https://api2.convai.com:8443"},
		{name: "default http port", input: "http://127.0.0.1:80", want: "http://127.0.0.1"},
		{name: "zero-padded https default", input: "https://api2.convai.com:0443", want: "https://api2.convai.com"},
		{name: "zero-padded http default", input: "http://127.0.0.1:0080", want: "http://127.0.0.1"},
		{name: "zero-padded custom port", input: "https://api.customer.test:08443", want: "https://api.customer.test:8443"},
		{name: "zero-padded ipv6 port", input: "https://[2001:db8::1]:08443", want: "https://[2001:db8::1]:8443"},
		{name: "ipv6 loopback", input: "http://[::1]:80/", want: "http://[::1]"},
		{name: "ipv6 loopback port", input: "http://[::1]:8080", want: "http://[::1]:8080"},
		{name: "insecure remote upper", input: "HTTP://API2.CONVAI.COM", wantErr: true},
		{name: "insecure remote", input: "http://api2.convai.com", wantErr: true},
		{name: "credentials", input: "https://user:secret@api2.convai.com", wantErr: true},
		{name: "path", input: "https://api2.convai.com/character", wantErr: true},
		{name: "query", input: "https://api2.convai.com?token=secret", wantErr: true},
		{name: "hostless", input: "api2.convai.com", wantErr: true},
		{name: "empty port", input: "https://api.customer.test:", wantErr: true},
		{name: "zero port", input: "https://api.customer.test:0", wantErr: true},
		{name: "large port", input: "https://api.customer.test:65536", wantErr: true},
		{name: "overflow port", input: "https://api.customer.test:99999999999999999999999", wantErr: true},
		{name: "nonnumeric port", input: "https://api.customer.test:abc", wantErr: true},
		{name: "negative port", input: "https://api.customer.test:-1", wantErr: true},
		{name: "signed port", input: "https://api.customer.test:+443", wantErr: true},
		{name: "minimum port", input: "https://api.customer.test:1", want: "https://api.customer.test:1"},
		{name: "maximum port", input: "https://api.customer.test:65535", want: "https://api.customer.test:65535"},
		{name: "ipv6 remote", input: "https://[2001:db8::1]:8443", want: "https://[2001:db8::1]:8443"},
		{name: "ipv6 empty port", input: "https://[::1]:", wantErr: true},
		{name: "ipv6 zero port", input: "https://[::1]:0", wantErr: true},
		{name: "unbracketed ipv6", input: "https://::1", wantErr: true},
		{name: "non-ip brackets", input: "https://[api.customer.test]", wantErr: true},
		{name: "ipv4 brackets", input: "https://[127.0.0.1]", wantErr: true},
		{name: "empty query", input: "https://api.customer.test?", wantErr: true},
		{name: "empty fragment", input: "https://api.customer.test#", wantErr: true},
		{name: "fragment", input: "https://api.customer.test#value", wantErr: true},
		{name: "encoded root", input: "https://api.customer.test/%2f", wantErr: true},
		{name: "host whitespace", input: "https://api .customer.test", wantErr: true},
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

func TestFileStoreUsesOneCanonicalFileForLoadSaveAndLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs extra privileges on Windows")
	}
	t.Parallel()
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(realDir, "config.yaml")
	direct := &FileStore{Path: realFile}
	if err := direct.Save(Default()); err != nil {
		t.Fatal(err)
	}
	fileLink := filepath.Join(root, "link.yaml")
	if err := os.Symlink(realFile, fileLink); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(root, "dirlink")
	if err := os.Symlink(realDir, dirLink); err != nil {
		t.Fatal(err)
	}
	viaFile := &FileStore{Path: fileLink}
	viaDir := &FileStore{Path: filepath.Join(dirLink, "config.yaml")}

	cfg := Default()
	cfg.Profiles["staging"] = Profile{APIURL: StagingAPIURL, CredentialType: CredentialPAT}
	if err := viaFile.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(fileLink); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("Save replaced the configuration symlink")
	}
	for _, store := range []*FileStore{direct, viaDir} {
		loaded, err := store.Load()
		if err != nil || loaded.Profiles["staging"].APIURL != StagingAPIURL {
			t.Fatalf("equivalent path did not see the write: %+v, %v", loaded, err)
		}
	}

	unlock, err := viaFile.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*FileStore{direct, viaDir} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		if _, err := store.Lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("equivalent path acquired a held lock: %v", err)
		}
		cancel()
	}
	unlock()
	again, err := viaDir.Lock(context.Background())
	if err != nil {
		t.Fatalf("released lock was not available: %v", err)
	}
	again()

	dangling := filepath.Join(root, "dangling.yaml")
	if err := os.Symlink(filepath.Join(root, "missing", "config.yaml"), dangling); err != nil {
		t.Fatal(err)
	}
	store := &FileStore{Path: dangling}
	if err := store.Save(Default()); err == nil {
		t.Fatal("Save replaced a dangling configuration symlink")
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load ignored a dangling configuration symlink")
	}
	if _, err := store.Lock(context.Background()); err == nil {
		t.Fatal("Lock accepted a dangling configuration symlink")
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
