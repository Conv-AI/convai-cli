package contract

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Conv-AI/convai-cli/internal/jsonwire"

	"gopkg.in/yaml.v3"
)

//go:embed character.openapi.json
var Document []byte

//go:embed catalog.yaml
var catalogDocument []byte

//go:embed owner.lock.json
var lockDocument []byte

type Command struct {
	OperationID   string   `yaml:"operation_id"`
	Group         string   `yaml:"group"`
	Name          string   `yaml:"command"`
	Arguments     []string `yaml:"arguments"`
	BodyArguments []string `yaml:"body_arguments"`
	FileRequired  bool     `yaml:"file_required"`
}

type Parameter struct {
	Ref      string         `json:"$ref"`
	Name     string         `json:"name"`
	In       string         `json:"in"`
	Required bool           `json:"required"`
	Schema   map[string]any `json:"schema"`
	Raw      map[string]any `json:"-"`
}

func (p *Parameter) UnmarshalJSON(data []byte) error {
	type plain Parameter
	if err := jsonwire.Decode(data, (*plain)(p)); err != nil {
		return err
	}
	return jsonwire.Decode(data, &p.Raw)
}

type PathItem struct {
	Ref        string
	Parameters []Parameter
	Servers    *[]map[string]any
	Operations map[string]Operation
}

func (p *PathItem) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if ref, exists := fields["$ref"]; exists {
		if err := json.Unmarshal(ref, &p.Ref); err != nil {
			return err
		}
	}
	p.Operations = map[string]Operation{}
	if parameters, exists := fields["parameters"]; exists {
		if err := json.Unmarshal(parameters, &p.Parameters); err != nil {
			return err
		}
	}
	if servers, exists := fields["servers"]; exists {
		if err := jsonwire.Decode(servers, &p.Servers); err != nil {
			return err
		}
	}
	for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
		if value, exists := fields[method]; exists {
			var operation Operation
			if err := json.Unmarshal(value, &operation); err != nil {
				return err
			}
			p.Operations[method] = operation
		}
	}
	return nil
}

type Operation struct {
	OperationID string                 `json:"operationId"`
	Summary     string                 `json:"summary"`
	Parameters  []Parameter            `json:"parameters"`
	Security    *[]SecurityRequirement `json:"security"`
	Servers     *[]map[string]any      `json:"servers"`
	RequestBody struct {
		Required bool `json:"required"`
		Content  map[string]struct {
			Schema map[string]any `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema map[string]any `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
	Metadata Metadata       `json:"x-convai-mcp"`
	Raw      map[string]any `json:"-"`
}

func (o *Operation) UnmarshalJSON(data []byte) error {
	type plain Operation
	if err := jsonwire.Decode(data, (*plain)(o)); err != nil {
		return err
	}
	return jsonwire.Decode(data, &o.Raw)
}

type SecurityRequirement map[string][]string

type Metadata struct {
	Exposed bool `json:"exposed"`
	Tools   []struct {
		Name           string   `json:"name"`
		RequiredScopes []string `json:"requiredScopes"`
		Safety         struct {
			Destructive bool `json:"destructiveHint"`
		} `json:"safety"`
	} `json:"tools"`
}

type Spec struct {
	Paths      map[string]PathItem   `json:"paths"`
	Security   []SecurityRequirement `json:"security"`
	Servers    []map[string]any      `json:"servers"`
	Components struct {
		Schemas         map[string]map[string]any `json:"schemas"`
		SecuritySchemes map[string]map[string]any `json:"securitySchemes"`
		Parameters      map[string]Parameter      `json:"parameters"`
	} `json:"components"`
}

func Load() (Spec, []Command, error) {
	var lock struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(lockDocument, &lock); err != nil {
		return Spec{}, nil, err
	}
	digest := sha256.Sum256(Document)
	if hex.EncodeToString(digest[:]) != lock.SHA256 {
		return Spec{}, nil, fmt.Errorf("owner contract checksum mismatch")
	}
	var spec Spec
	if err := jsonwire.Decode(Document, &spec); err != nil {
		return Spec{}, nil, err
	}
	var catalog struct {
		Version  int       `yaml:"version"`
		Commands []Command `yaml:"operations"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(catalogDocument))
	decoder.KnownFields(true)
	if err := decoder.Decode(&catalog); err != nil {
		return Spec{}, nil, err
	}
	if catalog.Version != 1 {
		return Spec{}, nil, fmt.Errorf("unsupported command catalog")
	}
	seen := map[string]bool{}
	for _, command := range catalog.Commands {
		operation, ok := spec.Find(command.OperationID)
		key := command.Group + " " + command.Name
		if !ok || !operation.Metadata.Exposed || len(operation.Metadata.Tools) == 0 || seen[key] {
			return Spec{}, nil, fmt.Errorf("unapproved or duplicate CLI operation %q", command.OperationID)
		}
		seen[key] = true
	}
	return spec, catalog.Commands, nil
}

// CommandScopes returns the sorted union of owner-required scopes for the
// curated command catalog. With readOnly, it includes only operations whose
// every required scope is a read scope, so browser login can request the
// least privilege that still serves the selected commands.
func CommandScopes(spec Spec, commands []Command, readOnly bool) []string {
	seen := map[string]bool{}
	for _, command := range commands {
		operation, ok := spec.Find(command.OperationID)
		if !ok {
			continue
		}
		for _, tool := range operation.Metadata.Tools {
			writes := slices.ContainsFunc(tool.RequiredScopes, func(scope string) bool {
				return !strings.HasSuffix(scope, ":read")
			})
			if readOnly && writes {
				continue
			}
			for _, scope := range tool.RequiredScopes {
				seen[scope] = true
			}
		}
	}
	scopes := make([]string, 0, len(seen))
	for scope := range seen {
		scopes = append(scopes, scope)
	}
	slices.Sort(scopes)
	return scopes
}

func (s Spec) Find(id string) (Operation, bool) {
	for _, item := range s.Paths {
		for _, operation := range item.Operations {
			if operation.OperationID == id {
				if item.Ref != "" {
					return Operation{}, false
				}
				var ok bool
				operation.Parameters, ok = s.effectiveParameters(item.Parameters, operation.Parameters)
				if !ok {
					return Operation{}, false
				}
				if operation.Servers == nil {
					operation.Servers = item.Servers
					if operation.Servers == nil {
						operation.Servers = &s.Servers
					}
				}
				return operation, true
			}
		}
	}
	return Operation{}, false
}

func (s Spec) effectiveParameters(inherited, declared []Parameter) ([]Parameter, bool) {
	result := []Parameter{}
	positions := map[string]int{}
	for _, parameters := range [][]Parameter{inherited, declared} {
		for _, parameter := range parameters {
			seen := map[string]bool{}
			for parameter.Ref != "" {
				const prefix = "#/components/parameters/"
				if !strings.HasPrefix(parameter.Ref, prefix) || seen[parameter.Ref] {
					return nil, false
				}
				seen[parameter.Ref] = true
				var exists bool
				parameter, exists = s.Components.Parameters[strings.TrimPrefix(parameter.Ref, prefix)]
				if !exists {
					return nil, false
				}
			}
			key := parameter.In + ":" + parameter.Name
			if index, exists := positions[key]; exists {
				result[index] = parameter
			} else {
				positions[key] = len(result)
				result = append(result, parameter)
			}
		}
	}
	return result, true
}

func (s Spec) Resolve(schema map[string]any) map[string]any {
	if ref, ok := schema["$ref"].(string); ok {
		return s.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	return schema
}
