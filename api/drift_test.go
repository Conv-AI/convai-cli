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
		{"release default becomes promotion", func(root map[string]any) {
			root["components"].(map[string]any)["schemas"].(map[string]any)["CharacterVersionCreate"].(map[string]any)["properties"].(map[string]any)["make_latest"].(map[string]any)["default"] = true
		}},
		{"deprecation default becomes activation", func(root map[string]any) {
			root["components"].(map[string]any)["schemas"].(map[string]any)["CharacterVersionDeprecate"].(map[string]any)["properties"].(map[string]any)["deprecated"].(map[string]any)["default"] = false
		}},
		{"default removed", func(root map[string]any) {
			delete(root["components"].(map[string]any)["schemas"].(map[string]any)["CharacterVersionCreate"].(map[string]any)["properties"].(map[string]any)["make_latest"].(map[string]any), "default")
		}},
		{"default added", func(root map[string]any) {
			root["components"].(map[string]any)["schemas"].(map[string]any)["CharacterVersionCreate"].(map[string]any)["properties"].(map[string]any)["version"].(map[string]any)["default"] = "1.0"
		}},
		{"required inherited query", func(root map[string]any) {
			root["paths"].(map[string]any)["/character/list"].(map[string]any)["parameters"] = []any{map[string]any{"name": "tenant", "in": "query", "required": true, "schema": map[string]any{"type": "string"}}}
		}},
		{"required inherited header", func(root map[string]any) {
			root["paths"].(map[string]any)["/character/list"].(map[string]any)["parameters"] = []any{map[string]any{"name": "X-Tenant", "in": "header", "required": true, "schema": map[string]any{"type": "string"}}}
		}},
		{"required inherited parameter reference", func(root map[string]any) {
			root["components"].(map[string]any)["parameters"] = map[string]any{"Tenant": map[string]any{"name": "tenant", "in": "query", "required": true, "schema": map[string]any{"type": "string"}}}
			root["paths"].(map[string]any)["/character/list"].(map[string]any)["parameters"] = []any{map[string]any{"$ref": "#/components/parameters/Tenant"}}
		}},
		{"missing parameter reference", func(root map[string]any) {
			root["paths"].(map[string]any)["/character/list"].(map[string]any)["parameters"] = []any{map[string]any{"$ref": "#/components/parameters/Missing"}}
		}},
		{"removed API key alternative", func(root map[string]any) {
			root["paths"].(map[string]any)["/character/list"].(map[string]any)["get"].(map[string]any)["security"] = []any{map[string]any{"PersonalAccessTokenAuth": []any{}}}
		}},
		{"explicit unauthenticated override", func(root map[string]any) {
			root["paths"].(map[string]any)["/character/list"].(map[string]any)["get"].(map[string]any)["security"] = []any{}
		}},
		{"inherited authentication removed", func(root map[string]any) {
			delete(root["paths"].(map[string]any)["/character/list"].(map[string]any)["get"].(map[string]any), "security")
		}},
		{"inherited authentication changed", func(root map[string]any) {
			delete(root["paths"].(map[string]any)["/character/list"].(map[string]any)["get"].(map[string]any), "security")
			root["security"] = []any{map[string]any{"PersonalAccessTokenAuth": []any{}}}
		}},
		{"API key header changed", func(root map[string]any) {
			root["components"].(map[string]any)["securitySchemes"].(map[string]any)["ConvaiApiKeyAuth"].(map[string]any)["name"] = "X-API-Key"
		}},
		{"Bearer scheme changed", func(root map[string]any) {
			root["components"].(map[string]any)["securitySchemes"].(map[string]any)["PersonalAccessTokenAuth"].(map[string]any)["scheme"] = "basic"
		}},
		{"authentication scheme removed", func(root map[string]any) {
			delete(root["components"].(map[string]any)["securitySchemes"].(map[string]any), "ConvaiApiKeyAuth")
		}},
		{"referenced authentication scheme changed", func(root map[string]any) {
			schemes := root["components"].(map[string]any)["securitySchemes"].(map[string]any)
			schemes["APIKeyDefinition"] = schemes["ConvaiApiKeyAuth"]
			schemes["APIKeyDefinition"].(map[string]any)["name"] = "X-API-Key"
			schemes["ConvaiApiKeyAuth"] = map[string]any{"$ref": "#/components/securitySchemes/APIKeyDefinition"}
		}},
		{"cyclic authentication scheme", func(root map[string]any) {
			root["components"].(map[string]any)["securitySchemes"].(map[string]any)["ConvaiApiKeyAuth"] = map[string]any{"$ref": "#/components/securitySchemes/ConvaiApiKeyAuth"}
		}},
		{"explicit override ignores root authentication", func(root map[string]any) {
			operation := root["paths"].(map[string]any)["/character/list"].(map[string]any)["get"].(map[string]any)
			root["security"] = operation["security"]
			operation["security"] = []any{}
		}},
		{"authentication scope added", func(root map[string]any) {
			root["paths"].(map[string]any)["/character/list"].(map[string]any)["get"].(map[string]any)["security"] = []any{map[string]any{"ConvaiApiKeyAuth": []any{"character:read"}}, map[string]any{"PersonalAccessTokenAuth": []any{}}}
		}},
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

func TestEquivalentUpstreamAuthentication(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"root inheritance", "alternative order", "referenced scheme"} {
		t.Run(variant, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(contract.Document, &document); err != nil {
				t.Fatal(err)
			}
			operation := document["paths"].(map[string]any)["/character/list"].(map[string]any)["get"].(map[string]any)
			switch variant {
			case "root inheritance":
				document["security"] = operation["security"]
				delete(operation, "security")
			case "alternative order":
				alternatives := operation["security"].([]any)
				alternatives[0], alternatives[1] = alternatives[1], alternatives[0]
			case "referenced scheme":
				schemes := document["components"].(map[string]any)["securitySchemes"].(map[string]any)
				schemes["APIKeyDefinition"] = schemes["ConvaiApiKeyAuth"]
				schemes["ConvaiApiKeyAuth"] = map[string]any{"$ref": "#/components/securitySchemes/APIKeyDefinition"}
			}
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := contract.CheckUpstream(data); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCompatiblePathItemParameters(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"optional inherited parameter", "move parameters to path", "operation override", "referenced inherited parameter"} {
		t.Run(variant, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(contract.Document, &document); err != nil {
				t.Fatal(err)
			}
			item := document["paths"].(map[string]any)["/character/list"].(map[string]any)
			operation := item["get"].(map[string]any)
			switch variant {
			case "optional inherited parameter":
				item["parameters"] = []any{map[string]any{"name": "tenant", "in": "query", "required": false, "schema": map[string]any{"type": "string"}}}
			case "move parameters to path":
				item["parameters"] = operation["parameters"]
				delete(operation, "parameters")
			case "operation override":
				item["parameters"] = []any{map[string]any{"name": "limit", "in": "query", "required": true, "schema": map[string]any{"type": "integer"}}}
			case "referenced inherited parameter":
				parameters := operation["parameters"].([]any)
				document["components"].(map[string]any)["parameters"] = map[string]any{"ListParameter": parameters[0]}
				item["parameters"] = []any{map[string]any{"$ref": "#/components/parameters/ListParameter"}}
				operation["parameters"] = parameters[1:]
			}
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := contract.CheckUpstream(data); err != nil {
				t.Fatal(err)
			}
		})
	}
}
