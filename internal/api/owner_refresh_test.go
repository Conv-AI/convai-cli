package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

func TestOwnerRefreshPreservesRequestsWithoutIdempotencyHeader(t *testing.T) {
	for _, tc := range []struct {
		operation string
		query     map[string]any
		body      string
	}{
		{"character_create", nil, `{"charName":"Ari","backstory":"A guide."}`},
		{"character_clone", nil, `{"character_id":"11111111-1111-4111-8111-111111111111"}`},
		{"character_version_create", map[string]any{"character_id": "11111111-1111-4111-8111-111111111111"}, `{"version":"1.0"}`},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			calls := 0
			client, err := New("https://owner.example", credential.Credential{
				Type: config.CredentialAPIKey, Value: "01234567890123456789012345678901",
			}, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Idempotency-Key") != "" {
					t.Fatal("optional operation ID was added to an existing request")
				}
				data, err := io.ReadAll(r.Body)
				if err != nil || string(data) != tc.body {
					t.Fatalf("body=%s, error=%v", data, err)
				}
				return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"message":"fixture"}`))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Call(context.Background(), tc.operation, tc.query, []byte(tc.body))
			var apiErr *Error
			if calls != 1 || !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
				t.Fatalf("calls=%d, error=%v", calls, err)
			}
		})
	}
}
