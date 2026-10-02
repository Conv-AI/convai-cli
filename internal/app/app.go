package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	contract "github.com/Conv-AI/convai-cli/api"

	"github.com/Conv-AI/convai-cli/internal/api"
	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"github.com/Conv-AI/convai-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type Options struct {
	Version         string
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	ConfigStore     config.Store
	CredentialStore credential.Store
	HTTPClient      *http.Client
	ReadSecret      func(int) ([]byte, error)
}

type application struct {
	version         string
	stdin           io.Reader
	stdout          io.Writer
	stderr          io.Writer
	configStore     config.Store
	credentialStore credential.Store
	httpClient      *http.Client
	readSecret      func(int) ([]byte, error)
	profileFlag     string
	apiURLFlag      string
	outputFlag      string
	workspaceFlag   string
	spec            contract.Spec
	commands        []contract.Command
}

func New(options Options) (*cobra.Command, error) {
	if options.ConfigStore == nil {
		store, err := config.NewFileStore()
		if err != nil {
			return nil, err
		}
		options.ConfigStore = store
	}
	if options.CredentialStore == nil {
		options.CredentialStore = credential.KeyringStore{}
	}
	if options.Stdin == nil {
		options.Stdin = os.Stdin
	}
	if options.Stdout == nil {
		options.Stdout = os.Stdout
	}
	if options.Stderr == nil {
		options.Stderr = os.Stderr
	}
	if options.ReadSecret == nil {
		options.ReadSecret = term.ReadPassword
	}
	spec, commands, err := contract.Load()
	if err != nil {
		return nil, err
	}
	a := &application{
		version:         options.Version,
		stdin:           options.Stdin,
		stdout:          options.Stdout,
		stderr:          options.Stderr,
		configStore:     options.ConfigStore,
		credentialStore: options.CredentialStore,
		httpClient:      options.HTTPClient,
		readSecret:      options.ReadSecret,
		spec:            spec,
		commands:        commands,
	}
	return a.rootCommand(), nil
}

func (a *application) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "convai",
		Short:         "Manage Convai resources from the command line",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(a.stdin)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.PersistentFlags().StringVar(&a.profileFlag, "profile", "", "configuration profile")
	root.PersistentFlags().StringVar(&a.apiURLFlag, "api-url", "", "override the profile API URL")
	root.PersistentFlags().StringVarP(&a.outputFlag, "output", "o", "table", "output: table, json, or yaml")
	root.PersistentFlags().StringVar(&a.workspaceFlag, "workspace", "", "Workspace UUID, or personal to use the account default")

	root.AddCommand(
		a.authCommand(),
		a.configCommand(),
		a.characterCommand(),
		a.doctorCommand(),
		&cobra.Command{
			Use:   "version",
			Short: "Print the CLI version",
			Run: func(cmd *cobra.Command, _ []string) {
				fmt.Fprintln(cmd.OutOrStdout(), a.version)
			},
		},
	)
	a.addResourceCommands(root)
	return root
}

func (a *application) authCommand() *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Manage Convai credentials"}
	auth.AddCommand(a.authLoginCommand(), a.authStatusCommand(), a.authLogoutCommand())
	return auth
}

