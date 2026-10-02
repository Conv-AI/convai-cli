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
