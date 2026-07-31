package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
)

type Client struct {
	generated *characterapi.ClientWithResponses
}

type Error struct {
	StatusCode int
	Message    string
	RequestID  string
}

func (e *Error) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("Convai API returned HTTP %d: %s (request_id=%s)", e.StatusCode, e.Message, e.RequestID)
	}
	return fmt.Sprintf("Convai API returned HTTP %d: %s", e.StatusCode, e.Message)
}

func New(baseURL string, auth credential.Credential, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	editor := func(_ context.Context, request *http.Request) error {
		switch auth.Type {
		case config.CredentialPAT:
			request.Header.Set("Authorization", "Bearer "+auth.Value)
		case config.CredentialAPIKey:
			request.Header.Set("CONVAI-API-KEY", auth.Value)
		default:
			return errors.New("unsupported credential type")
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "convai-cli")
		return nil
	}
	generated, err := characterapi.NewClientWithResponses(
		baseURL,
		characterapi.WithHTTPClient(httpClient),
		characterapi.WithRequestEditorFn(editor),
	)
	if err != nil {
		return nil, fmt.Errorf("create Character API client: %w", err)
	}
	return &Client{generated: generated}, nil
}

func (c *Client) ListCharacters(ctx context.Context, limit, offset int) ([]characterapi.CharacterListItem, int, error) {
	response, err := c.generated.CharacterListWithResponse(ctx, &characterapi.CharacterListParams{
		Limit:  &limit,
		Offset: &offset,
	})
	if err != nil {
		return nil, 0, transportError(err)
	}
	if response.JSON200 == nil {
		return nil, 0, responseError(response.StatusCode(), response.Body, response.HTTPResponse)
	}
	items := []characterapi.CharacterListItem{}
	if response.JSON200.Items != nil {
		items = *response.JSON200.Items
	}
	total := len(items)
	if response.JSON200.Total != nil {
		total = *response.JSON200.Total
	}
	return items, total, nil
}

func (c *Client) GetCharacter(ctx context.Context, characterID string) (characterapi.CharacterResponse, error) {
	response, err := c.generated.CharacterGetWithResponse(ctx, &characterapi.CharacterGetParams{
		CharacterId: characterID,
	})
	if err != nil {
		return characterapi.CharacterResponse{}, transportError(err)
	}
	if response.JSON200 == nil {
		return characterapi.CharacterResponse{}, responseError(
			response.StatusCode(), response.Body, response.HTTPResponse,
		)
	}
	return *response.JSON200, nil
}

func (c *Client) CreateCharacter(
	ctx context.Context,
	request characterapi.CharacterCreate,
) (characterapi.CharacterCreateResponse, error) {
	response, err := c.generated.CharacterCreateWithResponse(ctx, request)
	if err != nil {
		return characterapi.CharacterCreateResponse{}, transportError(err)
	}
	if response.JSON201 == nil {
		return characterapi.CharacterCreateResponse{}, responseError(
			response.StatusCode(), response.Body, response.HTTPResponse,
		)
	}
	return *response.JSON201, nil
}

func transportError(err error) error {
	return fmt.Errorf("connect to Convai API: %w", err)
}

func responseError(statusCode int, body []byte, response *http.Response) error {
	message := http.StatusText(statusCode)
	var payload map[string]any
	if len(body) > 0 && json.Unmarshal(body, &payload) == nil {
		for _, field := range []string{"message", "detail", "error"} {
			if value, ok := payload[field].(string); ok && value != "" {
				message = value
				break
			}
		}
	}
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		message = message[:500]
	}
	requestID := ""
	if response != nil {
		requestID = response.Header.Get("X-Request-ID")
	}
	return &Error{StatusCode: statusCode, Message: message, RequestID: requestID}
}

func DecodeCreateRequest(reader io.Reader, format string) (characterapi.CharacterCreate, error) {
	const maxInputBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(reader, maxInputBytes+1))
	if err != nil {
		return characterapi.CharacterCreate{}, fmt.Errorf("read character input: %w", err)
	}
	if len(body) > maxInputBytes {
		return characterapi.CharacterCreate{}, errors.New("character input exceeds 1 MiB")
	}
	if len(body) == 0 {
		return characterapi.CharacterCreate{}, errors.New("character input is empty")
	}
	var request characterapi.CharacterCreate
	switch format {
	case "json":
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&request)
		if err == nil {
			var extra any
			if extraErr := decoder.Decode(&extra); !errors.Is(extraErr, io.EOF) {
				if extraErr == nil {
					err = errors.New("input must contain exactly one JSON document")
				} else {
					err = extraErr
				}
			}
		}
	case "yaml", "yml":
		err = unmarshalYAML(body, &request)
	default:
		return characterapi.CharacterCreate{}, fmt.Errorf("unsupported input format %q", format)
	}
	if err != nil {
		return characterapi.CharacterCreate{}, fmt.Errorf("decode character input: %w", err)
	}
	return request, nil
}