func (a *application) authLoginCommand() *cobra.Command {
	var tokenStdin bool
	var noVerify bool
	command := &cobra.Command{
		Use:   "login",
		Short: "Store an API key, PAT, or issued OAuth access token in the OS keychain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, profileName, profile, err := a.profile()
			if err != nil {
				return err
			}
			value, err := a.readCredential(tokenStdin)
			if err != nil {
				return err
			}
			parsed, err := credential.Parse(value)
			if err != nil {
				return err
			}
			profile.CredentialType = parsed.Type
			if a.apiURLFlag != "" {
				profile.APIURL, err = config.NormalizeAPIURL(a.apiURLFlag)
				if err != nil {
					return err
				}
			}
			if !noVerify {
				client, err := api.New(profile.APIURL, parsed, a.httpClient)
				if err != nil {
					return err
				}
				if _, _, err := client.WithWorkspace(profile.WorkspaceID).ListCharacters(cmd.Context(), 1, 0); err != nil {
					return fmt.Errorf("verify credential: %w", err)
				}
			}
			previous, previousErr := a.credentialStore.Get(profileName)
			if previousErr != nil && !errors.Is(previousErr, credential.ErrNotFound) {
				return previousErr
			}
			if err := a.credentialStore.Set(profileName, parsed); err != nil {
				return err
			}
			// Workspace flags affect verification, not the saved profile selection.
			profile.WorkspaceID = cfg.Profiles[profileName].WorkspaceID
			cfg.Profiles[profileName] = profile
			cfg.CurrentProfile = profileName
			if err := a.configStore.Save(cfg); err != nil {
				if previousErr == nil {
					_ = a.credentialStore.Set(profileName, previous)
				} else {
					_ = a.credentialStore.Delete(profileName)
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Stored %s credential for profile %s\n", parsed.Type, profileName)
			return nil
		},
	}
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the credential from stdin")
	command.Flags().BoolVar(&noVerify, "no-verify", false, "store without a read-only API verification")
	return command
}

func (a *application) authStatusCommand() *cobra.Command {
	var check bool
	command := &cobra.Command{
		Use:   "status",
		Short: "Show credential metadata without exposing the credential",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, profileName, profile, err := a.profile()
			if err != nil {
				return err
			}
			resolved, source, err := credential.Resolve(a.credentialStore, profileName)
			if err != nil {
				return err
			}
			status := map[string]any{
				"profile":         profileName,
				"api_url":         profile.APIURL,
				"credential_type": resolved.Type,
				"source":          source,
				"workspace_id":    profile.WorkspaceID,
			}
			if check {
				client, err := api.New(profile.APIURL, resolved, a.httpClient)
				if err != nil {
					return err
				}
				if _, _, err := client.WithWorkspace(profile.WorkspaceID).ListCharacters(cmd.Context(), 1, 0); err != nil {
					return err
				}
				status["verified"] = true
			}
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			if format == output.Table {
				fmt.Fprintf(cmd.OutOrStdout(), "Profile: %s\nAPI URL: %s\nCredential: %s (%s)\n", profileName, profile.APIURL, resolved.Type, source)
				if check {
					fmt.Fprintln(cmd.OutOrStdout(), "Verified: true")
				}
				return nil
			}
			return output.Structured(cmd.OutOrStdout(), format, status)
		},
	}
	command.Flags().BoolVar(&check, "check", false, "verify with a read-only Character API request")
	return command
}

func (a *application) authLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the profile credential from the operating-system keychain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, profileName, _, err := a.profile()
			if err != nil {
				return err
			}
			err = a.credentialStore.Delete(profileName)
			if errors.Is(err, credential.ErrNotFound) {
				fmt.Fprintf(cmd.OutOrStdout(), "No stored credential for profile %s\n", profileName)
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed credential for profile %s\n", profileName)
			return nil
		},
	}
}

func (a *application) configCommand() *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Manage CLI profiles"}
	command.AddCommand(a.configGetCommand(), a.configUseCommand(), a.configSetCommand())
	return command
}

func (a *application) configGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show profile metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, name, profile, err := a.profile()
			if err != nil {
				return err
			}
			value := map[string]any{
				"profile":         name,
				"api_url":         profile.APIURL,
				"credential_type": profile.CredentialType,
				"workspace_id":    profile.WorkspaceID,
			}
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			if format == output.Table {
				fmt.Fprintf(cmd.OutOrStdout(), "Profile: %s\nAPI URL: %s\nCredential type: %s\n", name, profile.APIURL, profile.CredentialType)
				return nil
			}
			return output.Structured(cmd.OutOrStdout(), format, value)
		},
	}
}

func (a *application) configUseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "use PROFILE",
		Short: "Set the current profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.ValidateProfileName(args[0]); err != nil {
				return err
			}
			cfg, err := a.configStore.Load()
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[args[0]]; !ok {
				return fmt.Errorf("profile %q does not exist", args[0])
			}
			cfg.CurrentProfile = args[0]
			if err := a.configStore.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Current profile: %s\n", args[0])
			return nil
		},
	}
}

func (a *application) configSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set api-url|workspace-id VALUE",
		Short: "Create or update profile metadata",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "api-url" && args[0] != "workspace-id" {
				return errors.New("only api-url and workspace-id can be set")
			}
			cfg, err := a.configStore.Load()
			if err != nil {
				return err
			}
			name := a.profileFlag
			if name == "" {
				name = cfg.CurrentProfile
			}
			if err := config.ValidateProfileName(name); err != nil {
				return err
			}
			profile, ok := cfg.Profiles[name]
			if !ok {
				profile.CredentialType = config.CredentialPAT
				profile.APIURL = config.DefaultAPIURL
			}
			value := args[1]
			if args[0] == "api-url" {
				profile.APIURL, err = config.NormalizeAPIURL(value)
			} else {
				if value == "personal" {
					value = ""
				}
				err = config.ValidateWorkspaceID(value)
				profile.WorkspaceID = value
			}
			if err != nil {
				return err
			}
			cfg.Profiles[name] = profile
			if cfg.CurrentProfile == "" {
				cfg.CurrentProfile = name
			}
			if err := a.configStore.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %s %s to %s\n", name, args[0], args[1])
			return nil
		},
	}
}

