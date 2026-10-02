package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/api"
)

func TestStructuredErrorsPreserveHTTPStatusAndExitCode(t *testing.T) {
	t.Parallel()
	for status, want := range map[int]int{401: 3, 403: 3, 404: 4, 409: 5, 422: 2, 429: 6, 502: 7} {
		var stderr bytes.Buffer
		code := ReportError(&stderr, "json", fmt.Errorf("request failed: %w", &api.Error{StatusCode: status, Message: "Denied", RequestID: "request-123"}))
		var value map[string]any
		if err := json.Unmarshal(stderr.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if code != want || value["status_code"] != float64(status) || value["request_id"] != "request-123" {
			t.Fatalf("code=%d, response=%v", code, value)
		}
	}
}
