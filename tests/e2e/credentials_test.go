package e2e

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAuthReleaseStaging(t *testing.T) {
	// Preflight every gate before issuing credentials or creating any fixture.
	fixture(t, "CONVAI_E2E_TOKEN")
	fixture(t, "CONVAI_E2E_FIREBASE_ID_TOKEN")
	requireOptIn(t, "CONVAI_E2E_OAUTH")
	requireOptIn(t, "CONVAI_E2E_MUTATIONS")
	t.Run("PAT", TestStagingPATCredentialMatrix)
	t.Run("OAuth", TestStagingOAuthCredentialMatrix)
}

// Managed tests issue their own short-lived fixtures through Auth, never through the DB.
func TestStagingPATCredentialMatrix(t *testing.T) {
	firebase := fixture(t, "CONVAI_E2E_FIREBASE_ID_TOKEN")
	requireOptIn(t, "CONVAI_E2E_MUTATIONS")
	primary := newHarness(t)
	client := newManagementClient()
	credentialMatrix(t, primary, func(t *testing.T, scopes []string) harness {
		credential, err := client.createPAT(firebase, scopes)
		if err != nil {
			t.Fatalf("issue temporary PAT: %v", err)
		}
		h := primary
		h.token, h.workspace = credential.AccessToken, "personal"
		t.Cleanup(func() {
			if err := client.revokePAT(firebase, credential); err != nil {
				t.Errorf("revoke temporary PAT: %v", err)
				return
			}
			verifyRevoked(t, h)
		})
		return h
	})
}

func TestStagingOAuthCredentialMatrix(t *testing.T) {
	requireOptIn(t, "CONVAI_E2E_OAUTH")
	requireOptIn(t, "CONVAI_E2E_MUTATIONS")
	primary := newHarness(t)
	client := newManagementClient()
	credentialMatrix(t, primary, func(t *testing.T, scopes []string) harness {
		credential, err := client.authorizeOAuth(t, scopes)
		if err != nil {
			t.Fatalf("issue temporary OAuth credential: %v", err)
		}
		h := primary
		h.token, h.workspace = credential.AccessToken, "personal"
		pairs := []issuedCredential{credential}
		t.Cleanup(func() {
			for _, pair := range pairs {
				if err := client.revokeOAuth(pair); err != nil {
					t.Errorf("revoke temporary OAuth credentials: %v", err)
				}
			}
			for _, pair := range pairs {
				probe := h
				probe.token = pair.AccessToken
				verifyRevoked(t, probe)
				if err := client.verifyOAuthRefreshRevoked(pair); err != nil {
					t.Errorf("verify refresh-token revocation: %v", err)
				}
			}
		})
		if _, ok := h.run(t, "", "auth", "status", "--check"); !ok {
			t.Fatal("fresh OAuth access credential verification failed")
		}
		rotated, err := client.refreshOAuth(credential)
		if err != nil {
			t.Fatalf("positive OAuth refresh verification: %v", err)
		}
		pairs = append(pairs, rotated)
		if rotated.AccessToken == credential.AccessToken || rotated.RefreshToken == credential.RefreshToken {
			t.Fatal("OAuth refresh did not rotate both credentials")
		}
		h.token = rotated.AccessToken
		return h
	})
}

func credentialMatrix(t *testing.T, foreignOwner harness, issue func(*testing.T, []string) harness) {
	t.Helper()
	foreignID, foreign := temporaryCharacter(t, foreignOwner)
	write := issue(t, []string{"character:read", "character:write"})
	ownedID, owned := temporaryCharacter(t, write)
	verifyDistinctOwners(t, foreign, owned)
	t.Run("writable-auth-and-reads", func(t *testing.T) { verifyReads(t, write, false) })
	t.Run("writable-authoring-lifecycle", func(t *testing.T) { authoringLifecycle(t, write) })
	t.Run("writable-ownership-and-workspace", func(t *testing.T) {
		verifyForeign(t, foreignOwner, write, foreignID, foreign)
	})
	read := issue(t, []string{"character:read"})
	t.Run("read-only-auth-and-reads", func(t *testing.T) { verifyReads(t, read, false) })
	t.Run("read-only-write-denial", func(t *testing.T) { verifyReadOnly(t, write, read, ownedID) })
	t.Run("read-only-ownership-and-workspace", func(t *testing.T) {
		verifyForeign(t, foreignOwner, read, foreignID, foreign)
	})
}

