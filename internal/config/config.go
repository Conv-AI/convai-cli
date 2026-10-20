package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const (
	DefaultProfileName = "default"
	DefaultAPIURL      = "https://api2.convai.com"
	StagingAPIURL      = "https://api2-stg.convai.com"
	PreviewAPIURL      = "https://api2-preview.convai.com"
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
	OAuth          OAuthTrust     `yaml:"oauth,omitempty" json:"oauth,omitempty"`
	CABundle       string         `yaml:"ca_bundle,omitempty" json:"ca_bundle,omitempty"`
}

// OAuthTrust is an operator-approved mapping, never inferred from discovery.
// APIURL is repeated deliberately: changing the profile origin alone must not
// transfer an issuer's authority to a different resource server.
type OAuthTrust struct {
	APIURL   string `yaml:"api_url" json:"api_url"`
	Issuer   string `yaml:"issuer" json:"issuer"`
	Resource string `yaml:"resource" json:"resource"`
}

type Config struct {
	CurrentProfile string             `yaml:"current_profile" json:"current_profile"`
	Profiles       map[string]Profile `yaml:"profiles" json:"profiles"`
}

type Store interface {
	Load() (Config, error)
	Save(Config) error
	// Lock serializes reads and read-modify-write across processes. Callers
	// must hold it for Load and Save, including read-only snapshots, and
	// release it before user interaction, credential locks, or network calls.
	Lock(ctx context.Context) (unlock func(), err error)
}

type FileStore struct {
	Path string
}

// resolve returns the canonical configuration file: absolute, with symlinks in
// the directory and in the file itself resolved. Load, Save, and Lock all use
// it, so equivalent paths read, write, and lock the same file, and Save
// replaces the symlink target rather than the symlink. A file symlink whose
// target is missing is rejected instead of being replaced by a regular file.
// With create, a missing directory is created first.
func (s *FileStore) resolve(create bool) (string, error) {
	path, err := filepath.Abs(s.Path)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", fmt.Errorf("create config directory: %w", err)
		}
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			// Not wrapped: a dangling link must not read as a missing config.
			return "", fmt.Errorf("resolve config symlink: %v", err)
		}
		return target, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("resolve config directory: %w", err)
	}
	return filepath.Join(dir, filepath.Base(path)), nil
}

// Lock takes an advisory OS file lock beside the canonical configuration
// file, so processes that reach one configuration through different paths
// share it. It is separate from the per-profile credential lock and is always
// acquired after it, never before.
func (s *FileStore) Lock(ctx context.Context) (func(), error) {
	target, err := s.resolve(true)
	if err != nil {
		return nil, err
	}
	lock := flock.New(target+".lock", flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, 20*time.Millisecond)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("wait for another convai process updating the configuration: %w", ctx.Err())
		}
		return nil, fmt.Errorf("lock configuration: %w", err)
	}
	if !locked {
		return nil, errors.New("lock configuration: lock unavailable")
	}
	return func() { _ = lock.Unlock() }, nil
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
	path, err := s.resolve(false)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, errors.New("parse config: only supported non-secret fields are accepted")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return Config{}, errors.New("parse config: expected exactly one YAML document")
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
	path, err := s.resolve(true)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
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
	if err := os.Rename(tempName, path); err != nil {
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
		if err := ValidateTrust(profile); err != nil {
			return fmt.Errorf("invalid profile %q trust: %w", name, err)
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
	port, err := validateAuthority(parsed)
	if err != nil {
		return "", err
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("API URL must not contain a query string or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback(parsed.Hostname())) {
		return "", errors.New("API URL must use HTTPS unless it targets loopback")
	}
	if parsed.RawPath != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("API URL must not contain a path")
	}
	// Canonical origin: lower-case host and no scheme-default port, so trust
	// lookup and credential binding can compare equivalent URLs exactly.
	host := strings.ToLower(parsed.Hostname())
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return parsed.Scheme + "://" + host, nil
}
