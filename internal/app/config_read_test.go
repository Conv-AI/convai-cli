package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"github.com/Conv-AI/convai-cli/internal/oauth"
)

type checkedConfigStore struct {
	config.Store
	held    atomic.Bool
	loads   atomic.Int32
	loadErr error
}

func (s *checkedConfigStore) Lock(ctx context.Context) (func(), error) {
	unlock, err := s.Store.Lock(ctx)
	if err != nil {
		return nil, err
	}
	s.held.Store(true)
	return func() { s.held.Store(false); unlock() }, nil
}

func (s *checkedConfigStore) Load() (config.Config, error) {
	s.loads.Add(1)
	if !s.held.Load() {
		return config.Config{}, errors.New("configuration read without its lock")
	}
	if s.loadErr != nil {
		return config.Config{}, s.loadErr
	}
	return s.Store.Load()
}

func TestConfigReadsAreLockedThroughoutOAuthLifecycle(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	store := &checkedConfigStore{Store: f.configs}
	assertUnlocked := func() {
		t.Helper()
		if store.held.Load() {
			t.Error("configuration lock held during browser or network interaction")
		}
	}
	transport := f.auth.server.Client().Transport
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertUnlocked()
		return transport.RoundTrip(r)
	})}
	run := func(args ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		root, err := New(Options{
			Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
			ConfigStore: store, CredentialStore: f.store, HTTPClient: client,
			Locker:      credential.FileLocker{Dir: f.lockDir},
			OpenBrowser: func(raw string) error { assertUnlocked(); return f.browser(raw) },
			Interactive: func() bool { return true }, Now: f.clock,
			OAuthEnvironments: []oauth.Environment{{APIURL: f.rest.server.URL, Issuer: f.auth.server.URL}},
		})
		if err != nil {
			t.Fatal(err)
		}
		root.SetArgs(args)
		err = root.ExecuteContext(context.Background())
		f.assertNoSecrets(stdout.String(), stderr.String(), err)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		assertUnlocked()
	}
	run("config", "get")
	run("config", "set", "workspace-id", "personal")
	run("auth", "login", "--read-only")
	run("auth", "login", "--read-only") // Replacing a login revokes its previous grant.
	f.advance(time.Hour)
	run("auth", "status", "--check") // Forces refresh and its authoritative trust check.
	run("character", "list", "--output", "json")
	run("auth", "logout")
}

func TestConfigReadWaitHonorsCancellation(t *testing.T) {
	t.Parallel()
	files := &config.FileStore{Path: filepath.Join(t.TempDir(), "config.yaml")}
	unlock, err := files.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	store := &checkedConfigStore{Store: files}
	a := &application{configStore: store}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.readConfig(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("read waiting for writer: %v", err)
	}
	if store.loads.Load() != 0 {
		t.Fatal("read config without acquiring the writer's lock")
	}
}

func TestConfigReadReleasesLockAfterLoadFailure(t *testing.T) {
	t.Parallel()
	store := &checkedConfigStore{
		Store: &config.FileStore{Path: filepath.Join(t.TempDir(), "config.yaml")}, loadErr: io.ErrUnexpectedEOF,
	}
	a := &application{configStore: store}
	if _, err := a.readConfig(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read config: %v", err)
	}
	if store.held.Load() {
		t.Fatal("failed read retained the configuration lock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := store.Lock(ctx)
	if err != nil {
		t.Fatalf("cannot reacquire lock after failed read: %v", err)
	}
	unlock()
}
