package contract

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

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
	Name     string         `json:"name"`
	In       string         `json:"in"`
	Required bool           `json:"required"`
	Schema   map[string]any `json:"schema"`
}

type Operation struct {
	OperationID string      `json:"operationId"`
	Summary     string      `json:"summary"`
	Parameters  []Parameter `json:"parameters"`
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
	Metadata struct {
		Exposed bool `json:"exposed"`
		Tools   []struct {
			Name           string   `json:"name"`
			RequiredScopes []string `json:"requiredScopes"`
			Safety         struct {
				Destructive bool `json:"destructiveHint"`
			} `json:"safety"`
		} `json:"tools"`
	} `json:"x-convai-mcp"`
}

type Spec struct {
	Paths      map[string]map[string]Operation `json:"paths"`
	Components struct {
		Schemas map[string]map[string]any `json:"schemas"`
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
	if err := json.Unmarshal(Document, &spec); err != nil {
		return Spec{}, nil, err
	}
	var catalog struct {
		Version  int       `yaml:"version"`
		Commands []Command `yaml:"operations"`
	}
	if err := yaml.Unmarshal(catalogDocument, &catalog); err != nil {
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

func (s Spec) Find(id string) (Operation, bool) {
	for _, methods := range s.Paths {
		for _, operation := range methods {
			if operation.OperationID == id {
				return operation, true
			}
		}
	}
	return Operation{}, false
}

func (s Spec) Resolve(schema map[string]any) map[string]any {
	if ref, ok := schema["$ref"].(string); ok {
		return s.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	return schema
}
