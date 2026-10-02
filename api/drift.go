package contract

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// CheckUpstream checks only reviewed operations, not unrelated internal routes.
func CheckUpstream(data []byte) error {
	pinned, commands, err := Load()
	if err != nil {
		return err
	}
	var upstream Spec
	if err := json.Unmarshal(data, &upstream); err != nil {
		return fmt.Errorf("decode upstream OpenAPI: %w", err)
	}
	for _, command := range commands {
		old, _ := pinned.Find(command.OperationID)
		current, ok := upstream.Find(command.OperationID)
		if !ok {
			return fmt.Errorf("upstream removed operation %s", command.OperationID)
		}
		oldPath, oldMethod := pinned.route(command.OperationID)
		path, method := upstream.route(command.OperationID)
		if path != oldPath || method != oldMethod {
			return fmt.Errorf("upstream changed route for %s", command.OperationID)
		}
		if !current.Metadata.Exposed || !reflect.DeepEqual(old.Metadata.Tools, current.Metadata.Tools) {
			return fmt.Errorf("upstream changed tool approval, scopes, or safety for %s", command.OperationID)
		}
		if current.RequestBody.Required && !old.RequestBody.Required {
			return fmt.Errorf("upstream now requires a request body for %s", command.OperationID)
		}
		oldParams := map[string]Parameter{}
		for _, parameter := range old.Parameters {
			oldParams[parameter.In+":"+parameter.Name] = parameter
		}
		for _, parameter := range current.Parameters {
			previous, exists := oldParams[parameter.In+":"+parameter.Name]
			if parameter.Required && (!exists || !previous.Required) {
				return fmt.Errorf("upstream added required parameter %s to %s", parameter.Name, command.OperationID)
			}
			if exists {
				if err := compatibleInput(pinned, upstream, previous.Schema, parameter.Schema, parameter.Name); err != nil {
					return fmt.Errorf("%s: %w", command.OperationID, err)
				}
				delete(oldParams, parameter.In+":"+parameter.Name)
			}
		}
		if len(oldParams) > 0 {
			return fmt.Errorf("upstream removed a supported parameter from %s", command.OperationID)
		}
		for encoding, previous := range old.RequestBody.Content {
			currentBody, exists := current.RequestBody.Content[encoding]
			if !exists {
				return fmt.Errorf("upstream removed %s request encoding from %s", encoding, command.OperationID)
			}
			if err := compatibleInput(pinned, upstream, previous.Schema, currentBody.Schema, "body"); err != nil {
				return fmt.Errorf("%s: %w", command.OperationID, err)
			}
		}
		for status, previous := range old.Responses {
			if !strings.HasPrefix(status, "2") {
				continue
			}
			response, exists := current.Responses[status]
			if !exists {
				return fmt.Errorf("upstream removed successful response %s from %s", status, command.OperationID)
			}
			for encoding, content := range previous.Content {
				updated, exists := response.Content[encoding]
				if !exists {
					return fmt.Errorf("upstream removed response encoding from %s", command.OperationID)
				}
				if err := compatibleOutput(pinned, upstream, content.Schema, updated.Schema, "response"); err != nil {
					return fmt.Errorf("%s: %w", command.OperationID, err)
				}
			}
		}
	}
	return nil
}

func (s Spec) route(id string) (string, string) {
	for path, methods := range s.Paths {
		for method, op := range methods {
			if op.OperationID == id {
				return path, method
			}
		}
	}
	return "", ""
}

