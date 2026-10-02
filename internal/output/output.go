package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

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

func Resource(writer io.Writer, format Format, value any) error {
	if format != Table {
		return Structured(writer, format, value)
	}
	if object, ok := value.(map[string]any); ok {
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
		for _, key := range keys {
			data, err := json.Marshal(object[key])
			if err != nil {
				return err
			}
			fmt.Fprintf(table, "%s\t%s\n", key, data)
		}
		return table.Flush()
	}
	return Structured(writer, JSON, value)
}

func Structured(writer io.Writer, format Format, value any) error {
	switch format {
	case JSON:
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(value)
	case YAML:
		jsonData, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var document yaml.Node
		if err := yaml.Unmarshal(jsonData, &document); err != nil {
			return err
		}
		// Emit the JSON syntax tree without decoding scalars through floats.
		// YAML's resolver otherwise tags very large JSON integers as floats.
		var prepare func(*yaml.Node)
		prepare = func(node *yaml.Node) {
			// JSON strings are quoted. An unquoted scalar that YAML resolved
			// as a string is a numeric literal beyond its resolver's range.
			if node.Kind == yaml.ScalarNode && node.Style == 0 && node.Tag == "!!str" {
				node.Tag = "!!float"
			}
			if node.Tag == "!!float" && !strings.ContainsAny(node.Value, ".eE") {
				node.Tag = "!!int"
			}
			node.Style = 0
			for _, child := range node.Content {
				prepare(child)
			}
		}
		prepare(&document)
		yamlData, err := yaml.Marshal(&document)
		if err != nil {
			return err
		}
		_, err = writer.Write(yamlData)
		return err
	default:
		return errors.New("structured output requires json or yaml")
	}
}

func CharacterList(writer io.Writer, format Format, items []characterapi.CharacterListItem, total *int) error {
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
			item.Timestamp,
		)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if total == nil {
		fmt.Fprintln(writer, "\nTotal: unknown")
	} else {
		fmt.Fprintf(writer, "\n%d total\n", *total)
	}
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
