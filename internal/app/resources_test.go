package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
			wantQuery := map[string][]string{}
			for index, name := range definition.Arguments {
				value := fmt.Sprintf("%08x-aaaa-4aaa-8aaa-aaaaaaaaaaaa", index+1)
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
				} else {
					wantQuery[name] = []string{value}
				}
			}
			for _, parameter := range op.Parameters {
				if parameter.Name == "workspace_id" {
					wantQuery[parameter.Name] = []string{testUUID}
				}
			}
			if _, ok := wantQuery["workspace_id"]; ok {
				args = append(args, "--workspace", testUUID)
			}
			if definition.OperationID == "character_version_diff" {
				args = append(args, "--view", "semantic")
				wantQuery["view"] = []string{"semantic"}
			}
			if definition.OperationID == "get_available_voices_tts_get_available_voices_get" {
				args = append(args, "--lang-code", "en", "--gender", "NEUTRAL", "--provider", "fixture-provider")
				wantQuery["lang_code"] = []string{"en"}
				wantQuery["gender"] = []string{"NEUTRAL"}
				wantQuery["provider"] = []string{"fixture-provider"}
			}
			if definition.FileRequired || len(body) > 0 {
				args = append(args, "--file", "-")
			}
			args = append(args, "--yes", "--output", "json")
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			// Expected injection is independent of the file sent to the command.
			if content, ok := op.RequestBody.Content["application/json"]; ok {
				properties, _ := spec.Resolve(content.Schema)["properties"].(map[string]any)
				if _, supported := properties["workspace_id"]; supported && wantQuery["workspace_id"] != nil {
					body["workspace_id"] = testUUID
				}
			}
			if definition.OperationID == "character_version_create" {
				body["make_latest"] = false
			}
			if definition.OperationID == "character_version_deprecate" {
				body["deprecated"] = true
			}
			path, method := "", ""
			for p, item := range spec.Paths {
				for m, candidate := range item.Operations {
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
				if !reflect.DeepEqual(map[string][]string(r.URL.Query()), wantQuery) {
					t.Errorf("query=%v, want=%v", r.URL.Query(), wantQuery)
				}
				if len(op.RequestBody.Content) > 0 {
					var actual map[string]any
					if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
						t.Error(err)
					} else if !reflect.DeepEqual(actual, body) {
						t.Errorf("body=%v, want=%v", actual, body)
					}
				}
				writeOwnerResponse(t, w, definition.OperationID)
			}))
			defer server.Close()
			stdout, _, err := execute(t, server, strings.NewReader(string(data)), args...)
			if err != nil {
				t.Fatal(err)
			}
			if !called || !json.Valid([]byte(stdout)) {
				t.Fatalf("called=%v, output=%s", called, stdout)
			}
			if op.Metadata.Tools[0].Safety.Destructive {
				called = false
				withoutConfirmation := []string{}
				for _, argument := range args {
					if argument != "--yes" {
						withoutConfirmation = append(withoutConfirmation, argument)
					}
				}
				_, _, err := execute(t, server, strings.NewReader(string(data)), withoutConfirmation...)
				if err == nil || called {
					t.Fatalf("unconfirmed owner-destructive command reached REST: error=%v, called=%v", err, called)
				}
			}
		})
	}
}

func writeOwnerResponse(t *testing.T, w http.ResponseWriter, id string) {
	t.Helper()
	spec, _, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	op, _ := spec.Find(id)
	status := "200"
	if _, ok := op.Responses[status]; !ok {
		status = "201"
	}
	value := sampleValue(spec, op.Responses[status].Content["application/json"].Schema)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.ValidateResponse(id, status, data); err != nil {
		t.Error(err)
	}
	w.Header().Set("Content-Type", "application/json")
	if status == "201" {
		w.WriteHeader(http.StatusCreated)
	}
	_, _ = w.Write(data)
}

func listPageFixture(t *testing.T, raw string) string {
	t.Helper()
	var page map[string]any
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatal(err)
	}
	for _, value := range page["items"].([]any) {
		item := value.(map[string]any)
		for key, field := range map[string]any{"character_name": "fixture", "voice_type": nil, "language_code": "en", "last_interacted_with": "2026-10-01T00:00:00", "timestamp": "2026-10-01T00:00:00"} {
			item[key] = field
		}
	}
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
	if value, exists := schema["const"]; exists {
		return value
	}
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
		writeOwnerResponse(t, w, "character_update")
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
					if item["id"] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || item["status"] != status {
						t.Error("wrong association payload")
					}
					writeOwnerResponse(t, w, "character_update")
				}))
				defer server.Close()
				_, _, err := execute(t, server, strings.NewReader(""), group, mode, testUUID, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "--yes")
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := execute(t, server, strings.NewReader(""), group, mode, testUUID, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"); err == nil {
					t.Fatal("association mutation accepted without confirmation")
				}
			})
		}
	}
}

func TestConfirmationTargetsAreSpecificAndEscaped(t *testing.T) {
	got := confirmationTargets(map[string]any{"character_id": "character", "workspace_id": "workspace", "backstory": "private"}, map[string]any{"version": "1.0", "function_id": "function\n", "description": "private"})
	want := `, character_id="character", function_id="function\n", version="1.0"`
	if got != want {
		t.Fatalf("confirmation targets=%q, want=%q", got, want)
	}
}

func TestVersionCommandsSendIntentExplicitly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		command, field, input string
		value                 bool
	}{
		{"release", "make_latest", "", false},
		{"release", "make_latest", `{"make_latest":true}`, true},
		{"deprecate", "deprecated", "", true},
		{"deprecate", "deprecated", `{"deprecated":false}`, false},
	} {
		t.Run(tc.command+"/"+fmt.Sprint(tc.value), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if value, exists := body[tc.field]; !exists || value != tc.value {
					t.Errorf("explicit %s = %v (present=%v), want %v", tc.field, value, exists, tc.value)
				}
				id := "character_version_deprecate"
				if tc.command == "release" {
					id = "character_version_create"
				}
				writeOwnerResponse(t, w, id)
			}))
			defer server.Close()
			args := []string{"character", "version", tc.command, testUUID, "1.0", "--yes", "--output", "json"}
			if tc.input != "" {
				args = append(args, "--file", "-")
			}
			if _, _, err := execute(t, server, strings.NewReader(tc.input), args...); err != nil {
				t.Fatal(err)
			}
		})
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
	for _, args := range [][]string{
		{"character", "create"},
		{"character", "update", testUUID, "--yes"},
	} {
		if err := os.WriteFile(path, []byte("charName: Ari\nbackstory: A guide.\n---\ncharName: Other\n"), 0600); err != nil {
			t.Fatal(err)
		}
		called := false
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		_, _, err := execute(t, server, strings.NewReader(""), append(args, "--file", path)...)
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "exactly one document") || called {
			t.Fatalf("args=%v, error=%v, called=%v", args, err, called)
		}
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
		_, _ = io.WriteString(w, `{"charID":"new","character_id":"new","workspace_id":"fixture","revision_id":"fixture"}`)
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
				_, _ = io.WriteString(w, listPageFixture(t, fmt.Sprintf(`{"items":[{"character_id":"fixture-%d"}],"total":2}`, calls)))
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
