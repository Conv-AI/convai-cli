package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

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
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("input must be a JSON or YAML object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("input must contain exactly one document")
	}
	return result, nil
}
