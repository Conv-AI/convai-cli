package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

type memoryConfigStore struct {
	cfg     config.Config
	saveErr error
}

func (s *memoryConfigStore) Load() (config.Config, error) {
	return cloneConfig(s.cfg), nil
}

func (s *memoryConfigStore) Save(cfg config.Config) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.cfg = cloneConfig(cfg)
	return nil
}

type memoryCredentialStore struct {
	values map[string]credential.Credential
}

func (s *memoryCredentialStore) Get(profile string) (credential.Credential, error) {
	value, ok := s.values[profile]
	if !ok {
		return credential.Credential{}, credential.ErrNotFound
	}
	return value, nil
}

func (s *memoryCredentialStore) Set(profile string, value credential.Credential) error {
	s.values[profile] = value
	return nil
}

func (s *memoryCredentialStore) Delete(profile string) error {
	if _, ok := s.values[profile]; !ok {
		return credential.ErrNotFound
	}
	delete(s.values, profile)
	return nil
}

func TestCharacterListJSONUsesRESTDirectly(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/character/list" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("CONVAI-API-KEY") == "" {
			t.Error("missing API key header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"total":0,"limit":50,"offset":0}`))
	}))
	defer server.Close()

	stdout, _, err := execute(t, server, strings.NewReader(""), "character", "list", "--output", "json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stdout, `"total": 0`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestCharacterCreateRequiresBackstoryBeforeNetwork(t *testing.T) {
	t.Parallel()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	_, _, err := execute(t, server, strings.NewReader(""), "character", "create", "--name", "Ari", "--voice", "voice-1")
	if err == nil || !strings.Contains(err.Error(), "backstory is required") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("server was called for invalid create input")
	}
}

func TestCharacterCreateFromStdin(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"backstory":"Backstory"`)) {
			t.Errorf("body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"charID":"character-1","character_id":"character-1"}`))
	}))
	defer server.Close()

	input := strings.NewReader(`{"charName":"Ari","voiceType":"voice-1","backstory":"Backstory"}`)
	stdout, _, err := execute(t, server, input, "character", "create", "--file", "-", "--output", "json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stdout, `"character_id": "character-1"`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestCharacterCreateFileValidatesRequiredFieldsBeforeNetwork(t *testing.T) {
	t.Parallel()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	input := strings.NewReader(`{"charName":"Ari","voiceType":"voice-1"}`)
	_, _, err := execute(t, server, input, "character", "create", "--file", "-")
	if err == nil || !strings.Contains(err.Error(), "backstory is required") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("server was called for invalid file input")
	}
}

func TestCharacterCreateValidatesOutputBeforeNetwork(t *testing.T) {
	t.Parallel()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	_, _, err := execute(
		t,
		server,
		strings.NewReader(""),
		"character",
		"create",
		"--name",
		"Ari",
		"--voice",
		"voice-1",
		"--backstory",
		"Backstory",
		"--output",
		"yml",
	)
	if err == nil || !strings.Contains(err.Error(), "output must be table, json, or yaml") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("server was called for invalid output")
	}
}

func TestCharacterListValidatesPaginationBeforeNetwork(t *testing.T) {
	t.Parallel()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	_, _, err := execute(t, server, strings.NewReader(""), "character", "list", "--limit", "501")
	if err == nil || !strings.Contains(err.Error(), "--limit must be between 1 and 500") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("server was called for invalid pagination")
	}
}