func verifyDistinctOwners(t *testing.T, first, second map[string]any) {
	t.Helper()
	a, _ := first["user_id"].(string)
	b, _ := second["user_id"].(string)
	if a == "" || b == "" || a == b {
		t.Fatal("negative ownership tests require verified, distinct owner accounts")
	}
}

func temporaryCharacter(t *testing.T, h harness) (string, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"charName":  "CLI auth boundary " + time.Now().UTC().Format("20060102150405"),
		"backstory": "Temporary CLI authorization fixture. Delete after testing.", "autoSelectAssets": false,
	})
	created, ok := h.run(t, string(body), "character", "create", "--file", "-")
	if !ok {
		t.Fatal("create temporary authorization fixture failed")
	}
	id, ok := created["character_id"].(string)
	if !ok || id == "" {
		t.Fatal("temporary character omitted its ID; inspect account before rerunning")
	}
	cleanupCharacter(t, h, id)
	got, ok := h.run(t, "", "character", "get", id)
	if !ok || got["character_id"] != id {
		t.Fatal("temporary fixture owner readback failed")
	}
	return id, got
}

func cleanupCharacter(t *testing.T, h harness, id string) {
	t.Helper()
	t.Cleanup(func() {
		if _, ok := h.run(t, "", "character", "delete", id, "--yes"); !ok {
			t.Errorf("cleanup failed for temporary character %s", id)
			return
		}
		if _, status, ok := h.runWithStatus(t, "", "character", "get", id); ok || status != 404 {
			t.Errorf("deleted temporary character %s must return HTTP 404, got %d", id, status)
		}
	})
}

func verifyReadOnly(t *testing.T, owner, read harness, id string) {
	t.Helper()
	before, ok := owner.run(t, "", "character", "get", id)
	if !ok {
		t.Fatal("owner readback before scope denial failed")
	}
	if _, ok := read.run(t, "", "character", "get", id); !ok {
		t.Fatal("read-only credential cannot read the same-account fixture")
	}
	if _, status, ok := read.runWithStatus(t, `{"backstory":"Unexpected write: scope enforcement failed."}`, "character", "update", id, "--file", "-", "--yes"); ok || status != 403 {
		t.Fatalf("read-only write denial must be HTTP 403, got %d", status)
	}
	after, ok := owner.run(t, "", "character", "get", id)
	if !ok || after["backstory"] != before["backstory"] {
		t.Fatal("read-only denial changed the temporary character")
	}
}

func verifyForeign(t *testing.T, owner, caller harness, id string, character map[string]any) {
	t.Helper()
	workspace, ok := character["workspace_id"].(string)
	if !ok || workspace == "" || workspace == "personal" {
		t.Fatal("foreign owner did not return a concrete Workspace ID")
	}
	owner.workspace = workspace
	if got, ok := owner.run(t, "", "character", "get", id); !ok || got["workspace_id"] != workspace {
		t.Fatal("foreign character/Workspace existence verification failed")
	}
	if _, ok := owner.run(t, "", "character", "list", "--limit", "1"); !ok {
		t.Fatal("foreign Workspace owner verification failed")
	}
	if _, status, ok := caller.runWithStatus(t, "", "character", "get", id); ok || status != 404 {
		t.Fatalf("foreign character must be hidden with HTTP 404, got %d", status)
	}
	caller.workspace = workspace
	if _, status, ok := caller.runWithStatus(t, "", "character", "list"); ok || (status != 403 && status != 404) {
		t.Fatalf("foreign Workspace denial must be HTTP 403/404, got %d", status)
	}
}

func verifyRevoked(t *testing.T, h harness) {
	t.Helper()
	if _, status, ok := h.runWithStatus(t, "", "character", "list"); ok || status != 401 {
		t.Errorf("revoked credential must return HTTP 401, got %d", status)
	}
}
