package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	contract "github.com/Conv-AI/convai-cli/api"
)

func decodeQuery(values map[string]any, target any) error {
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// Call uses generated SDK methods without re-marshaling patch bodies. Explicit
// null, false and empty collections must not turn into omitted update fields.
func (c *Client) Call(ctx context.Context, operation string, query map[string]any, body []byte) (any, error) {
	if err := contract.ValidateInput(operation, query, body); err != nil {
		return nil, err
	}
	response, err := c.dispatch(ctx, operation, query, body)
	if err != nil {
		return nil, c.safeError(transportError(err))
	}
	defer response.Body.Close()
	const maxResponse = 16 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return nil, c.safeError(transportError(err))
	}
	if len(data) > maxResponse {
		return nil, fmt.Errorf("Convai response exceeds 16 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, c.safeError(responseError(response.StatusCode, data, response))
	}
	if len(data) == 0 {
		return map[string]any{"status": response.StatusCode}, nil
	}
	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("Convai API returned invalid JSON")
	}
	return result, nil
}
