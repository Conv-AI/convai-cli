package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("interrupted response") }

func TestAllCommandPathsShareResponseSafety(t *testing.T) {
	const secret = "01234567890123456789012345678901"
	for _, command := range []struct {
		name   string
		status int
		valid  string
		call   func(*Client) error
	}{
		{"list", 200, `{"items":[]}`, func(c *Client) error { _, _, err := c.ListCharacters(context.Background(), 1, 0); return err }},
		{"get", 200, `{"user_id":"fixture","character_id":"fixture","character_name":null,"voice_type":null,"language_code":"en","description":null,"model_type":null,"is_narrative_driven":false,"timestamp":"2026-10-01T00:00:00","memory_settings":{},"character_traits":{},"metadata_filter":{}}`, func(c *Client) error {
			_, err := c.GetCharacter(context.Background(), "df076c58-43f5-11f1-a81f-42010a080006")
			return err
		}},
		{"create", 201, `{"character_id":"fixture","charID":"fixture","workspace_id":"fixture","revision_id":"fixture"}`, func(c *Client) error {
			_, err := c.CreateCharacter(context.Background(), characterapi.CharacterCreate{CharName: "Ari", Backstory: "A guide."})
			return err
		}},
		{"catalog", 200, `{"languages":[]}`, func(c *Client) error {
			_, err := c.Call(context.Background(), "get_available_languages_tts_get_available_languages_get", nil, nil)
			return err
		}},
	} {
		for _, tc := range []struct {
			name   string
			status int
			body   func() io.Reader
			want   string
		}{
			{"valid", 200, func() io.Reader { return strings.NewReader(`{}`) }, ""},
			{"schema invalid", 200, func() io.Reader { return strings.NewReader(`{}`) }, "owner contract"},
			{"wrong encoding", 200, func() io.Reader { return strings.NewReader(`{}`) }, "undeclared response encoding"},
			{"queued is not completion", 202, func() io.Reader { return strings.NewReader(`{"status":"queued"}`) }, "undeclared success response"},
			{"wrong success status", 299, func() io.Reader { return strings.NewReader(`{}`) }, "undeclared success response"},
			{"malformed", 200, func() io.Reader { return strings.NewReader(`{"`) }, "invalid JSON"},
			{"null", 200, func() io.Reader { return strings.NewReader(`null`) }, "owner contract"},
			{"empty", 200, func() io.Reader { return strings.NewReader("") }, "invalid JSON"},
			{"oversized success", 200, func() io.Reader { return strings.NewReader(strings.Repeat("x", maxResponseBytes+1)) }, "exceeds 16 MiB"},
			{"oversized denial", 403, func() io.Reader { return strings.NewReader(strings.Repeat("x", maxResponseBytes+1)) }, "exceeds 16 MiB"},
			{"read failure", 200, func() io.Reader { return failedReader{} }, "interrupted response"},
			{"redacted denial", 403, func() io.Reader { return strings.NewReader(`{"message":"denied ` + secret + `"}`) }, "[REDACTED]"},
		} {
			t.Run(command.name+"/"+tc.name, func(t *testing.T) {
				status := tc.status
				if status == 200 {
					status = command.status
				}
				body := &trackedBody{Reader: tc.body()}
				if tc.name == "valid" || tc.name == "wrong encoding" {
					body.Reader = strings.NewReader(command.valid)
				}
				if tc.name == "schema invalid" && command.name == "list" {
					body.Reader = strings.NewReader(`{"items":false}`)
				}
				calls := 0
				client, err := New("https://api2-stg.convai.com", credential.Credential{Type: config.CredentialAPIKey, Value: secret}, &http.Client{
					Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						calls++
						contentType := "application/json; charset=utf-8"
						if tc.name == "wrong encoding" {
							contentType = "text/html"
						}
						return &http.Response{StatusCode: status, Header: http.Header{"X-Request-Id": {"request-" + secret}, "Content-Type": {contentType}}, Body: body}, nil
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				err = command.call(client)
				if calls != 1 || !body.closed {
					t.Fatalf("calls=%d, closed=%v", calls, body.closed)
				}
				if tc.want == "" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), secret) {
					t.Fatalf("response error=%v", err)
				}
				if status == 403 || status == 202 || status == 299 {
					var failure *Error
					if !errors.As(err, &failure) || failure.StatusCode != status || failure.RequestID != "request-[REDACTED]" {
						t.Fatalf("lost HTTP status/request ID: %v", err)
					}
				}
			})
		}
	}
}
