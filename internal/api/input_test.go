package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	contract "github.com/Conv-AI/convai-cli/api"
	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

func TestTypedCommandsRejectInvalidInputWithoutRequests(t *testing.T) {
	const secret = "01234567890123456789012345678901"
	get := func(id string) func(*Client) error {
		return func(c *Client) error { _, err := c.GetCharacter(context.Background(), id); return err }
	}
	list := func(limit, offset int, sort string) func(*Client) error {
		return func(c *Client) error {
			_, _, err := c.WithSort(sort).ListCharacters(context.Background(), limit, offset)
			return err
		}
	}
	create := func(request characterapi.CharacterCreate) func(*Client) error {
		return func(c *Client) error { _, err := c.CreateCharacter(context.Background(), request); return err }
	}
	for _, tc := range []struct {
		name string
		call func(*Client) error
	}{
		{"empty ID", get("")},
		{"malformed ID", get("not-a-uuid")},
		{"invalid version", get("df076c58-43f5-11f1-a81f-42010a080006-01.2")},
		{"zero limit", list(0, 0, "")},
		{"excessive limit", list(501, 0, "")},
		{"negative offset", list(1, -1, "")},
		{"unknown sort", list(1, 0, "unknown")},
		{"empty name", create(characterapi.CharacterCreate{Backstory: "A guide."})},
		{"empty backstory", create(characterapi.CharacterCreate{CharName: "Ari"})},
		{"long name", create(characterapi.CharacterCreate{CharName: strings.Repeat("x", 101), Backstory: "A guide."})},
		{"invalid listing", create(characterapi.CharacterCreate{CharName: "Ari", Backstory: "A guide.", Listing: new("invalid")})},
		{"secret in invalid map key", create(characterapi.CharacterCreate{CharName: "Ari", Backstory: "A guide.", MetadataFilter: &map[string]characterapi.CharacterCreate_MetadataFilter_AdditionalProperties{secret: {}}})},
		{"invalid get Workspace", func(c *Client) error { return get("df076c58-43f5-11f1-a81f-42010a080006")(c.WithWorkspace("invalid")) }},
		{"invalid list Workspace", func(c *Client) error { return list(1, 0, "")(c.WithWorkspace("invalid")) }},
		{"conflicting create Workspace", func(c *Client) error {
			return create(characterapi.CharacterCreate{CharName: "Ari", Backstory: "A guide.", WorkspaceId: new("other")})(c.WithWorkspace("df076c58-43f5-11f1-a81f-42010a080006"))
		}},
		{"catalog secret in invalid key", func(c *Client) error {
			_, err := c.Call(context.Background(), "character_update", nil, []byte(`{"character_id":"df076c58-43f5-11f1-a81f-42010a080006","metadata_filter":{"`+secret+`":false}}`))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client, err := New("https://api2-stg.convai.com", credential.Credential{Type: config.CredentialAPIKey, Value: secret}, &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return nil, errors.New("unexpected outbound request")
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.call(client); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("validation error=%v", err)
			}
			if calls != 0 {
				t.Fatalf("invalid input made %d outbound requests", calls)
			}
		})
	}
}

func TestTypedGetPreservesOwnerVersionSelectors(t *testing.T) {
	const id = "df076c58-43f5-11f1-a81f-42010a080006"
	for _, suffix := range []string{"", "-latest", "-draft", "-0.0", "-1.2", "-1.2.3"} {
		t.Run(suffix, func(t *testing.T) {
			calls := 0
			client, err := New("https://api2-stg.convai.com", credential.Credential{Type: config.CredentialAPIKey, Value: "01234567890123456789012345678901"}, &http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.Query().Get("character_id") != id+suffix {
						t.Fatalf("selector rewritten: %s", r.URL.RawQuery)
					}
					return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"message":"fixture not found"}`))}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GetCharacter(context.Background(), id+suffix)
			var apiErr *Error
			if calls != 1 || !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
				t.Fatalf("calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestInputDecodersRejectTrailingYAMLDocuments(t *testing.T) {
	for _, trailing := range []string{"---\ncharName: Other\n", "---\n", "---\nmalformed: [\n"} {
		body := "charName: Ari\nbackstory: A guide.\n" + trailing
		if _, err := DecodeInputObject(strings.NewReader(body), "yaml"); err == nil {
			t.Errorf("object decoder accepted trailing YAML: %q", trailing)
		}
		if _, err := DecodeCreateRequest(strings.NewReader(body), "yaml"); err == nil {
			t.Errorf("typed decoder accepted trailing YAML: %q", trailing)
		}
	}
}

func TestInputValidationDoesNotRoundFractionalIntegers(t *testing.T) {
	for _, number := range []string{"1.0000000000000000001", "2.0000000000000000001"} {
		if err := contract.ValidateInput("character_create", nil, []byte(`{"charName":"Ari","backstory":"A guide.","curiosity_level":`+number+`}`)); err == nil {
			t.Errorf("fractional integer passed validation: %s", number)
		}
	}
}

func TestYAMLRejectsNumbersThatWouldBeRounded(t *testing.T) {
	for _, number := range []string{"18446744073709551616", "0.12345678901234567890123456789"} {
		if _, err := DecodeInputObject(strings.NewReader("metadata_filter:\n  external_id: "+number+"\n"), "yaml"); err == nil || !strings.Contains(err.Error(), "use JSON") {
			t.Fatalf("unsupported precision %s: error=%v", number, err)
		}
		if _, err := DecodeInputObject(strings.NewReader(`{"number":`+number+`}`), "json"); err != nil {
			t.Fatalf("JSON exact number rejected: %v", err)
		}
	}
}
