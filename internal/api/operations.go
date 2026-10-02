package api

import (
	"bytes"
	"context"
	"encoding/json"

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
	var result any
	err = c.decodeResponse(operation, response, &result)
	return result, err
}
