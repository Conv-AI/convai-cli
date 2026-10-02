package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"gopkg.in/yaml.v3"
)

func TestListOutputKeepsUnknownTotalUnknown(t *testing.T) {
	for _, total := range []*int{nil, new(5)} {
		for _, format := range []Format{JSON, Table} {
			var writer bytes.Buffer
			if err := CharacterList(&writer, format, []characterapi.CharacterListItem{}, total); err != nil {
				t.Fatal(err)
			}
			if format == JSON {
				var result map[string]any
				if err := json.Unmarshal(writer.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if total == nil && result["total"] != nil || total != nil && result["total"] != float64(*total) {
					t.Fatalf("output=%s", writer.String())
				}
			} else if total == nil && !strings.Contains(writer.String(), "Total: unknown") {
				t.Fatalf("unknown total displayed as a count: %s", writer.String())
			}
		}
	}
}

func TestCharacterYAMLUsesAPIFieldNames(t *testing.T) {
	t.Parallel()
	name := "Ari"
	voice := "voice-1"
	var writer bytes.Buffer
	err := Character(&writer, YAML, characterapi.CharacterResponse{
		CharacterId:   "character-1",
		CharacterName: &name,
		LanguageCode:  "en",
		VoiceType:     &voice,
	})
	if err != nil {
		t.Fatalf("Character: %v", err)
	}
	value := writer.String()
	for _, field := range []string{"character_id:", "character_name:", "voice_type:"} {
		if !strings.Contains(value, field) {
			t.Errorf("YAML is missing %q:\n%s", field, value)
		}
	}
	if strings.Contains(value, "characterid:") {
		t.Fatalf("YAML used Go field name:\n%s", value)
	}
}

func TestResourceOutputPreservesLargeIntegers(t *testing.T) {
	for _, format := range []Format{JSON, YAML, Table} {
		for _, number := range []string{"9007199254740993", "18446744073709551616", "0.12345678901234567890123456789", "1e309", strings.Repeat("9", 400)} {
			var writer bytes.Buffer
			if err := Resource(&writer, format, map[string]any{"external_id": json.Number(number)}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(writer.String(), number) {
				t.Fatalf("rounded %s output: %s", format, writer.String())
			}
			if format == YAML {
				var document yaml.Node
				if err := yaml.Unmarshal(writer.Bytes(), &document); err != nil {
					t.Fatal(err)
				}
				value := document.Content[0].Content[1]
				if value.Tag != "!!int" && value.Tag != "!!float" {
					t.Fatalf("number became YAML string: %s", writer.String())
				}
			}
		}
	}
}
