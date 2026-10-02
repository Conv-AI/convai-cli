package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const (
	DefaultProfileName = "default"
	DefaultAPIURL      = "https://api2.convai.com"
	StagingAPIURL      = "https://api2-stg.convai.com"
)

type CredentialType string

const (
	CredentialAPIKey CredentialType = "api_key"
	CredentialPAT    CredentialType = "personal_access_token"
	CredentialOAuth  CredentialType = "oauth_access_token"
)

type Profile struct {
	APIURL         string         `yaml:"api_url" json:"api_url"`
	CredentialType CredentialType `yaml:"credential_type" json:"credential_type"`
	WorkspaceID    string         `yaml:"workspace_id,omitempty" json:"workspace_id,omitempty"`
}

type Config struct {
	CurrentProfile string             `yaml:"current_profile" json:"current_profile"`
	Profiles       map[string]Profile `yaml:"profiles" json:"profiles"`
}

type Store interface {
	Load() (Config, error)
	Save(Config) error
}

type FileStore struct {
	Path string
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "convai", "config.yaml"), nil
}

func NewFileStore() (*FileStore, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return &FileStore{Path: path}, nil
}

func Default() Config {
	return Config{
		CurrentProfile: DefaultProfileName,
		Profiles: map[string]Profile{
			DefaultProfileName: {
				APIURL:         DefaultAPIURL,
				CredentialType: CredentialPAT,
			},
		},
	}
}

func (s *FileStore) Load() (Config, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (s *FileStore) Save(cfg Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tempName, s.Path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func Validate(cfg Config) error {
	if err := ValidateProfileName(cfg.CurrentProfile); err != nil {
		return fmt.Errorf("invalid current profile: %w", err)
	}
	if len(cfg.Profiles) == 0 {
		return errors.New("config must contain at least one profile")
	}
	if _, ok := cfg.Profiles[cfg.CurrentProfile]; !ok {
		return fmt.Errorf("current profile %q does not exist", cfg.CurrentProfile)
	}
	for name, profile := range cfg.Profiles {
		if err := ValidateProfileName(name); err != nil {
			return fmt.Errorf("invalid profile %q: %w", name, err)
		}
		if _, err := NormalizeAPIURL(profile.APIURL); err != nil {
			return fmt.Errorf("invalid profile %q API URL: %w", name, err)
		}
		if profile.CredentialType != CredentialAPIKey && profile.CredentialType != CredentialPAT && profile.CredentialType != CredentialOAuth {
			return fmt.Errorf("invalid profile %q credential type", name)
		}
		if err := ValidateWorkspaceID(profile.WorkspaceID); err != nil {
			return err
		}
	}
	return nil
}

func ValidateWorkspaceID(value string) error {
	if value == "" {
		return nil
	}
	if len(value) != 36 {
		return errors.New("workspace must be a UUID")
	}
	if _, err := uuid.Parse(value); err != nil {
		return errors.New("workspace must be a UUID")
	}
	return nil
}

func ValidateProfileName(name string) error {
	if name == "" || len(name) > 64 {
		return errors.New("profile name must contain 1 to 64 characters")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return errors.New("profile name may contain only letters, numbers, dots, hyphens, and underscores")
		}
	}
	return nil
}

func NormalizeAPIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("API URL is required")
	}
	parsed, err := parseURL(raw)
	if err != nil {
		return "", err
	}
	if parsed.User != nil {
		return "", errors.New("API URL must not contain credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("API URL must not contain a query string or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback(parsed.Hostname())) {
		return "", errors.New("API URL must use HTTPS unless it targets loopback")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("API URL must not contain a path")
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}
