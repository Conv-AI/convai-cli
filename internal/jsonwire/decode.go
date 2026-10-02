package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Decode preserves numeric lexemes in untyped maps and requires one document.
func Decode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("input must contain exactly one JSON document")
	}
	return nil
}
