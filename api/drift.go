package contract

import (
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/Conv-AI/convai-cli/internal/jsonwire"
)

// CheckUpstream is a reviewed-contract drift gate, not a semantic compatibility
// solver. Any change to a selected wire contract requires an owner-pin refresh.
func CheckUpstream(data []byte) error {
	pinned, commands, err := Load()
	if err != nil {
		return err
	}
	var upstream Spec
	if err := jsonwire.Decode(data, &upstream); err != nil {
		return fmt.Errorf("decode upstream OpenAPI: %w", err)
	}
	var oldRoot, newRoot map[string]any
	if err := jsonwire.Decode(Document, &oldRoot); err != nil {
		return err
	}
	if err := jsonwire.Decode(data, &newRoot); err != nil {
		return err
	}
	for _, key := range []string{"openapi", "jsonSchemaDialect"} {
		if !reflect.DeepEqual(oldRoot[key], newRoot[key]) {
			return fmt.Errorf("upstream changed %s", key)
		}
	}
	for _, command := range commands {
		old, _ := pinned.Find(command.OperationID)
		current, ok := upstream.Find(command.OperationID)
		if !ok {
			return fmt.Errorf("upstream removed or cannot resolve operation %s", command.OperationID)
		}
		oldPath, oldMethod := pinned.route(command.OperationID)
		path, method := upstream.route(command.OperationID)
		if path != oldPath || method != oldMethod {
			return fmt.Errorf("upstream changed or duplicated route for %s", command.OperationID)
		}
		if err := compatibleSecurity(pinned, upstream, old, current); err != nil {
			return fmt.Errorf("%s: %w", command.OperationID, err)
		}
		previous, err := operationSnapshot(oldRoot, old)
		if err != nil {
			return fmt.Errorf("%s: %w", command.OperationID, err)
		}
		updated, err := operationSnapshot(newRoot, current)
		if err != nil {
			return fmt.Errorf("%s: %w", command.OperationID, err)
		}
		if !reflect.DeepEqual(previous, updated) {
			return fmt.Errorf("upstream changed reviewed contract for %s; refresh the immutable owner pin after review", command.OperationID)
		}
	}
	return nil
}

type contractSnapshot struct {
	Operation  map[string]any
	References map[string]any
}

// Compare opaque schema keywords, serialization options, response headers,
// errors, and all owner metadata rather than maintaining a keyword allowlist.
// Traverse the reference graph once; recursive schemas cannot recurse forever.
func operationSnapshot(root map[string]any, operation Operation) (contractSnapshot, error) {
	value := map[string]any{}
	for key, field := range operation.Raw {
		switch key {
		case "parameters", "security", "servers", "summary", "description", "tags", "externalDocs":
			continue
		}
		value[key] = field
	}
	parameters := map[string]any{}
	for _, parameter := range operation.Parameters {
		parameters[parameter.In+":"+parameter.Name] = parameter.Raw
	}
	value["parameters"] = parameters
	servers := []map[string]any{{"url": "/"}}
	if operation.Servers != nil && len(*operation.Servers) > 0 {
		servers = *operation.Servers
	}
	value["servers"] = servers
	result := contractSnapshot{Operation: value, References: map[string]any{}}
	queue := []string{}
	if err := collectReferences(value, &queue); err != nil {
		return contractSnapshot{}, err
	}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if _, visited := result.References[ref]; visited {
			continue
		}
		resolved, err := localReference(root, ref)
		if err != nil {
			return contractSnapshot{}, err
		}
		result.References[ref] = resolved
		if err := collectReferences(resolved, &queue); err != nil {
			return contractSnapshot{}, err
		}
	}
	return result, nil
}

