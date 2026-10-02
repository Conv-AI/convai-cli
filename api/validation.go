package contract

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var validatorsOnce sync.Once
var validatorsErr error
var bodyValidators map[string]*jsonschema.Schema
var queryValidators map[string]*jsonschema.Schema

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external schema references are not allowed")
}

type schemaRegexp struct{ pattern *regexp2.Regexp }

func (r schemaRegexp) MatchString(value string) bool {
	matched, err := r.pattern.MatchString(value)
	return err == nil && matched
}
func (r schemaRegexp) String() string { return r.pattern.String() }
func compileRegexp(value string) (jsonschema.Regexp, error) {
	pattern, err := regexp2.Compile(value, regexp2.ECMAScript|regexp2.Unicode)
	if err != nil {
		return nil, err
	}
	pattern.MatchTimeout = 100 * time.Millisecond
	return schemaRegexp{pattern}, nil
}

func initializeValidators() {
	spec, commands, err := Load()
	if err != nil {
		validatorsErr = err
		return
	}
	var root map[string]any
	if err := json.Unmarshal(Document, &root); err != nil {
		validatorsErr = err
		return
	}
	queries := map[string]any{}
	for _, command := range commands {
		op, _ := spec.Find(command.OperationID)
		properties := map[string]any{}
		required := []any{}
		for _, param := range op.Parameters {
			if param.In != "query" {
				continue
			}
			properties[param.Name] = param.Schema
			if param.Required {
				required = append(required, param.Name)
			}
		}
		queries[command.OperationID] = map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	root["x-cli-queries"] = queries
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseRegexpEngine(compileRegexp)
	compiler.UseLoader(offlineLoader{})
	const location = "https://schemas.convai.local/character.json"
	if err := compiler.AddResource(location, root); err != nil {
		validatorsErr = err
		return
	}
	bodyValidators = map[string]*jsonschema.Schema{}
	queryValidators = map[string]*jsonschema.Schema{}
	for path, methods := range spec.Paths {
		for method, op := range methods {
			if _, ok := queries[op.OperationID]; !ok {
				continue
			}
			queryValidators[op.OperationID], err = compiler.Compile(location + "#/x-cli-queries/" + pointer(op.OperationID))
			if err != nil {
				validatorsErr = err
				return
			}
			if _, ok := op.RequestBody.Content["application/json"]; ok {
				bodyValidators[op.OperationID], err = compiler.Compile(location + "#/paths/" + pointer(path) + "/" + method + "/requestBody/content/application~1json/schema")
				if err != nil {
					validatorsErr = err
					return
				}
			}
		}
	}
}

func pointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

// ValidateInput checks wire schemas only. REST still enforces business policy.
func ValidateInput(id string, query map[string]any, body []byte) error {
	validatorsOnce.Do(initializeValidators)
	if validatorsErr != nil {
		return validatorsErr
	}
	validator, ok := queryValidators[id]
	if !ok {
		return fmt.Errorf("unapproved CLI operation %q", id)
	}
	if query == nil {
		query = map[string]any{}
	}
	if err := validator.Validate(query); err != nil {
		return fmt.Errorf("query does not match the owner contract: %s", validationPaths(err))
	}
	if validator, ok := bodyValidators[id]; ok {
		var value any
		if err := json.Unmarshal(body, &value); err != nil {
			return fmt.Errorf("request must be a JSON object")
		}
		if err := validator.Validate(value); err != nil {
			return fmt.Errorf("request does not match the owner contract: %s", validationPaths(err))
		}
	} else if len(body) > 0 {
		return fmt.Errorf("operation does not accept a body")
	}
	return nil
}

func validationPaths(err error) string {
	validation, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return "invalid schema input"
	}
	paths := map[string]bool{}
	var visit func(*jsonschema.ValidationError)
	visit = func(v *jsonschema.ValidationError) {
		if len(v.Causes) == 0 {
			paths[strings.Join(v.InstanceLocation, "/")] = true
		}
		for _, cause := range v.Causes {
			visit(cause)
		}
	}
	visit(validation)
	result := []string{}
	for path := range paths {
		if path == "" {
			path = "root"
		}
		result = append(result, path)
	}
	slices.Sort(result)
	return strings.Join(result, ", ")
}
