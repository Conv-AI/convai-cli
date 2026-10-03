package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/zalando/go-keyring"
)

const (
	sessionAccess  = "cvoat_0123456789abcdef0123456789abcdef_YWNjZXNz"
	sessionRefresh = "cvort_0123456789abcdef0123456789abcdef_cmVmcmVzaA"
)

func testSession(t *testing.T) Credential {
	t.Helper()
	value, err := NewSession(sessionAccess, Session{
		RefreshToken: sessionRefresh,
		ExpiresAt:    time.Date(2026, 10, 3, 13, 0, 0, 0, time.UTC),
		Scopes:       []string{"character:read", "character:write"},
		Issuer:       "https://login-stg.convai.com",
		ClientID:     "convai-cli",
		Audience:     "convai-api",
		APIURL:       "https://api2-stg.convai.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// The keyring mock is process-global, so these tests are not parallel.
func TestKeyringStoresVersionedSessionAndLegacyRawCredentials(t *testing.T) {
	keyring.MockInit()
	store := KeyringStore{}
	session := testSession(t)
	if err := store.Set("browser", session); err != nil {
		t.Fatal(err)
	}
	raw, err := keyring.Get(ServiceName, "browser")
	if err != nil || !strings.Contains(raw, `"convai_credential_version":1`) || !strings.Contains(raw, `"kind":"oauth_session"`) {
		t.Fatalf("stored record = %v", err)
	}
	loaded, err := store.Get("browser")
	if err != nil || !loaded.Managed() || loaded.Value != sessionAccess || loaded.Type != config.CredentialOAuth ||
		loaded.Session.RefreshToken != sessionRefresh || !loaded.Session.ExpiresAt.Equal(session.Session.ExpiresAt) ||
		strings.Join(loaded.Session.Scopes, " ") != "character:read character:write" || loaded.Session.APIURL != "https://api2-stg.convai.com" {
		t.Fatalf("loaded = %+v, %v", loaded, err)
	}

	// Entries written by earlier CLI versions are raw token strings.
	for profile, value := range map[string]string{
		"api-key": "01234567890123456789012345678901",
		"pat":     "cvpat_0123456789012345678901234567890123456789",
		"oauth":   sessionAccess,
	} {
		if err := keyring.Set(ServiceName, profile, value); err != nil {
			t.Fatal(err)
		}
		legacy, err := store.Get(profile)
		if err != nil || legacy.Managed() || legacy.Value != value {
			t.Fatalf("legacy %s = %+v, %v", profile, legacy, err)
		}
	}
	for name, raw := range map[string]string{
		"future version": strings.Replace(mustEncode(t, session), `"convai_credential_version":1`, `"convai_credential_version":2`, 1),
		"unknown field":  strings.Replace(mustEncode(t, session), `{`, `{"extra":true,`, 1),
		"trailing":       mustEncode(t, session) + `{}`,
		"trailing close": mustEncode(t, session) + `}`,
		"trailing array": mustEncode(t, session) + `]`,
		"duplicate":      strings.Replace(mustEncode(t, session), `{`, `{"access_token":"`+sessionAccess+`",`, 1),
		"refresh access": strings.Replace(mustEncode(t, session), `"access_token":"cvoat_`, `"access_token":"cvort_`, 1),
		"no refresh":     strings.Replace(mustEncode(t, session), sessionRefresh, "", 1),
		"no client":      strings.Replace(mustEncode(t, session), `"client_id":"convai-cli"`, `"client_id":""`, 1),
		"bad api url":    strings.Replace(mustEncode(t, session), `"api_url":"https://api2-stg.convai.com"`, `"api_url":"https://api2-stg.convai.com/"`, 1),
		"bad expiry":     strings.Replace(mustEncode(t, session), `"expires_at":"2026-10-03T13:00:00Z"`, `"expires_at":"soon"`, 1),
		"refresh raw":    sessionRefresh,
	} {
		if err := keyring.Set(ServiceName, name, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(name); err == nil {
			t.Fatalf("%s record was accepted", name)
		} else if strings.Contains(err.Error(), sessionRefresh) || strings.Contains(err.Error(), sessionAccess) {
			t.Fatalf("%s error exposed a credential", name)
		}
	}
	invalid := session
	invalidSession := *session.Session
	invalidSession.Audience = ""
	invalid.Session = &invalidSession
	if err := store.Set("invalid", invalid); err == nil {
		t.Fatal("incomplete session was written")
	}
}

func mustEncode(t *testing.T, value Credential) string {
	t.Helper()
	encoded, err := encodeSession(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestParseRejectsSerializedSessionsAndEmbeddedRefreshTokens(t *testing.T) {
	t.Parallel()
	compact := testSession(t)
	readOnly := *compact.Session
	readOnly.Scopes = []string{"character:read"}
	compact.Session = &readOnly
	record := mustEncode(t, compact)
	if len(record) > 512 || strings.ContainsAny(record, " \t\n") {
		t.Fatalf("fixture must be a compact record that passes the generic length and whitespace checks (%d bytes)", len(record))
	}
	for name, value := range map[string]string{
		"compact record":   record,
		"array":            `["` + sessionAccess + `"]`,
		"quoted":           `"` + sessionAccess + `"`,
		"embedded refresh": "prefix-" + sessionRefresh,
		"embedded code":    "prefix-cvoac_0123456789abcdef0123456789abcdef_Y29kZQ",
	} {
		_, err := Parse(value)
		if err == nil {
			t.Fatalf("%s was accepted as a credential", name)
		}
		if strings.Contains(err.Error(), sessionRefresh) || strings.Contains(err.Error(), sessionAccess) {
			t.Fatalf("%s error exposed a credential", name)
		}
	}
	for value, kind := range map[string]config.CredentialType{
		"01234567890123456789012345678901":               config.CredentialAPIKey,
		"AbC-_.~+/=0123456789abcdefgh":                   config.CredentialAPIKey,
		"cvpat_0123456789012345678901234567890123456789": config.CredentialPAT,
		sessionAccess: config.CredentialOAuth,
		// Random typed secrets can contain refresh or code markers.
		"cvpat_0123456789abcdef0123456789abcdef_xcvort_y": config.CredentialPAT,
		"cvpat_0123456789abcdef0123456789abcdef_xcvoac_y": config.CredentialPAT,
		"cvoat_0123456789abcdef0123456789abcdef_xcvort_y": config.CredentialOAuth,
		"cvoat_0123456789abcdef0123456789abcdef_xcvoac_y": config.CredentialOAuth,
	} {
		parsed, err := Parse(value)
		if err != nil || parsed.Type != kind {
			t.Fatalf("valid raw credential = %q, %v", parsed.Type, err)
		}
	}
}

func TestEnvironmentNeverYieldsAManagedOrRefreshCredential(t *testing.T) {
	t.Setenv(EnvironmentToken, mustEncode(t, testSession(t)))
	if _, _, err := Resolve(&memoryStore{}, "default"); err == nil {
		t.Fatal("environment accepted a session record")
	}
	compact := testSession(t)
	readOnly := *compact.Session
	readOnly.Scopes = []string{"character:read"}
	compact.Session = &readOnly
	t.Setenv(EnvironmentToken, mustEncode(t, compact))
	if _, _, err := Resolve(&memoryStore{}, "default"); err == nil {
		t.Fatal("environment accepted a compact session record")
	}
	t.Setenv(EnvironmentToken, sessionRefresh)
	if _, _, err := Resolve(&memoryStore{}, "default"); err == nil {
		t.Fatal("environment accepted a refresh token")
	}
}

func TestRedactRemovesKnownAndPrefixedSecrets(t *testing.T) {
	t.Parallel()
	message := Redact("a "+sessionRefresh+" b opaque-verifier c cvpat_x", "opaque-verifier")
	for _, secret := range []string{sessionRefresh, "opaque-verifier", "cvpat_"} {
		if strings.Contains(message, secret) {
			t.Fatalf("message %q exposed %q", message, secret)
		}
	}
}

func TestLockNameFoldsCaseForCaseInsensitiveStores(t *testing.T) {
	t.Parallel()
	if LockName("Default") != LockName("default") || strings.ContainsAny(LockName("staging"), `/\`) {
		t.Fatal("lock names must identify the keychain account safely")
	}
}

// TestFileLockerSerializesProcesses proves the cross-process boundary: a child
// process cannot take a profile lock that this process holds, and can once it
// is released. In-process tests would not exercise the OS file lock.
func TestFileLockerSerializesProcesses(t *testing.T) {
	if os.Getenv("CONVAI_LOCK_HELPER_DIR") != "" {
		return
	}
	dir := t.TempDir()
	locker := FileLocker{Dir: dir}
	unlock, err := locker.Lock(context.Background(), "Default")
	if err != nil {
		t.Fatal(err)
	}
	child := func() error {
		command := exec.Command(os.Args[0], "-test.run=^TestFileLockerHelperProcess$", "-test.count=1")
		command.Env = append(os.Environ(), "CONVAI_LOCK_HELPER_DIR="+dir)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, output)
		}
		return nil
	}
	if err := child(); err == nil {
		t.Fatal("child process acquired a lock held by the parent")
	}
	unlock()
	if err := child(); err != nil {
		t.Fatalf("child process could not acquire a released lock: %v", err)
	}
	info, err := os.Stat(dir + string(os.PathSeparator) + LockName("default"))
	if err != nil || info.Size() != 0 {
		t.Fatal("lock file must exist and stay empty")
	}
}

func TestFileLockerHelperProcess(t *testing.T) {
	dir := os.Getenv("CONVAI_LOCK_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// A different case of the same profile must contend for the same lock.
	unlock, err := FileLocker{Dir: dir}.Lock(ctx, "default")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("lock is held by another process")
		}
		t.Fatal(err)
	}
	unlock()
}
