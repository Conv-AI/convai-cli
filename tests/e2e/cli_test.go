package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type harness struct{ binary, home, token, workspace string }

func newHarness(t *testing.T) harness {
	t.Helper()
	return newCredentialHarness(t, fixture(t, "CONVAI_E2E_TOKEN"))
}

func newCredentialHarness(t *testing.T, token string) harness {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "convai")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/convai")
	build.Dir = "../.."
	if err := build.Run(); err != nil {
		t.Fatalf("build CLI: %v", err)
	}
	workspace := os.Getenv("CONVAI_E2E_WORKSPACE")
	if workspace == "" {
		workspace = "personal"
	}
	return harness{binary, dir, token, workspace}
}

func (h harness) run(t *testing.T, body string, args ...string) (map[string]any, bool) {
	t.Helper()
	value, _, ok := h.runWithStatus(t, body, args...)
	return value, ok
}

func (h harness) runWithStatus(t *testing.T, body string, args ...string) (map[string]any, int, bool) {
	t.Helper()
	args = append(args, "--api-url", "https://api2-stg.convai.com", "--workspace", h.workspace, "--output", "json")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, h.binary, args...)
	command.Env = commandEnvironment(os.Environ(), h)
	command.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if strings.Contains(stdout.String(), h.token) || strings.Contains(stderr.String(), h.token) {
		t.Fatal("CLI exposed the test credential")
	}
	if err != nil {
		// An error is evidence only when the actual CLI returned a structured HTTP status.
		var diagnostic struct {
			StatusCode int `json:"status_code"`
			ExitCode   int `json:"exit_code"`
		}
		if json.Unmarshal(stderr.Bytes(), &diagnostic) != nil {
			t.Fatal("CLI failed without a structured diagnostic")
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || diagnostic.ExitCode != exit.ExitCode() || ((diagnostic.StatusCode == 401 || diagnostic.StatusCode == 403) && exit.ExitCode() != 3) {
			t.Fatal("CLI diagnostic and process exit status disagree")
		}
		t.Logf("CLI %s: HTTP %d", strings.Join(args[:min(2, len(args))], " "), diagnostic.StatusCode)
		return nil, diagnostic.StatusCode, false
	}
	var value map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &value); err != nil {
		t.Fatalf("CLI did not return a JSON object")
	}
	return value, 0, true
}

func TestStagingReadAndAuthentication(t *testing.T) {
	h := newHarness(t)
	verifyReads(t, h, true)
}

func verifyReads(t *testing.T, h harness, discovery bool) {
	t.Helper()
	if _, ok := h.run(t, "", "auth", "status", "--check"); !ok {
		t.Fatal("credential verification failed")
	}
	id, _ := firstOwnedCharacter(t, h)
	if discovery {
		for _, group := range []string{"voice", "language", "model"} {
			if _, ok := h.run(t, "", group, "list"); !ok {
				t.Fatalf("%s catalog failed", group)
			}
		}
	}
	if _, ok := h.run(t, "", "character", "version", "list", id); !ok {
		t.Fatal("version listing failed")
	}
	invalid := h
	invalid.token = "cvpat_00000000000000000000000000000000"
	if _, status, ok := invalid.runWithStatus(t, "", "character", "list"); ok || status != 401 {
		t.Fatalf("invalid credential denial must be HTTP 401, got %d", status)
	}
}

func firstOwnedCharacter(t *testing.T, h harness) (string, map[string]any) {
	t.Helper()
	listed, ok := h.run(t, "", "character", "list", "--limit", "1")
	if !ok {
		t.Fatal("list failed")
	}
	items, ok := listed["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatal("fixture account needs an owned character")
	}
	id, ok := items[0].(map[string]any)["character_id"].(string)
	if !ok || id == "" {
		t.Fatal("list did not return a character ID")
	}
	got, ok := h.run(t, "", "character", "get", id)
	if !ok || got["character_id"] != id {
		t.Fatal("owned-character get failed")
	}
	return id, got
}

func TestStagingAuthoringLifecycle(t *testing.T) {
	requireOptIn(t, "CONVAI_E2E_MUTATIONS")
	h := newHarness(t)
	authoringLifecycle(t, h)
}

