package credential

import (
	"errors"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/config"
)

type memoryStore struct {
	value Credential
	err   error
}

func (s *memoryStore) Get(string) (Credential, error) {
	return s.value, s.err
}

func (s *memoryStore) Set(_ string, value Credential) error {
	s.value = value
	s.err = nil
	return nil
}

func (s *memoryStore) Delete(string) error {
	s.err = ErrNotFound
	return nil
}

func TestParseCredentialType(t *testing.T) {
	t.Parallel()
	pat, err := Parse("cvpat_0123456789012345678901234567890123456789")
	if err != nil {
		t.Fatalf("Parse PAT: %v", err)
	}
	if pat.Type != config.CredentialPAT {
		t.Fatalf("PAT type = %q", pat.Type)
	}

	apiKey, err := Parse("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("Parse API key: %v", err)
	}
	if apiKey.Type != config.CredentialAPIKey {
		t.Fatalf("API key type = %q", apiKey.Type)
	}
}

func TestParseRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "short", " token-with-leading-space", "token\nwith-newline"} {
		if _, err := Parse(value); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded", value)
		}
	}
}

func TestResolveEnvironmentTakesPrecedence(t *testing.T) {
	t.Setenv(EnvironmentToken, "cvpat_0123456789012345678901234567890123456789")
	store := &memoryStore{
		value: Credential{
			Type:  config.CredentialAPIKey,
			Value: "01234567890123456789012345678901",
		},
	}

	resolved, source, err := Resolve(store, "default")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if source != "environment" || resolved.Type != config.CredentialPAT {
		t.Fatalf("Resolve = %#v, %q", resolved, source)
	}
}

func TestResolvePropagatesNotFound(t *testing.T) {
	t.Setenv(EnvironmentToken, "")
	store := &memoryStore{err: ErrNotFound}
	_, _, err := Resolve(store, "default")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve error = %v, want ErrNotFound", err)
	}
}
