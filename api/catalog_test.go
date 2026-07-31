package api_test

import (
	"encoding/json"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

type catalog struct {
	Version    int `yaml:"version"`
	Operations []struct {
		OperationID    string   `yaml:"operation_id"`
		Group          string   `yaml:"group"`
		Command        string   `yaml:"command"`
		Stability      string   `yaml:"stability"`
		Destructive    bool     `yaml:"destructive"`
		RequiredScopes []string `yaml:"required_scopes"`
	} `yaml:"operations"`
}

type generatorConfig struct {
	OutputOptions struct {
		IncludeOperationIDs []string `yaml:"include-operation-ids"`
	} `yaml:"output-options"`
}

func TestCatalogReferencesStableOpenAPIOperationsAndGeneratorSelection(t *testing.T) {
	t.Parallel()
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	readJSON(t, "character.openapi.json", &spec)

	var reviewed catalog
	readYAML(t, "catalog.yaml", &reviewed)
	if reviewed.Version != 1 {
		t.Fatalf("catalog version = %d", reviewed.Version)
	}
	var generator generatorConfig
	readYAML(t, "oapi-codegen.yaml", &generator)

	available := map[string]bool{}
	for _, methods := range spec.Paths {
		for _, operation := range methods {
			if operation.OperationID != "" {
				available[operation.OperationID] = true
			}
		}
	}
	selected := map[string]bool{}
	for _, operationID := range generator.OutputOptions.IncludeOperationIDs {
		selected[operationID] = true
	}
	seen := map[string]bool{}
	for _, operation := range reviewed.Operations {
		if seen[operation.OperationID] {
			t.Fatalf("duplicate operation %q", operation.OperationID)
		}
		seen[operation.OperationID] = true
		if !available[operation.OperationID] {
			t.Errorf("catalog operation %q is absent from OpenAPI", operation.OperationID)
		}
		if !selected[operation.OperationID] {
			t.Errorf("catalog operation %q is absent from generator config", operation.OperationID)
		}
		if operation.Group == "" || operation.Command == "" || operation.Stability == "" {
			t.Errorf("catalog operation %q has incomplete CLI metadata", operation.OperationID)
		}
		if len(operation.RequiredScopes) == 0 {
			t.Errorf("catalog operation %q has no required scope", operation.OperationID)
		}
	}
	if len(selected) != len(seen) {
		t.Fatalf("generator selects %d operations but catalog contains %d", len(selected), len(seen))
	}
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func readYAML(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