func authoringLifecycle(t *testing.T, h harness) {
	t.Helper()
	name := "CLI E2E " + time.Now().UTC().Format("20060102150405")
	body, _ := json.Marshal(map[string]any{"charName": name, "backstory": "A temporary CLI regression fixture. Delete after testing.", "autoSelectAssets": false})
	created, ok := h.run(t, string(body), "character", "create", "--file", "-")
	if !ok {
		t.Fatal("create failed")
	}
	id, ok := created["character_id"].(string)
	if !ok || id == "" {
		t.Fatal("create did not return a character ID")
	}
	cleanupCharacter(t, h, id)
	if _, ok := h.run(t, `{"backstory":"Updated temporary CLI fixture.","speaking_style_description":null,"actions_enabled":false}`, "character", "update", id, "--file", "-", "--yes"); !ok {
		t.Fatal("update failed")
	}
	got, ok := h.run(t, "", "character", "get", id)
	if !ok || got["backstory"] != "Updated temporary CLI fixture." || got["actions_enabled"] != false {
		t.Fatal("update readback failed")
	}
	if _, ok := h.run(t, "", "character", "version", "release", id, "1.0"); !ok {
		t.Fatal("release failed")
	}
	if _, ok := h.run(t, "", "character", "version", "promote", id, "1.0", "--yes"); !ok {
		t.Fatal("promote failed")
	}
	versions, ok := h.run(t, "", "character", "version", "list", id)
	if !ok || versions["latest_version"] != "1.0" {
		t.Fatal("promotion readback failed")
	}
	clone, ok := h.run(t, "", "character", "clone", id)
	if !ok {
		t.Fatal("clone failed")
	}
	cloneID, ok := clone["character_id"].(string)
	if !ok || cloneID == "" || cloneID == id {
		t.Fatal("clone identity was invalid")
	}
	cleanupCharacter(t, h, cloneID)
}

func TestStagingReadOnlyPATCannotWrite(t *testing.T) {
	token := fixture(t, "CONVAI_E2E_READ_ONLY_PAT")
	requireOptIn(t, "CONVAI_E2E_MUTATIONS")
	h := newHarness(t)
	id, _ := temporaryCharacter(t, h)
	readOnly := h
	readOnly.token = token
	verifyReadOnly(t, h, readOnly, id)
}

func TestStagingOwnershipAndWorkspaceDenials(t *testing.T) {
	foreign := fixture(t, "CONVAI_E2E_FOREIGN_CHARACTER_ID")
	workspace := fixture(t, "CONVAI_E2E_FORBIDDEN_WORKSPACE")
	ownerToken := fixture(t, "CONVAI_E2E_FOREIGN_OWNER_TOKEN")
	h := newHarness(t)
	owner := h
	owner.token, owner.workspace = ownerToken, workspace
	got, ok := owner.run(t, "", "character", "get", foreign)
	if !ok || got["workspace_id"] != workspace {
		t.Fatal("foreign fixture existence/owner verification failed")
	}
	_, own := firstOwnedCharacter(t, h)
	verifyDistinctOwners(t, own, got)
	if _, ok := owner.run(t, "", "character", "list", "--limit", "1"); !ok {
		t.Fatal("foreign Workspace existence/owner verification failed")
	}
	if _, status, ok := h.runWithStatus(t, "", "character", "get", foreign); ok || status != 404 {
		t.Fatalf("foreign character must be hidden with HTTP 404, got %d", status)
	}
	h.workspace = workspace
	if _, status, ok := h.runWithStatus(t, "", "character", "list"); ok || (status != 403 && status != 404) {
		t.Fatalf("forbidden Workspace must return HTTP 403/404, got %d", status)
	}
}

func TestStagingPlanDenial(t *testing.T) {
	token := fixture(t, "CONVAI_E2E_UNENTITLED_TOKEN")
	h := newHarness(t)
	h.token = token
	if _, status, ok := h.runWithStatus(t, "", "character", "list"); ok || status != 403 {
		t.Fatalf("unentitled credential must return HTTP 403, got %d", status)
	}
}
