package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	contract "github.com/Conv-AI/convai-cli/api"
	"github.com/Conv-AI/convai-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"sigs.k8s.io/yaml"
)

func (a *application) addResourceCommands(root *cobra.Command) {
	for _, definition := range a.commands {
		if slices.Contains([]string{"character_list", "character_get", "character_create"}, definition.OperationID) {
			continue
		}
		parent := root
		for _, group := range strings.Fields(definition.Group) {
			var existing *cobra.Command
			for _, child := range parent.Commands() {
				if child.Name() == group {
					existing = child
					break
				}
			}
			if existing == nil {
				existing = &cobra.Command{Use: group, Short: "Manage Convai " + group}
				parent.AddCommand(existing)
			}
			parent = existing
		}
		op, _ := a.spec.Find(definition.OperationID)
		parent.AddCommand(a.resourceCommand(definition, op))
	}
	root.AddCommand(a.associationCommands("kb", "docs", "document_id"), a.associationCommands("function-association", "functions", "function_id"))
}

func (a *application) resourceCommand(definition contract.Command, operation contract.Operation) *cobra.Command {
	var file string
	var yes bool
	queryFlags := map[string]*string{}
	use := definition.Name
	for _, name := range definition.Arguments {
		use += " " + strings.ToUpper(name)
	}
	command := &cobra.Command{
		Use: use, Short: operation.Summary, Args: cobra.ExactArgs(len(definition.Arguments)),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			if definition.FileRequired && file == "" {
				return errors.New("--file is required; use - for stdin")
			}
			body := map[string]any{}
			if file != "" {
				body, err = a.inputObject(file)
				if err != nil {
					return err
				}
			}
			// Release and deprecate express an explicit action, not a mutable
			// server default. Promotion still requires separate confirmation.
			if operation.OperationID == "character_version_create" {
				if _, exists := body["make_latest"]; !exists {
					body["make_latest"] = false
				}
			}
			if operation.OperationID == "character_version_deprecate" {
				if _, exists := body["deprecated"]; !exists {
					body["deprecated"] = true
				}
			}
			query := map[string]any{}
			for i, name := range definition.Arguments {
				if slices.Contains(definition.BodyArguments, name) {
					if err := setField(body, name, args[i]); err != nil {
						return err
					}
				} else {
					if existing, ok := body[name]; ok && existing != args[i] {
						return fmt.Errorf("input %s conflicts with selected target", name)
					}
					query[name] = args[i]
				}
			}
			for _, parameter := range operation.Parameters {
				if parameter.In != "query" || parameter.Name == "workspace_id" {
					continue
				}
				flag := strings.ReplaceAll(parameter.Name, "_", "-")
				if cmd.Flags().Changed(flag) {
					value := *queryFlags[parameter.Name]
					var parsed any = value
					if parameter.Schema["type"] == "integer" {
						n, err := strconv.Atoi(value)
						if err != nil {
							return fmt.Errorf("--%s must be an integer", flag)
						}
						parsed = float64(n)
					}
					query[parameter.Name] = parsed
				}
			}
			_, _, profile, err := a.profile()
			if err != nil {
				return err
			}
			if err := a.applyWorkspace(operation, query, body, profile.WorkspaceID); err != nil {
				return err
			}
			var data []byte
			if len(operation.RequestBody.Content) > 0 {
				data, err = json.Marshal(body)
				if err != nil {
					return err
				}
			}
			if err := contract.ValidateInput(operation.OperationID, query, data); err != nil {
				return err
			}
			destructive := operation.Metadata.Tools[0].Safety.Destructive
			if operation.OperationID == "character_version_create" && body["make_latest"] == true {
				destructive = true
			}
			if destructive {
				if err := a.confirm(cmd, yes, query, body); err != nil {
					return err
				}
			}
			client, err := a.apiClient()
			if err != nil {
				return err
			}
			result, err := client.Call(cmd.Context(), operation.OperationID, query, data)
			if err != nil {
				return err
			}
			return output.Resource(cmd.OutOrStdout(), format, result)
		},
	}
	if len(operation.RequestBody.Content) > 0 {
		command.Flags().StringVarP(&file, "file", "f", "", "JSON or YAML body; use - for stdin")
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "confirm destructive changes")
	for _, parameter := range operation.Parameters {
		if parameter.In != "query" || parameter.Name == "workspace_id" || slices.Contains(definition.Arguments, parameter.Name) {
			continue
		}
		queryFlags[parameter.Name] = command.Flags().String(strings.ReplaceAll(parameter.Name, "_", "-"), "", "filter: "+parameter.Name)
	}
	return command
}