func (a *application) characterCommand() *cobra.Command {
	command := &cobra.Command{Use: "character", Short: "Manage Convai characters"}
	command.AddCommand(a.characterListCommand(), a.characterGetCommand(), a.characterCreateCommand())
	return command
}

func (a *application) characterListCommand() *cobra.Command {
	var limit int
	var offset int
	var all bool
	var sort string
	command := &cobra.Command{
		Use:   "list",
		Short: "List characters owned by the authenticated account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			if limit < 1 || limit > 500 {
				return errors.New("--limit must be between 1 and 500")
			}
			if offset < 0 {
				return errors.New("--offset must be at least 0")
			}
			client, err := a.apiClient()
			if err != nil {
				return err
			}
			if sort != "created" && sort != "last_updated" && sort != "last_interacted" {
				return errors.New("invalid --sort")
			}
			client = client.WithSort(sort)
			items, total, err := client.ListCharacters(cmd.Context(), limit, offset)
			if err != nil {
				return err
			}
			if all {
				pages := 1
				for offset+len(items) < total {
					if pages >= 1000 {
						return errors.New("pagination exceeded 1000 pages; use explicit page offsets")
					}
					page, updatedTotal, err := client.ListCharacters(cmd.Context(), limit, offset+len(items))
					if err != nil {
						return err
					}
					if len(page) == 0 {
						return errors.New("pagination stopped before the reported total; retry with a stable dataset")
					}
					items = append(items, page...)
					total = updatedTotal
					pages++
				}
			}
			return output.CharacterList(cmd.OutOrStdout(), format, items, total)
		},
	}
	command.Flags().IntVar(&limit, "limit", 50, "maximum number of characters")
	command.Flags().IntVar(&offset, "offset", 0, "number of characters to skip")
	command.Flags().BoolVar(&all, "all", false, "fetch every remaining page")
	command.Flags().StringVar(&sort, "sort", "created", "created, last_updated, or last_interacted")
	return command
}

func (a *application) characterGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get CHARACTER_ID",
		Short: "Get one owned character",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.apiClient()
			if err != nil {
				return err
			}
			character, err := client.GetCharacter(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			return output.Character(cmd.OutOrStdout(), format, character)
		},
	}
}

func (a *application) characterCreateCommand() *cobra.Command {
	var name string
	var voice string
	var backstory string
	var inputFile string
	command := &cobra.Command{
		Use:   "create",
		Short: "Create a character",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			request, err := a.createRequest(name, voice, backstory, inputFile)
			if err != nil {
				return err
			}
			if a.workspaceFlag == "personal" && request.WorkspaceId != nil && *request.WorkspaceId != "" {
				return errors.New("input workspace conflicts with explicit personal selection")
			}
			client, err := a.apiClient()
			if err != nil {
				return err
			}
			created, err := client.CreateCharacter(cmd.Context(), request)
			if err != nil {
				return err
			}
			return output.CharacterCreated(cmd.OutOrStdout(), format, created)
		},
	}
	command.Flags().StringVar(&name, "name", "", "character name")
	command.Flags().StringVar(&voice, "voice", "", "voice identifier")
	command.Flags().StringVar(&backstory, "backstory", "", "non-empty character backstory")
	command.Flags().StringVarP(&inputFile, "file", "f", "", "JSON or YAML request file; use - for stdin")
	return command
}

func (a *application) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Verify configuration, credential access, and Character API connectivity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, name, profile, err := a.profile()
			if err != nil {
				return err
			}
			_ = cfg
			resolved, source, err := credential.Resolve(a.credentialStore, name)
			if err != nil {
				return err
			}
			client, err := api.New(profile.APIURL, resolved, a.httpClient)
			if err != nil {
				return err
			}
			_, _, err = client.WithWorkspace(profile.WorkspaceID).ListCharacters(cmd.Context(), 1, 0)
			if err != nil {
				return err
			}
			result := map[string]any{
				"status":            "ok",
				"profile":           name,
				"api_url":           profile.APIURL,
				"credential_type":   resolved.Type,
				"credential_source": source,
			}
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			if format == output.Table {
				fmt.Fprintf(cmd.OutOrStdout(), "status\tok\nprofile\t%s\napi_url\t%s\ncredential\t%s (%s)\n", name, profile.APIURL, resolved.Type, source)
				return nil
			}
			return output.Structured(cmd.OutOrStdout(), format, result)
		},
	}
}