func collectReferences(value any, queue *[]string) error {
	switch value := value.(type) {
	case map[string]any:
		for key, field := range value {
			switch key {
			case "$dynamicRef", "$recursiveRef":
				return fmt.Errorf("unsupported contract reference keyword %s", key)
			case "$ref":
				ref, ok := field.(string)
				if !ok {
					return fmt.Errorf("contract reference must be a string")
				}
				*queue = append(*queue, ref)
			default:
				if err := collectReferences(field, queue); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, field := range value {
			if err := collectReferences(field, queue); err != nil {
				return err
			}
		}
	}
	return nil
}

func localReference(root map[string]any, ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/components/") {
		return nil, fmt.Errorf("unsupported contract reference %q", ref)
	}
	pointer, err := url.PathUnescape(strings.TrimPrefix(ref, "#/"))
	if err != nil {
		return nil, fmt.Errorf("invalid contract reference %q", ref)
	}
	var result any = root
	for _, token := range strings.Split(pointer, "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := result.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unresolved contract reference %q", ref)
		}
		result, ok = object[token]
		if !ok || result == nil {
			return nil, fmt.Errorf("unresolved contract reference %q", ref)
		}
	}
	return result, nil
}

func (s Spec) effectiveSecurity(operation Operation) []SecurityRequirement {
	if operation.Security != nil {
		return *operation.Security
	}
	return s.Security
}

func compatibleSecurity(oldSpec, newSpec Spec, old, current Operation) error {
	previous := normalizedSecurity(oldSpec.effectiveSecurity(old))
	updated := normalizedSecurity(newSpec.effectiveSecurity(current))
	if len(previous) != len(updated) {
		return fmt.Errorf("upstream changed authentication alternatives")
	}
	for _, requirement := range previous {
		if !slices.ContainsFunc(updated, func(value SecurityRequirement) bool {
			return reflect.DeepEqual(requirement, value)
		}) {
			return fmt.Errorf("upstream changed authentication requirements")
		}
		for name := range requirement {
			oldScheme, err := oldSpec.securityScheme(name, map[string]bool{})
			if err != nil {
				return err
			}
			newScheme, err := newSpec.securityScheme(name, map[string]bool{})
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(oldScheme, newScheme) {
				return fmt.Errorf("upstream changed authentication scheme %s", name)
			}
		}
	}
	return nil
}

// Alternative order and scope order do not change OpenAPI authorization.
func normalizedSecurity(requirements []SecurityRequirement) []SecurityRequirement {
	result := []SecurityRequirement{}
	for _, requirement := range requirements {
		value := SecurityRequirement{}
		for name, scopes := range requirement {
			ordered := append([]string{}, scopes...)
			slices.Sort(ordered)
			value[name] = slices.Compact(ordered)
		}
		if !slices.ContainsFunc(result, func(existing SecurityRequirement) bool {
			return reflect.DeepEqual(existing, value)
		}) {
			result = append(result, value)
		}
	}
	return result
}

func (s Spec) securityScheme(name string, seen map[string]bool) (map[string]any, error) {
	scheme, exists := s.Components.SecuritySchemes[name]
	if !exists || seen[name] {
		return nil, fmt.Errorf("unresolved authentication scheme %s", name)
	}
	ref, isRef := scheme["$ref"].(string)
	if !isRef {
		return scheme, nil
	}
	const prefix = "#/components/securitySchemes/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, fmt.Errorf("unsupported authentication scheme reference %s", name)
	}
	seen[name] = true
	resolved, err := s.securityScheme(strings.TrimPrefix(ref, prefix), seen)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for key, value := range resolved {
		result[key] = value
	}
	for key, value := range scheme {
		if key != "$ref" {
			result[key] = value
		}
	}
	return result, nil
}

func (s Spec) route(id string) (string, string) {
	foundPath, foundMethod := "", ""
	for path, item := range s.Paths {
		for method, op := range item.Operations {
			if op.OperationID == id {
				if foundPath != "" {
					return "", ""
				}
				foundPath, foundMethod = path, method
			}
		}
	}
	return foundPath, foundMethod
}
