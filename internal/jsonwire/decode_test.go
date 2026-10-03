package jsonwire

import (
	"encoding/json"
	"testing"
)

func TestDecodePreservesNumbersAndRejectsTrailingDocuments(t *testing.T) {
	for _, value := range []string{"9007199254740993", "18446744073709551616", "0.12345678901234567890123456789"} {
		var decoded map[string]any
		if err := Decode([]byte(`{"number":`+value+`}`), &decoded); err != nil {
			t.Fatal(err)
		}
		if got, ok := decoded["number"].(json.Number); !ok || got.String() != value {
			t.Fatalf("number=%v, want exact %s", decoded["number"], value)
		}
	}
	for _, input := range []string{`{} {}`, `{} null`, `{} false`, `{} 9007199254740993`, `{} malformed`} {
		var decoded any
		if err := Decode([]byte(input), &decoded); err == nil {
			t.Errorf("accepted trailing document: %s", input)
		}
	}
}

func TestDecodeStrictRejectsDuplicatesUnknownFieldsAndTrailingData(t *testing.T) {
	type record struct {
		A string          `json:"a"`
		B json.RawMessage `json:"b"`
	}
	var accepted record
	if err := DecodeStrict([]byte(`{"a":"x","b":{"a":1,"c":[{"a":2},{"a":3}]}}`), &accepted); err != nil || accepted.A != "x" {
		t.Fatalf("valid record = %+v, %v", accepted, err)
	}
	for _, input := range []string{
		`{"a":"x","a":"y"}`,
		`{"a":"x","b":{"c":1,"c":2}}`,
		`{"a":"x","b":[{"c":1,"c":2}]}`,
		`{"a":"x","z":1}`,
		`{"a":"x"}}`,
		`{"a":"x"}]`,
		`{"a":"x"} {}`,
		`{"a":"x"`,
	} {
		var decoded record
		if err := DecodeStrict([]byte(input), &decoded); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}
