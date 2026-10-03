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

// DecodeStrict is Decode for trusted-format records: it also rejects unknown
// struct fields and duplicate object members anywhere in the document.
func DecodeStrict(data []byte, target any) error {
	if err := rejectDuplicateMembers(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("input must contain exactly one JSON document")
	}
	return nil
}

func rejectDuplicateMembers(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	type frame struct {
		object    bool
		expectKey bool
		keys      map[string]bool
	}
	var stack []*frame
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if top != nil && top.object && top.expectKey {
			if key, ok := token.(string); ok {
				if top.keys[key] {
					return errors.New("input contains a duplicate object member")
				}
				top.keys[key] = true
				top.expectKey = false
				continue
			}
		}
		switch token {
		case json.Delim('{'):
			stack = append(stack, &frame{object: true, expectKey: true, keys: map[string]bool{}})
			continue
		case json.Delim('['):
			stack = append(stack, &frame{})
			continue
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
		}
		// A completed value inside an object is followed by the next key.
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].expectKey = true
		}
	}
}
