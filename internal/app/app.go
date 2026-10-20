package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	contract "github.com/Conv-AI/convai-cli/api"

	"github.com/Conv-AI/convai-cli/internal/api"
	"github.com/Conv-AI/convai-cli/internal/characterapi"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"github.com/Conv-AI/convai-cli/internal/oauth"
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
	// Locker serializes credential changes across processes. The default is
	// an OS file lock keyed to the keychain entry.
	Locker credential.Locker
	// OpenBrowser, Interactive, Now, and LoginTimeout are browser-login seams.
	OpenBrowser  func(string) error
	Interactive  func() bool
	Now          func() time.Time
	LoginTimeout time.Duration
	// OAuthEnvironments replaces the trusted API-to-issuer mapping. Tests use
	// it to point at local stubs; issuer and endpoint checks still apply.
	OAuthEnvironments []oauth.Environment
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
	locker          credential.Locker
	openBrowser     func(string) error
	interactive     func() bool
	now             func() time.Time
	loginTimeout    time.Duration
	environments    []oauth.Environment
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
	if options.OpenBrowser == nil {
		options.OpenBrowser = oauth.OpenBrowser
	}
	if options.Interactive == nil {
		options.Interactive = stdinIsTerminal(options.Stdin)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.LoginTimeout <= 0 {
		options.LoginTimeout = defaultLoginTimeout
	}
	if options.OAuthEnvironments == nil {
		options.OAuthEnvironments = oauth.Environments
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
		locker:          options.Locker,
		openBrowser:     options.OpenBrowser,
		interactive:     options.Interactive,
		now:             options.Now,
		loginTimeout:    options.LoginTimeout,
		environments:    options.OAuthEnvironments,
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

func (a *application) configCommand() *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Manage CLI profiles"}
	command.AddCommand(a.configGetCommand(), a.configUseCommand(), a.configSetCommand(), a.configImportCommand())
	return command
}

func (a *application) configGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show profile metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, name, profile, err := a.profile(cmd.Context())
			if err != nil {
				return err
			}
			value := map[string]any{
				"profile":         name,
				"api_url":         profile.APIURL,
				"credential_type": profile.CredentialType,
				"workspace_id":    profile.WorkspaceID,
				"oauth":           profile.OAuth,
				"ca_bundle":       profile.CABundle,
			}
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			if format == output.Table {
				fmt.Fprintf(cmd.OutOrStdout(), "Profile: %s\nAPI URL: %s\nWorkspace: %s\nCredential type: %s\n", name, profile.APIURL, workspaceLabel(profile.WorkspaceID), profile.CredentialType)
				if profile.OAuth != (config.OAuthTrust{}) {
					fmt.Fprintf(cmd.OutOrStdout(), "OAuth issuer: %s\nOAuth resource: %s\n", profile.OAuth.Issuer, profile.OAuth.Resource)
				}
				if profile.CABundle != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "CA bundle: %s\n", profile.CABundle)
				}
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
			err := a.updateConfig(cmd.Context(), func(cfg *config.Config) error {
				if _, ok := cfg.Profiles[args[0]]; !ok {
					return fmt.Errorf("profile %q does not exist", args[0])
				}
				cfg.CurrentProfile = args[0]
				return nil
			})
			if err != nil {
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
			var name string
			err := a.updateConfig(cmd.Context(), func(cfg *config.Config) error {
				name = a.profileFlag
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
				var err error
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
				return nil
			})
			if err != nil {
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
			client, err := a.apiClient(cmd.Context())
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
				pageSize := len(items)
				for {
					if total != nil && offset+len(items) >= *total {
						break
					}
					if pageSize == 0 {
						if total != nil {
							return errors.New("pagination stopped before the reported total; retry with a stable dataset")
						}
						break
					}
					if total == nil && pageSize < limit {
						break
					}
					if pages >= 1000 {
						return errors.New("pagination exceeded 1000 pages; use explicit page offsets")
					}
					page, updatedTotal, err := client.ListCharacters(cmd.Context(), limit, offset+len(items))
					if err != nil {
						return err
					}
					items = append(items, page...)
					total = updatedTotal
					pageSize = len(page)
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
			client, err := a.apiClient(cmd.Context())
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
			client, err := a.apiClient(cmd.Context())
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
			_, name, profile, err := a.profile(cmd.Context())
			if err != nil {
				return err
			}
			resolved, source, err := a.credentialFor(cmd.Context(), name, profile)
			if err != nil {
				return err
			}
			client, err := a.newAPIClient(name, profile, resolved)
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
				"workspace_id":      profile.WorkspaceID,
			}
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			if format == output.Table {
				fmt.Fprintf(cmd.OutOrStdout(), "status\tok\nprofile\t%s\napi_url\t%s\nworkspace\t%s\ncredential\t%s (%s)\n", name, profile.APIURL, workspaceLabel(profile.WorkspaceID), resolved.Type, source)
				return nil
			}
			return output.Structured(cmd.OutOrStdout(), format, result)
		},
	}
}

func (a *application) profile(ctx context.Context) (config.Config, string, config.Profile, error) {
	cfg, err := a.readConfig(ctx)
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
	// Use the canonical origin everywhere: validation accepts equivalent forms
	// such as a trailing slash, while trust mapping and session binding
	// compare exact strings.
	apiURL := profile.APIURL
	if a.apiURLFlag != "" {
		apiURL = a.apiURLFlag
	}
	profile.APIURL, err = config.NormalizeAPIURL(apiURL)
	if err != nil {
		return config.Config{}, "", config.Profile{}, err
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
	if err := config.ValidateTrust(profile); err != nil {
		return config.Config{}, "", config.Profile{}, err
	}
	return cfg, name, profile, nil
}

// Readers share the writers' lock: on Windows a read can otherwise overlap
// replacement of config.yaml and fail with a sharing violation. Release it
// before any user interaction, credential lock, or network request.
func (a *application) readConfig(ctx context.Context) (config.Config, error) {
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	unlock, err := a.configStore.Lock(ctx)
	if err != nil {
		return config.Config{}, err
	}
	defer unlock()
	return a.configStore.Load()
}

// updateConfig applies one read-modify-write under the cross-process
// configuration lock, so concurrent CLI processes do not lose each other's
// changes. Callers must not perform network calls or wait for the user inside
// change. Lock order: a credential lock, when held, is taken before this one.
func (a *application) updateConfig(ctx context.Context, change func(*config.Config) error) error {
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	unlock, err := a.configStore.Lock(ctx)
	cancel()
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := a.configStore.Load()
	if err != nil {
		return err
	}
	if err := change(&cfg); err != nil {
		return err
	}
	return a.configStore.Save(cfg)
}

func workspaceLabel(value string) string {
	if value == "" {
		return "personal"
	}
	return value
}

func (a *application) apiClient(ctx context.Context) (*api.Client, error) {
	_, name, profile, err := a.profile(ctx)
	if err != nil {
		return nil, err
	}
	resolved, _, err := a.credentialFor(ctx, name, profile)
	if errors.Is(err, credential.ErrNotFound) {
		return nil, fmt.Errorf("resolve credential for profile %s: %w; run `convai auth login --profile %s` or set %s", name, err, name, credential.EnvironmentToken)
	}
	if err != nil {
		return nil, err
	}
	client, err := a.newAPIClient(name, profile, resolved)
	if err != nil {
		return nil, err
	}
	return client.WithWorkspace(profile.WorkspaceID), nil
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
	return credential.Redact(err.Error())
}
