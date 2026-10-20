package contract_test

import (
	"encoding/json"
	"testing"

	contract "github.com/Conv-AI/convai-cli/api"
)

func TestAvatarUpdateOwnerConstraints(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"snake", `{"avatar_id":"11111111-1111-4111-8111-111111111111"}`, true},
		{"camel", `{"avatarId":"11111111-1111-4111-8111-111111111111"}`, true},
		{"invalid UUID", `{"avatar_id":"bad"}`, false},
		{"null", `{"avatar_id":null}`, false},
		{"aliases", `{"avatar_id":"11111111-1111-4111-8111-111111111111","avatarId":"11111111-1111-4111-8111-111111111111"}`, false},
		{"model fields", `{"model_link":"https://example.com/model","model_placeholder":"https://example.com/preview"}`, true},
		{"model aliases", `{"model_link":"one","modelLink":"two"}`, false},
		{"older request", `{"backstory":"A guide."}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := contract.ValidateInput("character_update", map[string]any{
				"character_id": "11111111-1111-4111-8111-111111111111",
			}, []byte(tc.body))
			if (err == nil) != tc.ok {
				t.Fatalf("valid=%v, error=%v", tc.ok, err)
			}
		})
	}
}

func TestRefreshedOwnerMetadataRemainsGoverned(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(contract.Document, &root); err != nil {
		t.Fatal(err)
	}
	paths := root["paths"].(map[string]any)
	list := paths["/character/list"].(map[string]any)["get"].(map[string]any)
	list["x-convai-workspace"].(map[string]any)["contract_version"] = "unreviewed"
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.CheckUpstream(data); err == nil {
		t.Fatal("unreviewed Workspace metadata change passed the drift gate")
	}
}
