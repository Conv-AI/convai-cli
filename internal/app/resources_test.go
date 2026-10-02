package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contract "github.com/Conv-AI/convai-cli/api"
)

const testUUID = "df076c58-43f5-11f1-a81f-42010a080006"

func TestReviewedCommandCoverageAndWireRouting(t *testing.T) {
	t.Parallel()
	spec, commands, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range commands {
		if definition.OperationID == "character_list" || definition.OperationID == "character_get" || definition.OperationID == "character_create" {
			continue
		}
		t.Run(definition.Group+"/"+definition.Name, func(t *testing.T) {
			op, _ := spec.Find(definition.OperationID)
			body := map[string]any{}
			if schema, ok := op.RequestBody.Content["application/json"]; ok {
				body = sampleValue(spec, schema.Schema).(map[string]any)
			}
			switch definition.Group + " " + definition.Name {
			case "character update":
				body["backstory"] = "A valid update."
			case "character version revert":
				body["from"] = "latest"
			case "narrative section update":
				body["objective"] = "A valid objective."
			case "narrative trigger update":
				body["trigger_message"] = "A valid trigger."
			case "narrative start":
				body["start_narrative_section_id"] = nil
			case "function create":
				body["language"] = "python"
				body["source_code"] = "def main():\n    return {}"
				body["input_description"] = `{"parameters":{},"required":[]}`
			case "function update":
				body["description"] = "A valid description."
			case "function test":
				body["function_id"] = testUUID
			}
			args := append(strings.Fields(definition.Group), definition.Name)
			for _, name := range definition.Arguments {
				value := testUUID
				if name == "version" {
					value = "1.0"
				}
				if name == "from" {
					value = "latest"
				}
				if name == "to" {
					value = "draft"
				}
				args = append(args, value)
				if contains(definition.BodyArguments, name) {
					body[name] = value
				}
			}
			if definition.FileRequired || len(body) > 0 {
				args = append(args, "--file", "-")
			}
			args = append(args, "--yes", "--output", "json")
			path, method := "", ""
			for p, methods := range spec.Paths {
				for m, candidate := range methods {
					if candidate.OperationID == op.OperationID {
						path, method = p, strings.ToUpper(m)
					}
				}
			}
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.URL.Path != path || r.Method != method {
					t.Errorf("route = %s %s, want %s %s", r.Method, r.URL.Path, method, path)
				}
				if r.Header.Get("CONVAI-API-KEY") == "" {
					t.Error("credential was not forwarded")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer server.Close()
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			stdout, _, err := execute(t, server, strings.NewReader(string(data)), args...)
			if err != nil {
				t.Fatal(err)
			}
			if !called || !strings.Contains(stdout, `"ok": true`) {
				t.Fatalf("called=%v, output=%s", called, stdout)
			}
		})
	}
}

func contains(values []string, name string) bool {
	for _, v := range values {
		if v == name {
			return true
		}
	}
	return false
}

func sampleValue(spec contract.Spec, schema map[string]any) any {
	schema = spec.Resolve(schema)
	if values, ok := schema["enum"].([]any); ok {
		return values[0]
	}
	if values, ok := schema["anyOf"].([]any); ok {
		return sampleValue(spec, values[0].(map[string]any))
	}
	switch schema["type"] {
	case "object":
		value := map[string]any{}
		properties, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		for _, name := range required {
			value[name.(string)] = sampleValue(spec, properties[name.(string)].(map[string]any))
		}
		return value
	case "array":
		return []any{}
	case "boolean":
		return false
	case "integer", "number":
		return float64(1)
	default:
		return testUUID
	}
}

func TestUpdatePreservesClearsAndWorkspace(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Query().Get("workspace_id") != testUUID || body["workspace_id"] != testUUID {
			t.Errorf("workspace not forwarded consistently")
		}
		if value, exists := body["speaking_style_description"]; !exists || value != nil {
			t.Error("explicit null was lost")
		}
		if body["actions_enabled"] != false {
			t.Error("explicit false was lost")
		}
		if value, ok := body["pronunciations_to_delete"].([]any); !ok || len(value) != 0 {
			t.Error("empty collection was lost")
		}
		if _, exists := body["backstory"]; exists {
			t.Error("omitted field was added")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	_, _, err := execute(t, server, strings.NewReader(`{"speaking_style_description":null,"actions_enabled":false,"pronunciations_to_delete":[]}`), "character", "update", testUUID, "--file", "-", "--yes", "--workspace", testUUID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnsafeOrInvalidResourceInputsDoNotCallREST(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
		args        []string
	}{
		{"delete needs confirmation", "", []string{"character", "delete", testUUID}},
		{"update needs confirmation", `{"backstory":"new"}`, []string{"character", "update", testUUID, "--file", "-"}},
		{"unknown update field", `{"made_up_field":true}`, []string{"character", "update", testUUID, "--file", "-", "--yes"}},
		{"empty update", `{}`, []string{"character", "update", testUUID, "--file", "-", "--yes"}},
		{"duplicate aliases", `{"actions_enabled":false,"actionsEnabled":true}`, []string{"character", "update", testUUID, "--file", "-", "--yes"}},
		{"invalid query uuid", "", []string{"character", "version", "list", "not-a-uuid"}},
		{"create explicit invalid null", `{"charName":"Ari","backstory":"A guide.","autoSelectAssets":null}`, []string{"character", "create", "--file", "-"}},
		{"create personal mismatch", `{"charName":"Ari","backstory":"A guide.","workspace_id":"df076c58-43f5-11f1-a81f-42010a080006"}`, []string{"character", "create", "--file", "-", "--workspace", "personal"}},
		{"workspace mismatch", `{"workspace_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`, []string{"character", "update", testUUID, "--file", "-", "--yes", "--workspace", testUUID}},
		{"clone target mismatch", `{"character_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`, []string{"character", "clone", testUUID, "--file", "-"}},
		{"promoting release needs confirmation", `{"make_latest":true}`, []string{"character", "version", "release", testUUID, "1.0", "--file", "-"}},
		{"invalid retrieval mode", "", []string{"kb", "connect", testUUID, testUUID, "--retrieval-mode", "invalid", "--yes"}},
		{"discard requires revision", `{}`, []string{"character", "version", "discard-draft", testUUID, "--file", "-", "--yes"}},
		{"multiple documents", `{} {}`, []string{"function", "list", "--file", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			defer server.Close()
			_, _, err := execute(t, server, strings.NewReader(tc.input), tc.args...)
			if err == nil || called {
				t.Fatalf("error=%v, called=%v", err, called)
			}
		})
	}
}

func TestAssociationCommandsUseOwnerRESTMapping(t *testing.T) {
	t.Parallel()
	for _, group := range []string{"kb", "function-association"} {
		for _, mode := range []string{"connect", "disconnect"} {
			t.Run(group+"/"+mode, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/character/update" || r.URL.Query().Get("character_id") != testUUID {
						t.Error("wrong association owner")
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					collection := "docs"
					if group == "function-association" {
						collection = "functions"
					}
					item := body[collection].([]any)[0].(map[string]any)
					status := "active"
					if mode == "disconnect" {
						status = "inactive"
					}
					if item["id"] != testUUID || item["status"] != status {
						t.Error("wrong association payload")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				}))
				defer server.Close()
				_, _, err := execute(t, server, strings.NewReader(""), group, mode, testUUID, testUUID, "--yes")
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestYAMLResourceInputAndBounds(t *testing.T) {
	t.Parallel()
	a := &application{stdin: strings.NewReader(strings.Repeat("x", (1<<20)+1))}
	if _, err := a.inputObject("-"); err == nil {
		t.Fatal("oversized input accepted")
	}
	path := filepath.Join(t.TempDir(), "body.yaml")
	if err := os.WriteFile(path, []byte("speaking_style: null\nactions_enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := a.inputObject(path)
	if err != nil || value["actions_enabled"] != false {
		t.Fatalf("value=%v, error=%v", value, err)
	}
}

func TestCreateAllowsServerDefaultVoice(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, ok := body["voiceType"]; ok {
			t.Error("client supplied a default voice")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"charID":"new","character_id":"new"}`)
	}))
	defer server.Close()
	if _, _, err := execute(t, server, strings.NewReader(""), "character", "create", "--name", "Ari", "--backstory", "A helpful guide."); err != nil {
		t.Fatal(err)
	}
}

func TestListAllPreservesWorkspaceSortAndOffset(t *testing.T) {
	t.Parallel()
	for _, emptyPage := range []bool{false, true} {
		t.Run(fmt.Sprintf("emptyPage=%v", emptyPage), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("workspace_id") != testUUID || r.URL.Query().Get("sort") != "last_updated" || r.URL.Query().Get("offset") != fmt.Sprint(calls) {
					t.Errorf("wrong pagination context: %s", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				calls++
				if emptyPage && calls == 2 {
					_, _ = io.WriteString(w, `{"items":[],"total":2}`)
					return
				}
				fmt.Fprintf(w, `{"items":[{"character_id":"fixture-%d"}],"total":2}`, calls)
			}))
			defer server.Close()
			stdout, _, err := execute(t, server, strings.NewReader(""), "character", "list", "--all", "--limit", "1", "--sort", "last_updated", "--workspace", testUUID, "--output", "json")
			if calls != 2 {
				t.Fatalf("calls=%d", calls)
			}
			if emptyPage {
				if err == nil {
					t.Fatal("incomplete page accepted")
				}
				return
			}
			if err != nil || !strings.Contains(stdout, "fixture-2") {
				t.Fatalf("output=%s, error=%v", stdout, err)
			}
		})
	}
}
