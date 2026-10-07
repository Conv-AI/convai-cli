package app

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

// Local recovery must not decode a possibly obsolete or malformed secret.
type localCleanupStore struct {
	values    map[string]string
	deleted   []string
	deleteErr error
	t         *testing.T
}

func (s *localCleanupStore) Get(string) (credential.Credential, error) {
	s.t.Error("local cleanup read a credential")
	return credential.Credential{}, errors.New("unexpected credential read")
}

func (s *localCleanupStore) Set(string, credential.Credential) error {
	s.t.Error("local cleanup wrote a credential")
	return errors.New("unexpected credential write")
}

func (s *localCleanupStore) Delete(name string) error {
	s.deleted = append(s.deleted, name)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.values[name]; !ok {
		return credential.ErrNotFound
	}
	delete(s.values, name)
	return nil
}

func TestExplicitLocalLogoutRecoversWithoutLoadingProfileTrust(t *testing.T) {
	t.Setenv(credential.EnvironmentToken, "synthetic-environment-credential")
	for name, metadata := range map[string]string{
		"malformed":         "profiles: [\n",
		"missing metadata":  "",
		"missing profile":   "current_profile: default\nprofiles:\n  default:\n    api_url: https://api2.convai.com\n    credential_type: api_key\n",
		"zero port":         "current_profile: old\nprofiles:\n  old:\n    api_url: https://api.customer.test:0\n    credential_type: api_key\n",
		"out of range port": "current_profile: old\nprofiles:\n  old:\n    api_url: https://api.customer.test:65536\n    credential_type: api_key\n",
		"protected SaaS":    "current_profile: old\nprofiles:\n  old:\n    api_url: https://api2.convai.com:0443\n    credential_type: oauth_access_token\n    oauth:\n      api_url: https://api2.convai.com:0443\n      issuer: https://auth.customer.test\n      resource: https://api2.convai.com:0443/authoring\n",
	} {
		t.Run(name, func(t *testing.T) {
			store := &config.FileStore{Path: filepath.Join(t.TempDir(), "config.yaml")}
			if metadata != "" {
				if err := os.WriteFile(store.Path, []byte(metadata), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			secrets := &localCleanupStore{t: t, values: map[string]string{"old": "{malformed synthetic record", "other": "untouched synthetic record"}}
			if _, _, err := executeLocalCleanup(t, store, secrets, "auth", "logout", "--profile", "old"); err == nil || len(secrets.deleted) != 0 {
				t.Fatal("remote logout bypassed invalid/missing profile validation", err)
			}
			stdout, stderr, err := executeLocalCleanup(t, store, secrets, "auth", "logout", "--local", "--profile", "old")
			if err != nil || !strings.Contains(stdout, "Removed local credential for profile old") || !strings.Contains(stderr, "was not revoked") {
				t.Fatalf("local cleanup = %q / %q / %v", stdout, stderr, err)
			}
			if len(secrets.deleted) != 1 || secrets.deleted[0] != "old" || len(secrets.values) != 1 || secrets.values["other"] != "untouched synthetic record" {
				t.Fatal("cleanup did not isolate the explicit namespace")
			}
			after, err := os.ReadFile(store.Path)
			if metadata == "" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("cleanup created metadata", err)
				}
			} else if err != nil || string(after) != metadata {
				t.Fatal("cleanup changed metadata", err)
			}
			stdout, _, err = executeLocalCleanup(t, store, secrets, "auth", "logout", "--local", "--profile", "old")
			if err != nil || !strings.Contains(stdout, "No stored credential for profile old") {
				t.Fatalf("missing credential cleanup = %q, %v", stdout, err)
			}
		})
	}
}

func TestLocalLogoutRecoveryRejectsInvalidNamesAndPreservesRemoteValidation(t *testing.T) {
	store := &config.FileStore{Path: filepath.Join(t.TempDir(), "config.yaml")}
	if err := os.WriteFile(store.Path, []byte("profiles: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	approved := filepath.Join(t.TempDir(), "approved.yaml")
	if err := os.WriteFile(approved, []byte("api_url: https://api2.convai.com\ncredential_type: api_key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "../old", "old/name", "old\\name", "old\n", strings.Repeat("a", 65)} {
		secrets := &localCleanupStore{t: t, values: map[string]string{"old": "synthetic record"}}
		if _, _, err := executeLocalCleanup(t, store, secrets, "auth", "logout", "--local", "--profile", name); err == nil || !strings.Contains(err.Error(), "profile name") {
			t.Fatalf("invalid explicit name %q: %v", name, err)
		}
		if len(secrets.deleted) != 0 || len(secrets.values) != 1 {
			t.Fatal("invalid name touched credentials")
		}
	}
	for _, args := range [][]string{{"auth", "logout", "--profile", "old"}, {"auth", "logout", "--local"}, {"config", "set", "api-url", config.DefaultAPIURL, "--profile", "old"}, {"config", "import", "--file", approved, "--profile", "old"}} {
		secrets := &localCleanupStore{t: t, values: map[string]string{"old": "synthetic record"}}
		if _, _, err := executeLocalCleanup(t, store, secrets, args...); err == nil || !strings.Contains(err.Error(), "parse config") {
			t.Fatalf("metadata validation bypassed for %v: %v", args, err)
		}
		if len(secrets.deleted) != 0 {
			t.Fatal("non-recovery command deleted credentials")
		}
	}
	secrets := &localCleanupStore{t: t, values: map[string]string{"old": "synthetic record"}, deleteErr: errors.New("synthetic deletion denied")}
	stdout, _, err := executeLocalCleanup(t, store, secrets, "auth", "logout", "--local", "--profile", "old")
	if err == nil || stdout != "" || len(secrets.values) != 1 {
		t.Fatalf("failed deletion claimed success: %q, %v", stdout, err)
	}
}

func executeLocalCleanup(t *testing.T, store config.Store, secrets credential.Store, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root, err := New(Options{
		ConfigStore: store, CredentialStore: secrets, Locker: testLocker(t),
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Error("local recovery sent HTTP")
			return nil, errors.New("unexpected HTTP")
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	root.SetArgs(args)
	err = root.Execute()
	return stdout.String(), stderr.String(), err
}