func (a *application) applyWorkspace(operation contract.Operation, query, body map[string]any, selected string) error {
	if selected == "" {
		if a.workspaceFlag == "personal" && body["workspace_id"] != nil && body["workspace_id"] != "" {
			return errors.New("input workspace conflicts with explicit personal selection")
		}
		return nil
	}
	supported := false
	for _, parameter := range operation.Parameters {
		if parameter.In == "query" && parameter.Name == "workspace_id" {
			query["workspace_id"] = selected
			supported = true
		}
	}
	if content, ok := operation.RequestBody.Content["application/json"]; ok {
		properties, _ := a.spec.Resolve(content.Schema)["properties"].(map[string]any)
		if _, ok := properties["workspace_id"]; ok {
			if err := setField(body, "workspace_id", selected); err != nil {
				return err
			}
			supported = true
		}
	}
	if !supported {
		// Catalog discovery is global. Never silently drop Workspace selection
		// from a resource operation that would otherwise target the account.
		if operation.OperationID != "get_available_voices_tts_get_available_voices_get" && operation.OperationID != "get_available_languages_tts_get_available_languages_get" {
			return errors.New("operation does not support Workspace selection")
		}
	}
	if existing, ok := body["workspace_id"]; ok && existing != nil && existing != selected {
		return errors.New("input workspace conflicts with selected workspace")
	}
	return nil
}

func setField(body map[string]any, name string, value string) error {
	if existing, ok := body[name]; ok && existing != value {
		return fmt.Errorf("input %s conflicts with selected %s", name, name)
	}
	body[name] = value
	return nil
}

func (a *application) inputObject(path string) (map[string]any, error) {
	reader := a.stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open input: %w", err)
		}
		defer file.Close()
		reader = file
	}
	const maxInput = 1 << 20
	body, err := io.ReadAll(io.LimitReader(reader, maxInput+1))
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	if len(body) > maxInput {
		return nil, errors.New("input exceeds 1 MiB")
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".yaml" || extension == ".yml" {
		body, err = yaml.YAMLToJSONStrict(body)
		if err != nil {
			return nil, errors.New("invalid YAML object")
		}
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("input must be a JSON or YAML object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("input must contain exactly one document")
	}
	return result, nil
}

func (a *application) confirm(cmd *cobra.Command, yes bool, targets ...map[string]any) error {
	if yes {
		return nil
	}
	file, ok := a.stdin.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return errors.New("destructive operation requires --yes when stdin is not a terminal")
	}
	_, name, profile, err := a.profile()
	if err != nil {
		return err
	}
	workspace := profile.WorkspaceID
	if workspace == "" {
		workspace = "personal"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Confirm %s [%s, api=%s, workspace=%s%s] by typing yes: ", cmd.CommandPath(), name, profile.APIURL, workspace, confirmationTargets(targets...))
	answer, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil {
		return errors.New("confirmation cancelled")
	}
	if strings.TrimSpace(answer) != "yes" {
		return errors.New("confirmation cancelled")
	}
	return nil
}

func confirmationTargets(inputs ...map[string]any) string {
	values := map[string]string{}
	for _, input := range inputs {
		for key, value := range input {
			if key == "workspace_id" || (!strings.HasSuffix(key, "_id") && key != "version" && key != "from" && key != "to") {
				continue
			}
			if text, ok := value.(string); ok {
				values[key] = text
			}
		}
	}
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var result strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&result, ", %s=%q", key, values[key])
	}
	return result.String()
}

func (a *application) associationCommands(group, collection, idName string) *cobra.Command {
	parent := &cobra.Command{Use: group, Short: "Manage character " + collection + " associations"}
	for _, mode := range []string{"connect", "disconnect"} {
		mode := mode
		var yes bool
		var retrievalMode string
		command := &cobra.Command{
			Use: mode + " CHARACTER_ID " + strings.ToUpper(idName), Args: cobra.ExactArgs(2), Short: mode + " a character association",
			RunE: func(cmd *cobra.Command, args []string) error {
				format, err := output.Parse(a.outputFlag)
				if err != nil {
					return err
				}
				item := map[string]any{"id": args[1], "status": "active"}
				if mode == "disconnect" {
					item["status"] = "inactive"
				}
				if retrievalMode != "" {
					item["retrieval_mode"] = retrievalMode
				}
				body := map[string]any{collection: []any{item}}
				query := map[string]any{"character_id": args[0]}
				op, _ := a.spec.Find("character_update")
				_, _, profile, err := a.profile()
				if err != nil {
					return err
				}
				if err := a.applyWorkspace(op, query, body, profile.WorkspaceID); err != nil {
					return err
				}
				data, err := json.Marshal(body)
				if err != nil {
					return err
				}
				if err := contract.ValidateInput("character_update", query, data); err != nil {
					return err
				}
				if err := a.confirm(cmd, yes, map[string]any{"character_id": args[0], idName: args[1]}); err != nil {
					return err
				}
				client, err := a.apiClient()
				if err != nil {
					return err
				}
				result, err := client.Call(cmd.Context(), "character_update", query, data)
				if err != nil {
					return err
				}
				return output.Resource(cmd.OutOrStdout(), format, result)
			},
		}
		command.Flags().BoolVarP(&yes, "yes", "y", false, "confirm association changes")
		if collection == "docs" && mode == "connect" {
			command.Flags().StringVar(&retrievalMode, "retrieval-mode", "", "document retrieval mode")
		}
		parent.AddCommand(command)
	}
	return parent
}
