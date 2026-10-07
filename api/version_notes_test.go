package contract_test

import (
	"encoding/json"
	"strings"
	"testing"

	contract "github.com/Conv-AI/convai-cli/api"
)

func TestVersionReleaseNotesPreserveOptionalInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		note any
		set  bool
		ok   bool
	}{
		{"omitted", nil, false, true},
		{"null", nil, true, true},
		{"text", "First customer release", true, true},
		{"limit", strings.Repeat("x", 500), true, true},
		{"over limit", strings.Repeat("x", 501), true, false},
		{"wrong type", 42, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"version": "1.0"}
			if tc.set {
				body["note"] = tc.note
			}
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			err = contract.ValidateInput("character_version_create", map[string]any{
				"character_id": "11111111-1111-4111-8111-111111111111",
			}, data)
			if (err == nil) != tc.ok {
				t.Fatalf("valid=%v, error=%v", tc.ok, err)
			}
		})
	}
}

func TestVersionListsAcceptOlderOwnersWithoutNotes(t *testing.T) {
	for _, tc := range []struct {
		name string
		note any
		set  bool
		ok   bool
	}{
		{"old owner", nil, false, true},
		{"null", nil, true, true},
		{"text", "First customer release", true, true},
		{"wrong type", 42, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := map[string]any{
				"version": "1.0", "revision_id": "revision-1", "is_latest": true,
				"created_by": "fixture", "created_at": "2026-10-07T12:00:00",
				"is_deprecated": false, "deprecated_at": nil,
			}
			if tc.set {
				item["note"] = tc.note
			}
			data, err := json.Marshal(map[string]any{
				"items": []any{item}, "latest_revision_id": "revision-1", "latest_version": "1.0",
				"draft_revision_id": "revision-1", "draft_parent_revision_id": nil,
				"draft_parent_version": nil, "has_unpublished_changes": false, "latest_is_promoted": true,
			})
			if err != nil {
				t.Fatal(err)
			}
			err = contract.ValidateResponse("character_version_list", "200", data)
			if (err == nil) != tc.ok {
				t.Fatalf("valid=%v, error=%v", tc.ok, err)
			}
		})
	}
}
