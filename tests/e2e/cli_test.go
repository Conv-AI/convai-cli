package e2e

import (
	"bytes"
	"encoding/json"
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
	token := os.Getenv("CONVAI_E2E_TOKEN")
	if token == "" {
		t.Skip("set CONVAI_E2E_TOKEN for opt-in staging E2E")
	}
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
	command := exec.Command(h.binary, args...)
	command.Env = append(os.Environ(), "HOME="+h.home, "XDG_CONFIG_HOME="+h.home, "CONVAI_TOKEN="+h.token)
	command.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if strings.Contains(stdout.String(), h.token) || strings.Contains(stderr.String(), h.token) {
		t.Fatal("CLI exposed the test credential")
	}
	if err != nil {
		// Log only sanitized, bounded CLI diagnostics, never command.Env.
		message := strings.ReplaceAll(stderr.String(), h.token, "[REDACTED]")
		if len(message) > 1000 {
			message = message[:1000]
		}
		t.Logf("CLI %s failed: %s", strings.Join(args[:min(2, len(args))], " "), message)
		var diagnostic struct {
			StatusCode int `json:"status_code"`
		}
		_ = json.Unmarshal(stderr.Bytes(), &diagnostic)
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
	if _, ok := h.run(t, "", "auth", "status", "--check"); !ok {
		t.Fatal("credential verification failed")
	}
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
	for _, group := range []string{"voice", "language", "model"} {
		if _, ok := h.run(t, "", group, "list"); !ok {
			t.Fatalf("%s catalog failed", group)
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

func TestStagingAuthoringLifecycle(t *testing.T) {
	if os.Getenv("CONVAI_E2E_MUTATIONS") != "1" {
		t.Skip("set CONVAI_E2E_MUTATIONS=1 to create and clean up temporary staging fixtures")
	}
	h := newHarness(t)
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
	t.Cleanup(func() {
		if _, ok := h.run(t, "", "character", "delete", id, "--yes"); !ok {
			t.Errorf("cleanup failed for temporary character %s", id)
		}
	})
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
	t.Cleanup(func() {
		if _, ok := h.run(t, "", "character", "delete", cloneID, "--yes"); !ok {
			t.Errorf("cleanup failed for temporary clone %s", cloneID)
		}
	})
}

func TestStagingReadOnlyPATCannotWrite(t *testing.T) {
	if os.Getenv("CONVAI_E2E_READ_ONLY_PAT") == "" {
		t.Skip("set CONVAI_E2E_READ_ONLY_PAT for real scope-denial proof")
	}
	h := newHarness(t)
	h.token = os.Getenv("CONVAI_E2E_READ_ONLY_PAT")
	listed, ok := h.run(t, "", "character", "list", "--limit", "1")
	if !ok {
		t.Fatal("read-only PAT could not read")
	}
	items, ok := listed["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatal("read-only fixture needs an owned character")
	}
	id := items[0].(map[string]any)["character_id"].(string)
	got, ok := h.run(t, "", "character", "get", id)
	if !ok || got["backstory"] == nil {
		t.Fatal("fixture must have a readable backstory")
	}
	// Submit an idempotent update, not an invalid body rejected locally.
	body, _ := json.Marshal(map[string]any{"backstory": got["backstory"]})
	if _, status, ok := h.runWithStatus(t, string(body), "character", "update", id, "--file", "-", "--yes"); ok || status != 403 {
		t.Fatalf("read-only PAT write denial must be HTTP 403, got %d", status)
	}
}

func TestStagingOwnershipAndWorkspaceDenials(t *testing.T) {
	foreign := os.Getenv("CONVAI_E2E_FOREIGN_CHARACTER_ID")
	workspace := os.Getenv("CONVAI_E2E_FORBIDDEN_WORKSPACE")
	if foreign == "" || workspace == "" {
		t.Skip("set foreign-character and forbidden-Workspace fixtures for live ownership checks")
	}
	h := newHarness(t)
	if _, status, ok := h.runWithStatus(t, "", "character", "get", foreign); ok || status != 404 {
		t.Fatalf("foreign character must be hidden with HTTP 404, got %d", status)
	}
	h.workspace = workspace
	if _, status, ok := h.runWithStatus(t, "", "character", "list"); ok || (status != 403 && status != 404) {
		t.Fatalf("forbidden Workspace must return HTTP 403/404, got %d", status)
	}
}

func TestStagingPlanDenial(t *testing.T) {
	token := os.Getenv("CONVAI_E2E_UNENTITLED_TOKEN")
	if token == "" {
		t.Skip("set a separately provisioned unentitled staging token for live plan-denial proof")
	}
	h := newHarness(t)
	h.token = token
	if _, status, ok := h.runWithStatus(t, "", "character", "list"); ok || status != 403 {
		t.Fatalf("unentitled credential must return HTTP 403, got %d", status)
	}
}