func compatibleInput(oldSpec, newSpec Spec, old, current map[string]any, path string) error {
	old, current = inputSchema(oldSpec, old), inputSchema(newSpec, current)
	if old == nil || current == nil {
		return fmt.Errorf("unresolved schema at %s", path)
	}
	for _, key := range []string{"pattern", "const", "type", "format", "not", "if", "then", "else", "dependentSchemas", "dependentRequired", "uniqueItems", "additionalProperties"} {
		// The owner artifact adds a scalar type to nullable query schemas for
		// generators. The anyOf branches remain the authority in that case.
		if key == "type" && (old["anyOf"] != nil || current["anyOf"] != nil) {
			continue
		}
		if current[key] != nil && !reflect.DeepEqual(old[key], current[key]) {
			return fmt.Errorf("upstream changed %s at %s", key, path)
		}
	}
	for _, key := range []string{"minimum", "exclusiveMinimum", "minLength", "minItems", "minProperties"} {
		if value, ok := current[key].(float64); ok {
			previous, had := old[key].(float64)
			if !had || value > previous {
				return fmt.Errorf("upstream tightened %s at %s", key, path)
			}
		}
	}
	for _, key := range []string{"maximum", "exclusiveMaximum", "maxLength", "maxItems", "maxProperties"} {
		if value, ok := current[key].(float64); ok {
			previous, had := old[key].(float64)
			if !had || value < previous {
				return fmt.Errorf("upstream tightened %s at %s", key, path)
			}
		}
	}
	if values, ok := current["enum"].([]any); ok {
		previous, had := old["enum"].([]any)
		if !had {
			return fmt.Errorf("upstream restricted values at %s", path)
		}
		for _, value := range previous {
			if !slices.ContainsFunc(values, func(v any) bool { return reflect.DeepEqual(value, v) }) {
				return fmt.Errorf("upstream removed a value at %s", path)
			}
		}
	}
	previousRequired, _ := old["required"].([]any)
	currentRequired, _ := current["required"].([]any)
	for _, name := range currentRequired {
		if !slices.Contains(previousRequired, name) {
			return fmt.Errorf("upstream added required field %v at %s", name, path)
		}
	}
	previousProperties, _ := old["properties"].(map[string]any)
	currentProperties, _ := current["properties"].(map[string]any)
	for name, property := range previousProperties {
		updated, exists := currentProperties[name]
		if !exists {
			return fmt.Errorf("upstream removed field %s at %s", name, path)
		}
		if err := compatibleInput(oldSpec, newSpec, property.(map[string]any), updated.(map[string]any), path+"/"+name); err != nil {
			return err
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		previous, had := old[key].([]any)
		updated, has := current[key].([]any)
		if had != has || len(previous) != len(updated) {
			return fmt.Errorf("upstream changed %s at %s", key, path)
		}
		for i, branch := range previous {
			if err := compatibleInput(oldSpec, newSpec, branch.(map[string]any), updated[i].(map[string]any), path+"/"+key); err != nil {
				return err
			}
		}
	}
	if items, ok := old["items"].(map[string]any); ok {
		updated, ok := current["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("upstream removed item schema at %s", path)
		}
		if err := compatibleInput(oldSpec, newSpec, items, updated, path+"/items"); err != nil {
			return err
		}
	}
	return nil
}

// JSON Schema applies $ref siblings as an intersection, not an override.
func inputSchema(spec Spec, schema map[string]any) map[string]any {
	if _, ok := schema["$ref"].(string); !ok {
		return schema
	}
	base := spec.Resolve(schema)
	if base == nil {
		return nil
	}
	siblings := map[string]any{}
	for key, value := range schema {
		if key != "$ref" && key != "title" && key != "description" {
			siblings[key] = value
		}
	}
	if len(siblings) == 0 {
		return base
	}
	return map[string]any{"allOf": []any{base, siblings}}
}

func compatibleOutput(oldSpec, newSpec Spec, old, current map[string]any, path string) error {
	old, current = inputSchema(oldSpec, old), inputSchema(newSpec, current)
	if old == nil || current == nil {
		return fmt.Errorf("unresolved output schema at %s", path)
	}
	if !reflect.DeepEqual(old["type"], current["type"]) {
		return fmt.Errorf("upstream changed output type at %s", path)
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		previous, had := old[key].([]any)
		updated, has := current[key].([]any)
		if had != has || len(previous) != len(updated) {
			return fmt.Errorf("upstream changed output %s at %s", key, path)
		}
		for i, branch := range previous {
			if err := compatibleOutput(oldSpec, newSpec, branch.(map[string]any), updated[i].(map[string]any), fmt.Sprintf("%s/%s/%d", path, key, i)); err != nil {
				return err
			}
		}
	}
	previousRequired, _ := old["required"].([]any)
	currentRequired, _ := current["required"].([]any)
	for _, name := range previousRequired {
		if !slices.Contains(currentRequired, name) {
			return fmt.Errorf("upstream made required output field %v optional at %s", name, path)
		}
	}
	oldProperties, _ := old["properties"].(map[string]any)
	newProperties, _ := current["properties"].(map[string]any)
	for name, property := range oldProperties {
		updated, exists := newProperties[name]
		if !exists {
			return fmt.Errorf("upstream removed output field %s at %s", name, path)
		}
		if err := compatibleOutput(oldSpec, newSpec, property.(map[string]any), updated.(map[string]any), path+"/"+name); err != nil {
			return err
		}
	}
	if items, ok := old["items"].(map[string]any); ok {
		updated, ok := current["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("upstream changed output items at %s", path)
		}
		return compatibleOutput(oldSpec, newSpec, items, updated, path+"/items")
	}
	return nil
}
