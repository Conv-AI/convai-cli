package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"

	yamlv3 "gopkg.in/yaml.v3"
	"sigs.k8s.io/yaml"
)

// DecodeInputObject is shared by typed create and catalog command file inputs.
func DecodeInputObject(reader io.Reader, format string) (map[string]any, error) {
	const maxInput = 1 << 20
	body, err := io.ReadAll(io.LimitReader(reader, maxInput+1))
	if err != nil {
		return nil, errors.New("cannot read input")
	}
	if len(body) > maxInput {
		return nil, errors.New("input exceeds 1 MiB")
	}
	switch format {
	case "yaml", "yml":
		// YAMLToJSONStrict converts only the first document. Check the stream
		// separately so a trailing document cannot be silently discarded.
		stream := yamlv3.NewDecoder(bytes.NewReader(body))
		var document yamlv3.Node
		if err := stream.Decode(&document); err != nil {
			return nil, errors.New("invalid YAML object")
		}
		if err := validateYAMLNumbers(&document); err != nil {
			return nil, err
		}
		if err := stream.Decode(&document); !errors.Is(err, io.EOF) {
			return nil, errors.New("input must contain exactly one document")
		}
		body, err = yaml.YAMLToJSONStrict(body)
		if err != nil {
			return nil, errors.New("invalid YAML object")
		}
	case "json":
	default:
		return nil, errors.New("unsupported input format")
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("input must be a JSON or YAML object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("input must contain exactly one document")
	}
	return result, nil
}

func validateYAMLNumbers(node *yamlv3.Node) error {
	if node.Kind == yamlv3.ScalarNode && (node.Tag == "!!int" || node.Tag == "!!float") {
		var value any
		if err := node.Decode(&value); err != nil {
			return errors.New("unsupported YAML number; use JSON")
		}
		data, err := json.Marshal(value)
		if err != nil {
			return errors.New("unsupported YAML number; use JSON")
		}
		literal := strings.ReplaceAll(node.Value, "_", "")
		var exact *big.Rat
		if node.Tag == "!!int" {
			if integer, ok := new(big.Int).SetString(literal, 0); ok {
				exact = new(big.Rat).SetInt(integer)
			}
		} else {
			exact, _ = new(big.Rat).SetString(literal)
		}
		encoded, ok := new(big.Rat).SetString(string(data))
		if exact == nil || !ok || exact.Cmp(encoded) != 0 {
			return errors.New("YAML number cannot be converted exactly; use JSON")
		}
	}
	for _, child := range node.Content {
		if err := validateYAMLNumbers(child); err != nil {
			return err
		}
	}
	return nil
}
