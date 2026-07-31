package api

import "sigs.k8s.io/yaml"

func unmarshalYAML(data []byte, target any) error {
	return yaml.UnmarshalStrict(data, target)
}
