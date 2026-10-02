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
		name string
		call func(*Client) error
	}{
		{"list", func(c *Client) error { _, _, err := c.ListCharacters(context.Background(), 1, 0); return err }},
		{"get", func(c *Client) error { _, err := c.GetCharacter(context.Background(), "fixture"); return err }},
		{"create", func(c *Client) error {
			_, err := c.CreateCharacter(context.Background(), characterapi.CharacterCreate{CharName: "Ari", Backstory: "A guide."})
			return err
		}},
		{"catalog", func(c *Client) error {
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
			{"malformed", 200, func() io.Reader { return strings.NewReader(`{"`) }, "invalid JSON"},
			{"null", 200, func() io.Reader { return strings.NewReader(`null`) }, "invalid JSON"},
			{"empty", 200, func() io.Reader { return strings.NewReader("") }, "invalid JSON"},
			{"oversized success", 200, func() io.Reader { return strings.NewReader(strings.Repeat("x", maxResponseBytes+1)) }, "exceeds 16 MiB"},
			{"oversized denial", 403, func() io.Reader { return strings.NewReader(strings.Repeat("x", maxResponseBytes+1)) }, "exceeds 16 MiB"},
			{"read failure", 200, func() io.Reader { return failedReader{} }, "interrupted response"},
			{"redacted denial", 403, func() io.Reader { return strings.NewReader(`{"message":"denied ` + secret + `"}`) }, "[REDACTED]"},
		} {
			t.Run(command.name+"/"+tc.name, func(t *testing.T) {
				body := &trackedBody{Reader: tc.body()}
				calls := 0
				client, err := New("https://api2-stg.convai.com", credential.Credential{Type: config.CredentialAPIKey, Value: secret}, &http.Client{
					Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						calls++
						return &http.Response{StatusCode: tc.status, Header: http.Header{"X-Request-Id": {"request-" + secret}}, Body: body}, nil
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
				if tc.status == 403 {
					var failure *Error
					if !errors.As(err, &failure) || failure.StatusCode != 403 || failure.RequestID != "request-[REDACTED]" {
						t.Fatalf("lost HTTP status/request ID: %v", err)
					}
				}
			})
		}
	}
}
