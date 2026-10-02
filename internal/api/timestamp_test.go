package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

func TestListPreservesLegacyAndRFC3339Timestamps(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"2026-09-17T20:59:22.109230", "2026-09-17T20:59:22.109230Z", "2026-09-17T20:59:22-07:00"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"items":[{"character_id":"fixture","timestamp":%q}],"total":1}`, value)
		}))
		client, err := New(server.URL, credential.Credential{Type: config.CredentialAPIKey, Value: "01234567890123456789012345678901"}, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		items, _, err := client.ListCharacters(context.Background(), 1, 0)
		server.Close()
		if err != nil || len(items) != 1 || items[0].Timestamp != value {
			t.Fatalf("items=%v, error=%v", items, err)
		}
	}
}
