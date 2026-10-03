package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// Locker serializes every read-modify-write of one stored credential across
// CLI processes: browser login, token import, refresh rotation, and logout.
type Locker interface {
	Lock(ctx context.Context, profile string) (unlock func(), err error)
}

// FileLocker uses an advisory OS file lock. The lock file is empty and never
// contains credential material.
type FileLocker struct {
	Dir string
}

// DefaultLockDir is keyed to the operating-system account, like the keychain,
// rather than to HOME or XDG_CONFIG_HOME. Two processes that use different
// configuration directories still share one keychain entry per profile, so
// they must also share its lock.
func DefaultLockDir() (string, error) {
	home := ""
	if account, err := user.Current(); err == nil {
		home = account.HomeDir
	}
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("resolve credential lock directory: %w", err)
		}
	}
	return filepath.Join(home, ".convai", "locks"), nil
}

// LockName identifies the keychain service and account. Profile names are
// folded to lower case because some credential stores, such as Windows
// Credential Manager, match account names case-insensitively.
func LockName(profile string) string {
	return ServiceName + "-" + strings.ToLower(profile) + ".lock"
}

func (l FileLocker) Lock(ctx context.Context, profile string) (func(), error) {
	if l.Dir == "" {
		return nil, errors.New("credential lock directory is not configured")
	}
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create credential lock directory: %w", err)
	}
	lock := flock.New(filepath.Join(l.Dir, LockName(profile)), flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("wait for another convai process using profile %s: %w", profile, ctx.Err())
		}
		return nil, fmt.Errorf("lock credential for profile %s: %w", profile, err)
	}
	if !locked {
		return nil, fmt.Errorf("lock credential for profile %s: lock unavailable", profile)
	}
	return func() { _ = lock.Unlock() }, nil
}