func TestUnknownProfileFailsBeforeNetwork(t *testing.T) {
	t.Parallel()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	cfgStore := &memoryConfigStore{cfg: config.Default()}
	credentialStore := &memoryCredentialStore{
		values: map[string]credential.Credential{
			"stagin": {
				Type:  config.CredentialAPIKey,
				Value: "01234567890123456789012345678901",
			},
		},
	}
	root, err := New(Options{
		Stdin:           strings.NewReader(""),
		Stdout:          io.Discard,
		Stderr:          io.Discard,
		ConfigStore:     cfgStore,
		CredentialStore: credentialStore,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root.SetArgs([]string{"character", "list", "--profile", "stagin"})
	err = root.Execute()
	if err == nil || !strings.Contains(err.Error(), `profile "stagin" does not exist`) {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("server was called for unknown profile")
	}
}

func TestAuthLoginStoresCredentialWithoutPrintingIt(t *testing.T) {
	t.Parallel()
	cfgStore := &memoryConfigStore{cfg: config.Default()}
	credentialStore := &memoryCredentialStore{values: map[string]credential.Credential{}}
	var stdout, stderr bytes.Buffer
	root, err := New(Options{
		Version:         "test",
		Stdin:           strings.NewReader("01234567890123456789012345678901\n"),
		Stdout:          &stdout,
		Stderr:          &stderr,
		ConfigStore:     cfgStore,
		CredentialStore: credentialStore,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root.SetArgs([]string{"auth", "login", "--token-stdin", "--no-verify"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(stdout.String(), "01234567890123456789012345678901") {
		t.Fatal("stdout exposed credential")
	}
	stored, err := credentialStore.Get("default")
	if err != nil || stored.Type != config.CredentialAPIKey {
		t.Fatalf("stored = %#v, err = %v", stored, err)
	}
}

func TestAuthLoginRestoresPreviousCredentialWhenConfigSaveFails(t *testing.T) {
	t.Parallel()
	old := credential.Credential{
		Type:  config.CredentialAPIKey,
		Value: "old-credential-0123456789012345",
	}
	cfgStore := &memoryConfigStore{cfg: config.Default(), saveErr: errors.New("disk full")}
	credentialStore := &memoryCredentialStore{
		values: map[string]credential.Credential{"default": old},
	}
	root, err := New(Options{
		Stdin:           strings.NewReader("new-credential-0123456789012345\n"),
		Stdout:          io.Discard,
		Stderr:          io.Discard,
		ConfigStore:     cfgStore,
		CredentialStore: credentialStore,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root.SetArgs([]string{"auth", "login", "--token-stdin", "--no-verify"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Execute error = %v", err)
	}
	stored, _ := credentialStore.Get("default")
	if stored != old {
		t.Fatalf("credential = %#v, want %#v", stored, old)
	}
}

func TestAuthLoginWorkspaceOverrideDoesNotChangeSavedSelection(t *testing.T) {
	t.Parallel()
	const savedWorkspace = "11111111-1111-4111-8111-111111111111"
	const overrideWorkspace = "22222222-2222-4222-8222-222222222222"
	for _, selection := range []string{"personal", overrideWorkspace} {
		for _, verify := range []bool{true, false} {
			t.Run(selection+"/verify="+fmt.Sprint(verify), func(t *testing.T) {
				called := false
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					want := selection
					if selection == "personal" {
						want = ""
					}
					if r.URL.Query().Get("workspace_id") != want {
						t.Errorf("verification workspace = %q, want %q", r.URL.Query().Get("workspace_id"), want)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"items":[],"total":0,"limit":1,"offset":0}`))
				}))
				defer server.Close()
				cfg := config.Default()
				profile := cfg.Profiles["default"]
				profile.WorkspaceID = savedWorkspace
				cfg.Profiles["default"] = profile
				store := &memoryConfigStore{cfg: cfg}
				root, err := New(Options{
					Stdin:  strings.NewReader("01234567890123456789012345678901\n"),
					Stdout: io.Discard, Stderr: io.Discard, ConfigStore: store,
					CredentialStore: &memoryCredentialStore{values: map[string]credential.Credential{}},
					HTTPClient:      server.Client(),
				})
				if err != nil {
					t.Fatal(err)
				}
				args := []string{"auth", "login", "--token-stdin", "--workspace", selection, "--api-url", server.URL}
				if !verify {
					args = append(args, "--no-verify")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				stored := store.cfg.Profiles["default"]
				if stored.WorkspaceID != savedWorkspace || stored.APIURL != server.URL || stored.CredentialType != config.CredentialAPIKey {
					t.Fatalf("saved profile = %+v", stored)
				}
				if called != verify {
					t.Fatalf("verification called = %v, want %v", called, verify)
				}
			})
		}
	}
}

func TestRedactErrorRemovesPAT(t *testing.T) {
	t.Parallel()
	message := RedactError(errors.New("failed cvpat_abcDEF0123_more at upstream"))
	if strings.Contains(message, "cvpat_") || !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("message = %q", message)
	}
}

func execute(t *testing.T, server *httptest.Server, stdin io.Reader, args ...string) (string, string, error) {
	t.Helper()
	cfg := config.Default()
	profile := cfg.Profiles[cfg.CurrentProfile]
	profile.APIURL = server.URL
	profile.CredentialType = config.CredentialAPIKey
	cfg.Profiles[cfg.CurrentProfile] = profile
	cfgStore := &memoryConfigStore{cfg: cfg}
	credentialStore := &memoryCredentialStore{
		values: map[string]credential.Credential{
			cfg.CurrentProfile: {
				Type:  config.CredentialAPIKey,
				Value: "01234567890123456789012345678901",
			},
		},
	}
	var stdout, stderr bytes.Buffer
	root, err := New(Options{
		Version:         "test",
		Stdin:           stdin,
		Stdout:          &stdout,
		Stderr:          &stderr,
		ConfigStore:     cfgStore,
		CredentialStore: credentialStore,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root.SetArgs(args)
	err = root.Execute()
	return stdout.String(), stderr.String(), err
}

func cloneConfig(cfg config.Config) config.Config {
	profiles := make(map[string]config.Profile, len(cfg.Profiles))
	for name, profile := range cfg.Profiles {
		profiles[name] = profile
	}
	cfg.Profiles = profiles
	return cfg
}