func (a *application) profile() (config.Config, string, config.Profile, error) {
	cfg, err := a.configStore.Load()
	if err != nil {
		return config.Config{}, "", config.Profile{}, err
	}
	name := a.profileFlag
	if name == "" {
		name = cfg.CurrentProfile
	}
	if err := config.ValidateProfileName(name); err != nil {
		return config.Config{}, "", config.Profile{}, err
	}
	profile, ok := cfg.Profiles[name]
	if !ok {
		return config.Config{}, "", config.Profile{}, fmt.Errorf(
			"profile %q does not exist; create it with `convai config set api-url URL --profile %s`",
			name,
			name,
		)
	}
	if a.apiURLFlag != "" {
		profile.APIURL, err = config.NormalizeAPIURL(a.apiURLFlag)
		if err != nil {
			return config.Config{}, "", config.Profile{}, err
		}
	}
	if a.workspaceFlag != "" {
		profile.WorkspaceID = a.workspaceFlag
		if profile.WorkspaceID == "personal" {
			profile.WorkspaceID = ""
		}
	}
	if err := config.ValidateWorkspaceID(profile.WorkspaceID); err != nil {
		return config.Config{}, "", config.Profile{}, err
	}
	return cfg, name, profile, nil
}

func (a *application) apiClient() (*api.Client, error) {
	_, name, profile, err := a.profile()
	if err != nil {
		return nil, err
	}
	resolved, _, err := credential.Resolve(a.credentialStore, name)
	if err != nil {
		return nil, fmt.Errorf("resolve credential for profile %s: %w; run `convai auth login --profile %s` or set %s", name, err, name, credential.EnvironmentToken)
	}
	client, err := api.New(profile.APIURL, resolved, a.httpClient)
	if err != nil {
		return nil, err
	}
	return client.WithWorkspace(profile.WorkspaceID), nil
}

func (a *application) readCredential(fromStdin bool) (string, error) {
	if fromStdin {
		scanner := bufio.NewScanner(io.LimitReader(a.stdin, 1024))
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", fmt.Errorf("read credential from stdin: %w", err)
			}
			return "", errors.New("credential stdin is empty")
		}
		return scanner.Text(), nil
	}
	file, ok := a.stdin.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", errors.New("stdin is not a terminal; use --token-stdin")
	}
	fmt.Fprint(a.stderr, "Convai API key, PAT, or issued OAuth access token: ")
	value, err := a.readSecret(int(file.Fd()))
	fmt.Fprintln(a.stderr)
	if err != nil {
		return "", fmt.Errorf("read credential: %w", err)
	}
	return string(value), nil
}

func (a *application) createRequest(name, voice, backstory, inputFile string) (characterapi.CharacterCreate, error) {
	if inputFile != "" {
		if name != "" || voice != "" || backstory != "" {
			return characterapi.CharacterCreate{}, errors.New("--file cannot be combined with --name, --voice, or --backstory")
		}
		body, err := a.inputObject(inputFile)
		if err != nil {
			return characterapi.CharacterCreate{}, err
		}
		data, err := json.Marshal(body)
		if err != nil {
			return characterapi.CharacterCreate{}, err
		}
		request, err := api.DecodeCreateRequest(bytes.NewReader(data), "json")
		if err != nil {
			return characterapi.CharacterCreate{}, err
		}
		if err := validateCreateRequest(request); err != nil {
			return characterapi.CharacterCreate{}, err
		}
		if err := contract.ValidateInput("character_create", nil, data); err != nil {
			return characterapi.CharacterCreate{}, err
		}
		return request, nil
	}
	request := characterapi.CharacterCreate{
		CharName:  name,
		Backstory: backstory,
	}
	if voice != "" {
		request.VoiceType = &voice
	}
	if err := validateCreateRequest(request); err != nil {
		return characterapi.CharacterCreate{}, err
	}
	return request, nil
}

func validateCreateRequest(request characterapi.CharacterCreate) error {
	if strings.TrimSpace(request.CharName) == "" {
		return errors.New("character name is required")
	}
	if len([]rune(request.CharName)) > 100 {
		return errors.New("character name must contain at most 100 characters")
	}
	if request.VoiceType != nil && strings.TrimSpace(*request.VoiceType) == "" {
		return errors.New("voice identifier must not be blank when supplied")
	}
	if strings.TrimSpace(request.Backstory) == "" {
		return errors.New("backstory is required")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return contract.ValidateInput("character_create", nil, data)
}

func RedactError(err error) string {
	message := err.Error()
	for _, prefix := range []string{"cvpat_", "cvoat_", "cvort_", "cvoac_", "ck_live_", "sk_"} {
		for {
			index := strings.Index(message, prefix)
			if index < 0 {
				break
			}
			end := index
			for end < len(message) && !strings.ContainsRune(" \t\r\n\"'()[]{}", rune(message[end])) {
				end++
			}
			message = message[:index] + "[REDACTED]" + message[end:]
		}
	}
	return message
}
