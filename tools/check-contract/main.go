package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	contract "github.com/Conv-AI/convai-cli/api"
	"github.com/Conv-AI/convai-cli/internal/config"
)

func main() {
	if len(os.Args) != 2 {
		fail(fmt.Errorf("usage: check-contract HTTPS_OPENAPI_URL"))
	}
	endpoint := os.Args[1]
	// Validate the origin separately because OpenAPI is served at a path.
	if len(endpoint) < len("/openapi.json") || endpoint[len(endpoint)-len("/openapi.json"):] != "/openapi.json" {
		fail(fmt.Errorf("expected /openapi.json endpoint"))
	}
	if _, err := config.NormalizeAPIURL(endpoint[:len(endpoint)-len("/openapi.json")]); err != nil {
		fail(err)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(endpoint)
	if err != nil {
		fail(fmt.Errorf("fetch upstream OpenAPI failed"))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fail(fmt.Errorf("upstream OpenAPI returned HTTP %d", response.StatusCode))
	}
	const maxBytes = 16 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		fail(fmt.Errorf("upstream OpenAPI is unreadable or oversized"))
	}
	if err := contract.CheckUpstream(data); err != nil {
		fail(err)
	}
	fmt.Println("Reviewed CLI operations match the staging owner contract")
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
