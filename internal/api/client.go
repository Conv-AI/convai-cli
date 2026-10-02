package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	contract "github.com/Conv-AI/convai-cli/api"
	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"github.com/google/uuid"
)

type Client struct {
	generated *characterapi.Client
	secret    string
	workspace string
	sort      string
	spec      contract.Spec
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
	baseURL, err := config.NormalizeAPIURL(baseURL)
	if err != nil {
		return nil, err
	}
	parsed, err := credential.Parse(auth.Value)
	if err != nil {
		return nil, err
	}
	if parsed.Type != auth.Type {
		return nil, errors.New("credential type does not match token")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	httpClient = &copyClient
	editor := func(_ context.Context, request *http.Request) error {
		switch auth.Type {
		case config.CredentialPAT, config.CredentialOAuth:
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
	generated, err := characterapi.NewClient(
		baseURL,
		characterapi.WithHTTPClient(httpClient),
		characterapi.WithRequestEditorFn(editor),
	)
	if err != nil {
		return nil, fmt.Errorf("create Character API client: %w", err)
	}
	spec, _, err := contract.Load()
	if err != nil {
		return nil, err
	}
	return &Client{generated: generated, secret: auth.Value, spec: spec}, nil
}

func (c *Client) WithWorkspace(value string) *Client {
	copy := *c
	copy.workspace = value
	return &copy
}

func (c *Client) WithSort(value string) *Client { copy := *c; copy.sort = value; return &copy }

func (c *Client) safeError(err error) error {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		copy := *apiErr
		copy.Message = strings.ReplaceAll(copy.Message, c.secret, "[REDACTED]")
		copy.RequestID = strings.ReplaceAll(copy.RequestID, c.secret, "[REDACTED]")
		if len(copy.Message) > 500 {
			copy.Message = copy.Message[:500]
		}
		return &copy
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.secret, "[REDACTED]"))
}

func (c *Client) ListCharacters(ctx context.Context, limit, offset int) ([]characterapi.CharacterListItem, *int, error) {
	workspace, err := c.workspaceUUID()
	if err != nil {
		return nil, nil, err
	}
	params := &characterapi.CharacterListParams{
		Limit:       &limit,
		Offset:      &offset,
		WorkspaceId: workspace,
	}
	if c.sort != "" {
		sort := characterapi.CharacterListParamsSort(c.sort)
		params.Sort = &sort
	}
	response, err := c.generated.CharacterList(ctx, params)
	if err != nil {
		return nil, nil, c.safeError(transportError(err))
	}
	var result characterapi.PageCharacterListItem
	if err := c.decodeResponse("character_list", response, &result); err != nil {
		return nil, nil, err
	}
	items := []characterapi.CharacterListItem{}
	if result.Items != nil {
		items = *result.Items
	}
	return items, result.Total, nil
}

func (c *Client) GetCharacter(ctx context.Context, characterID string) (characterapi.CharacterResponse, error) {
	workspace, err := c.workspaceUUID()
	if err != nil {
		return characterapi.CharacterResponse{}, err
	}
	response, err := c.generated.CharacterGet(ctx, &characterapi.CharacterGetParams{
		CharacterId: characterID,
		WorkspaceId: workspace,
	})
	if err != nil {
		return characterapi.CharacterResponse{}, c.safeError(transportError(err))
	}
	var result characterapi.CharacterResponse
	err = c.decodeResponse("character_get", response, &result)
	return result, err
}

func (c *Client) CreateCharacter(
	ctx context.Context,
	request characterapi.CharacterCreate,
) (characterapi.CharacterCreateResponse, error) {
	if c.workspace != "" {
		if request.WorkspaceId != nil && *request.WorkspaceId != c.workspace {
			return characterapi.CharacterCreateResponse{}, errors.New("input workspace conflicts with selected workspace")
		}
		request.WorkspaceId = &c.workspace
	}
	response, err := c.generated.CharacterCreate(ctx, request)
	if err != nil {
		return characterapi.CharacterCreateResponse{}, c.safeError(transportError(err))
	}
	var result characterapi.CharacterCreateResponse
	err = c.decodeResponse("character_create", response, &result)
	return result, err
}

const maxResponseBytes = 16 << 20

// Every command uses the same bounded reader and redacted HTTP error path,
// including the typed convenience methods used by auth and character commands.
func (c *Client) decodeResponse(operationID string, response *http.Response, target any) error {
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return c.safeError(transportError(err))
	}
	if len(data) > maxResponseBytes {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return c.safeError(&Error{StatusCode: response.StatusCode, Message: "response exceeds 16 MiB", RequestID: response.Header.Get("X-Request-ID")})
		}
		return errors.New("Convai response exceeds 16 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.safeError(responseError(response.StatusCode, data, response))
	}
	operation, ok := c.spec.Find(operationID)
	if !ok {
		return errors.New("unapproved response operation")
	}
	if _, declared := operation.Responses[strconv.Itoa(response.StatusCode)]; !declared {
		return c.safeError(&Error{StatusCode: response.StatusCode, Message: "undeclared success response for " + operationID, RequestID: response.Header.Get("X-Request-ID")})
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) || json.Unmarshal(data, target) != nil {
		return errors.New("Convai API returned invalid JSON response")
	}
	return nil
}

func (c *Client) workspaceUUID() (*uuid.UUID, error) {
	if c.workspace == "" {
		return nil, nil
	}
	value, err := uuid.Parse(c.workspace)
	if err != nil {
		return nil, errors.New("workspace must be a UUID")
	}
	return &value, nil
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
