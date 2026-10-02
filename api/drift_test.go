package contract_test

import (
	"encoding/json"
	"testing"

	contract "github.com/Conv-AI/convai-cli/api"
)

func TestUpstreamDriftGate(t *testing.T) {
	t.Parallel()
	if err := contract.CheckUpstream(contract.Document); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"removed operation", func(root map[string]any) { delete(root["paths"].(map[string]any), "/character/delete") }},
		{"withdrawn approval", func(root map[string]any) {
			root["paths"].(map[string]any)["/character/update"].(map[string]any)["post"].(map[string]any)["x-convai-mcp"].(map[string]any)["exposed"] = false
		}},
		{"PAT support withdrawn", func(root map[string]any) {
			metadata := root["paths"].(map[string]any)["/character/update"].(map[string]any)["post"].(map[string]any)["x-convai-mcp"].(map[string]any)
			metadata["tools"].([]any)[0].(map[string]any)["transport"].(map[string]any)["supportsPat"] = false
		}},
		{"association template changed", func(root map[string]any) {
			metadata := root["paths"].(map[string]any)["/character/update"].(map[string]any)["post"].(map[string]any)["x-convai-mcp"].(map[string]any)
			metadata["tools"].([]any)[1].(map[string]any)["requestMapping"].(map[string]any)["template"] = map[string]any{"functions": []any{}}
		}},
		{"conditional scopes changed", func(root map[string]any) {
			metadata := root["paths"].(map[string]any)["/character/update"].(map[string]any)["post"].(map[string]any)["x-convai-mcp"].(map[string]any)
			metadata["tools"].([]any)[1].(map[string]any)["conditionalScopes"] = []any{"knowledge-bank:write"}
		}},
		{"source lines tightened", func(root map[string]any) {
			root["paths"].(map[string]any)["/functions/create"].(map[string]any)["post"].(map[string]any)["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)["source_code"].(map[string]any)["x-convai-maxLines"] = float64(200)
		}},
		{"reference sibling tightened", func(root map[string]any) {
			root["paths"].(map[string]any)["/character/update"].(map[string]any)["post"].(map[string]any)["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["minProperties"] = float64(3)
		}},
		{"removed success response", func(root map[string]any) {
			delete(root["paths"].(map[string]any)["/character/get"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any), "200")
		}},
		{"required output made optional", func(root map[string]any) {
			schemas := root["components"].(map[string]any)["schemas"].(map[string]any)
			response := schemas["CharacterResponse"].(map[string]any)
			required := []any{}
			for _, name := range response["required"].([]any) {
				if name != "character_id" {
					required = append(required, name)
				}
			}
			response["required"] = required
		}},
		{"nested required output made optional", func(root map[string]any) {
			schemas := root["components"].(map[string]any)["schemas"].(map[string]any)
			response := schemas["CharacterListItem"].(map[string]any)
			required := []any{}
			for _, name := range response["required"].([]any) {
				if name != "character_id" {
					required = append(required, name)
				}
			}
			response["required"] = required
		}},
		{"union raw diff required output made optional", func(root map[string]any) {
			schemas := root["components"].(map[string]any)["schemas"].(map[string]any)
			response := schemas["CharacterVersionDiffResponse"].(map[string]any)
			response["required"] = []any{}
		}},
		{"union semantic diff nested output type changed", func(root map[string]any) {
			schemas := root["components"].(map[string]any)["schemas"].(map[string]any)
			response := schemas["CharacterVersionSemanticDiffResponse"].(map[string]any)
			response["properties"].(map[string]any)["character_id"].(map[string]any)["type"] = "integer"
		}},
		{"output map value type changed", func(root map[string]any) {
			schemas := root["components"].(map[string]any)["schemas"].(map[string]any)
			property := schemas["CharacterResponse"].(map[string]any)["properties"].(map[string]any)["boosted_words"].(map[string]any)
			property["anyOf"].([]any)[0].(map[string]any)["items"].(map[string]any)["additionalProperties"].(map[string]any)["type"] = "integer"
		}},
		{"required field", func(root map[string]any) {
			schemas := root["components"].(map[string]any)["schemas"].(map[string]any)
			create := schemas["CharacterCreate"].(map[string]any)
			create["required"] = append(create["required"].([]any), "voiceType")
		}},
		{"removed alias", func(root map[string]any) {
			schemas := root["components"].(map[string]any)["schemas"].(map[string]any)
			delete(schemas["CharacterUpdate"].(map[string]any)["properties"].(map[string]any), "actions_enabled")
		}},
		{"tightened length", func(root map[string]any) {
			schemas := root["components"].(map[string]any)["schemas"].(map[string]any)
			schemas["CharacterCreate"].(map[string]any)["properties"].(map[string]any)["charName"].(map[string]any)["maxLength"] = float64(10)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(contract.Document, &document); err != nil {
				t.Fatal(err)
			}
			tc.mutate(document)
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := contract.CheckUpstream(data); err == nil {
				t.Fatal("breaking upstream drift was accepted")
			}
		})
	}
}
