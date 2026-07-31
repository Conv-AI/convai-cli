package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"gopkg.in/yaml.v3"
)

type Format string

const (
	Table Format = "table"
	JSON  Format = "json"
	YAML  Format = "yaml"
)

func Parse(value string) (Format, error) {
	format := Format(value)
	switch format {
	case Table, JSON, YAML:
		return format, nil
	default:
		return "", errors.New("output must be table, json, or yaml")
	}
}

func Structured(writer io.Writer, format Format, value any) error {
	switch format {
	case JSON:
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(value)
	case YAML:
		encoder := yaml.NewEncoder(writer)
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(value)
	default:
		return errors.New("structured output requires json or yaml")
	}
}

func CharacterList(writer io.Writer, format Format, items []characterapi.CharacterListItem, total int) error {
	if format != Table {
		return Structured(writer, format, map[string]any{
			"items": items,
			"total": total,
		})
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME\tLANGUAGE\tVOICE\tUPDATED")
	for _, item := range items {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\n",
			item.CharacterId,
			stringValue(item.CharacterName),
			item.LanguageCode,
			stringValue(item.VoiceType),
			item.Timestamp.UTC().Format(time.RFC3339),
		)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(writer, "\n%d total\n", total)
	return nil
}

func Character(writer io.Writer, format Format, value characterapi.CharacterResponse) error {
	if format != Table {
		return Structured(writer, format, value)
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	rows := [][2]string{
		{"ID", value.CharacterId},
		{"Name", stringValue(value.CharacterName)},
		{"Voice", stringValue(value.VoiceType)},
		{"Language", value.LanguageCode},
		{"Model", stringValue(value.ModelType)},
		{"Backstory", stringValue(value.Backstory)},
	}
	for _, row := range rows {
		fmt.Fprintf(table, "%s\t%s\n", row[0], row[1])
	}
	return table.Flush()
}

func CharacterCreated(
	writer io.Writer,
	format Format,
	value characterapi.CharacterCreateResponse,
) error {
	if format != Table {
		return Structured(writer, format, value)
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	fmt.Fprintf(table, "ID\t%s\n", value.CharacterId)
	if value.Message != nil {
		fmt.Fprintf(table, "Message\t%s\n", *value.Message)
	}
	return table.Flush()
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
