package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/Conv-AI/convai-cli/internal/api"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"github.com/Conv-AI/convai-cli/internal/oauth"
	"github.com/Conv-AI/convai-cli/internal/transport"
	"github.com/spf13/cobra"
)

func (a *application) configImportCommand() *cobra.Command {
	var file string
	command := &cobra.Command{
		Use: "import", Short: "Import approved non-secret profile metadata", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.apiURLFlag != "" || a.workspaceFlag != "" {
				return errors.New("config import does not accept API or Workspace overrides")
			}
			reader, err := os.Open(file)
			if err != nil {
				return fmt.Errorf("read profile file: %w", err)
			}
			defer reader.Close()
			data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
			if err != nil || len(data) > 1<<20 {
				return errors.New("profile file cannot be read or exceeds 1 MiB")
			}
			profile, err := config.DecodeProfile(data)
			if err != nil {
				return err
			}
			if _, _, err := transport.New(a.httpClient, profile.CABundle); err != nil {
				return err
			}
			var name string
			err = a.updateConfig(cmd.Context(), func(cfg *config.Config) error {
				name = a.profileFlag
				if name == "" {
					name = cfg.CurrentProfile
				}
				if err := config.ValidateProfileName(name); err != nil {
					return err
				}
				cfg.Profiles[name] = profile
				return nil
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported profile %s; credentials were not changed. Changed browser-login trust requires a new login.\n", name)
			return nil
		},
	}
	command.Flags().StringVar(&file, "file", "", "approved YAML profile file (no credentials)")
	_ = command.MarkFlagRequired("file")
	return command
}

type loginTrust struct {
	issuer, resource, audience, caFingerprint string
	httpClient                                *http.Client
}

func (a *application) loginTrust(profile config.Profile) (loginTrust, error) {
	if err := config.ValidateTrust(profile); err != nil {
		return loginTrust{}, err
	}
	issuer, trusted := oauth.IssuerFor(a.environments, profile.APIURL)
	resource, audience := "", oauth.Audience
	if profile.OAuth != (config.OAuthTrust{}) {
		issuer, trusted = profile.OAuth.Issuer, true
		resource, audience = profile.OAuth.Resource, profile.OAuth.Resource
	}
	if !trusted {
		return loginTrust{}, fmt.Errorf("browser login requires a fixed Convai-hosted mapping (%s) or an explicitly approved custom OAuth profile; for %s use `convai auth login --method token` until portable OAuth is available", a.trustedAPIs(), profile.APIURL)
	}
	client, fingerprint, err := transport.New(a.httpClient, profile.CABundle)
	return loginTrust{issuer: issuer, resource: resource, audience: audience, caFingerprint: fingerprint, httpClient: client}, err
}

func (a *application) newAPIClient(name string, profile config.Profile, value credential.Credential) (*api.Client, error) {
	var client *http.Client
	var err error
	if value.Managed() {
		trust, err := a.checkBinding(name, value, profile)
		if err != nil {
			return nil, err
		}
		client = trust.httpClient
	} else {
		client, _, err = transport.New(a.httpClient, profile.CABundle)
		if err != nil {
			return nil, err
		}
	}
	return api.New(profile.APIURL, value, client)
}

// saveRefreshed checks current trust while holding the configuration lock.
// It never writes config or holds that lock across a network request.
func (a *application) saveRefreshed(ctx context.Context, name string, original config.Profile, next credential.Credential) error {
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	unlock, err := a.configStore.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := a.configStore.Load()
	if err != nil {
		return err
	}
	profile, ok := cfg.Profiles[name]
	if !ok || !sameProfileTrust(profile, original) {
		return errors.New("profile trust changed during refresh; the rotated grant was not saved")
	}
	if _, err := a.checkBinding(name, next, profile); err != nil {
		return err
	}
	return a.credentialStore.Set(name, next)
}

func sameProfileTrust(left, right config.Profile) bool {
	leftURL, _ := config.NormalizeAPIURL(left.APIURL)
	rightURL, _ := config.NormalizeAPIURL(right.APIURL)
	return leftURL == rightURL && left.OAuth == right.OAuth && left.CABundle == right.CABundle
}
