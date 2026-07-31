package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Conv-AI/convai-cli/internal/characterapi"
)

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
