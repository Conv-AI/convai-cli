package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Conv-AI/convai-cli/internal/api"
	"sigs.k8s.io/yaml"
)

// ReportError keeps automation errors on stderr and returns a stable exit code.
func ReportError(writer io.Writer, format string, err error) int {
	code := 1
	payload := map[string]any{"error": RedactError(err)}
	var response *api.Error
	if errors.As(err, &response) {
		payload["status_code"] = response.StatusCode
		if response.RequestID != "" {
			payload["request_id"] = response.RequestID
		}
		switch response.StatusCode {
		case 401, 403:
			code = 3
		case 404:
			code = 4
		case 409:
			code = 5
		case 422:
			code = 2
		case 429:
			code = 6
		default:
			if response.StatusCode >= 500 {
				code = 7
			}
		}
	}
	payload["exit_code"] = code
	if format == "json" || format == "yaml" {
		data, marshalErr := json.Marshal(payload)
		if marshalErr == nil && format == "yaml" {
			data, marshalErr = yaml.JSONToYAML(data)
		}
		if marshalErr == nil {
			fmt.Fprintln(writer, string(data))
			return code
		}
	}
	fmt.Fprintf(writer, "convai: %s\n", payload["error"])
	return code
}
