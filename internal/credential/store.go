package credential

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/zalando/go-keyring"
)

const (
	ServiceName      = "io.convai.cli"
	EnvironmentToken = "CONVAI_TOKEN"
)

var ErrNotFound = errors.New("credential not found")

type Credential struct {
	Type  config.CredentialType
	Value string
}

type Store interface {
	Get(profile string) (Credential, error)
	Set(profile string, credential Credential) error
	Delete(profile string) error
}

type KeyringStore struct{}

func (KeyringStore) Get(profile string) (Credential, error) {
	value, err := keyring.Get(ServiceName, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("read operating-system credential store: %w", err)
	}
	return Parse(value)
}

func (KeyringStore) Set(profile string, credential Credential) error {
	if _, err := Parse(credential.Value); err != nil {
		return err
	}
	if err := keyring.Set(ServiceName, profile, credential.Value); err != nil {
		return fmt.Errorf("write operating-system credential store: %w", err)
	}
	return nil
}

func (KeyringStore) Delete(profile string) error {
	err := keyring.Delete(ServiceName, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete operating-system credential: %w", err)
	}
	return nil
}

func Resolve(store Store, profile string) (Credential, string, error) {
	if value := os.Getenv(EnvironmentToken); value != "" {
		credential, err := Parse(value)
		if err != nil {
			return Credential{}, "", fmt.Errorf("%s: %w", EnvironmentToken, err)
		}
		return credential, "environment", nil
	}
	credential, err := store.Get(profile)
	if err != nil {
		return Credential{}, "", err
	}
	return credential, "keyring", nil
}

func Parse(value string) (Credential, error) {
	if value == "" {
		return Credential{}, errors.New("credential is empty")
	}
	if value != strings.TrimSpace(value) {
		return Credential{}, errors.New("credential must not contain surrounding whitespace")
	}
	if len(value) < 20 || len(value) > 512 {
		return Credential{}, errors.New("credential length is invalid")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return Credential{}, errors.New("credential must not contain whitespace or control characters")
		}
	}
	kind := config.CredentialAPIKey
	if strings.HasPrefix(value, "cvpat_") {
		kind = config.CredentialPAT
	} else if strings.HasPrefix(value, "cvoat_") {
		kind = config.CredentialOAuth
	} else if strings.HasPrefix(value, "cvort_") || strings.HasPrefix(value, "cvoac_") || strings.Count(value, ".") == 2 {
		return Credential{}, errors.New("use an API key, PAT, or OAuth access token, not a refresh, authorization-code, or runtime token")
	}
	return Credential{Type: kind, Value: value}, nil
}
