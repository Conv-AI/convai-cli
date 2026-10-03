package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	contract "github.com/Conv-AI/convai-cli/api"
	"github.com/Conv-AI/convai-cli/internal/api"
	"github.com/Conv-AI/convai-cli/internal/config"
	"github.com/Conv-AI/convai-cli/internal/credential"
	"github.com/Conv-AI/convai-cli/internal/oauth"
	"github.com/Conv-AI/convai-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	defaultLoginTimeout = 5 * time.Minute
	lockTimeout         = time.Minute
	// Refresh slightly before expiry so a request cannot race the deadline.
	refreshSkew = 2 * time.Minute
	// verificationScope authorizes the read-only character list used to verify
	// a credential against the selected API and Workspace.
	verificationScope = "character:read"
)

func (a *application) authCommand() *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Manage Convai credentials"}
	auth.AddCommand(a.authLoginCommand(), a.authStatusCommand(), a.authLogoutCommand())
	return auth
}

func (a *application) authLoginCommand() *cobra.Command {
	var method string
	var tokenStdin bool
	var noVerify bool
	var scopes string
	var readOnly bool
	command := &cobra.Command{
		Use:   "login",
		Short: "Sign in with your browser, or import an API key, PAT, or OAuth access token",
		Long: "Without flags, login opens your browser to sign in to Convai and stores a refreshable\n" +
			"session in the OS keychain. Browser login is available for the Convai staging, Preview,\n" +
			"and production APIs. Use --method token (or --token-stdin in CI) to import an API key,\n" +
			"PAT, or already-issued OAuth access token instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if method == "" {
				method = "browser"
				if tokenStdin {
					method = "token"
				}
			}
			switch method {
			case "browser":
				if tokenStdin {
					return errors.New("--token-stdin requires --method token")
				}
			case "token":
				if scopes != "" || readOnly {
					return errors.New("--scopes and --read-only apply only to browser login; scopes of an imported credential are fixed when it is issued")
				}
			default:
				return errors.New("--method must be browser or token")
			}
			cfg, profileName, profile, err := a.profile()
			if err != nil {
				return err
			}
			if method == "token" {
				value, err := a.readCredential(tokenStdin)
				if err != nil {
					return err
				}
				parsed, err := credential.Parse(value)
				if err != nil {
					return err
				}
				return a.storeLogin(cmd, cfg, profileName, profile, parsed, noVerify, nil)
			}
			requested, err := a.loginScopes(scopes, readOnly)
			if err != nil {
				return err
			}
			return a.browserLogin(cmd, cfg, profileName, profile, requested, noVerify)
		},
	}
	command.Flags().StringVar(&method, "method", "", "browser (default) or token")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the credential from stdin (implies --method token)")
	command.Flags().BoolVar(&noVerify, "no-verify", false, "store without a read-only API verification")
	command.Flags().StringVar(&scopes, "scopes", "", "comma-separated browser-login scopes (default: every scope the CLI commands use)")
	command.Flags().BoolVar(&readOnly, "read-only", false, "request only read scopes for browser login")
	return command
}

// loginScopes requests least privilege from the curated command catalog's
// owner-declared scopes rather than everything Auth can issue.
func (a *application) loginScopes(requested string, readOnly bool) ([]string, error) {
	if readOnly {
		if requested != "" {
			return nil, errors.New("--read-only cannot be combined with --scopes")
		}
		return contract.CommandScopes(a.spec, a.commands, true), nil
	}
	supported := contract.CommandScopes(a.spec, a.commands, false)
	if requested == "" {
		return supported, nil
	}
	selected := strings.FieldsFunc(requested, func(r rune) bool { return r == ',' || r == ' ' })
	for _, scope := range selected {
		if !slices.Contains(supported, scope) {
			return nil, fmt.Errorf("unsupported scope %q; choose from %s", scope, strings.Join(supported, ", "))
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("--scopes must name at least one scope")
	}
	slices.Sort(selected)
	return slices.Compact(selected), nil
}

func (a *application) browserLogin(cmd *cobra.Command, cfg config.Config, name string, profile config.Profile, scopes []string, noVerify bool) error {
	ctx := cmd.Context()
	issuer, trusted := oauth.IssuerFor(a.environments, profile.APIURL)
	if !trusted {
		return fmt.Errorf("browser login is available only for the Convai-hosted APIs (%s); for %s use `convai auth login --method token`", a.trustedAPIs(), profile.APIURL)
	}
	if !a.interactive() {
		return fmt.Errorf("browser login needs an interactive terminal; in CI set %s or run `convai auth login --method token --token-stdin`", credential.EnvironmentToken)
	}
	client, err := oauth.Discover(ctx, a.httpClient, issuer, a.now)
	if err != nil {
		return err
	}
	state, err := oauth.Random()
	if err != nil {
		return err
	}
	verifier, err := oauth.Random()
	if err != nil {
		return err
	}
	callback, err := oauth.StartCallback(state, issuer, client.Metadata().IssuerInCallback)
	if err != nil {
		return err
	}
	defer callback.Close()
	authorizationURL := client.AuthorizationURL(callback.RedirectURI, state, oauth.Challenge(verifier), scopes)
	fmt.Fprintf(a.stderr, "Opening your browser to sign in to Convai for profile %s (scopes: %s).\n", name, strings.Join(scopes, " "))
	if err := a.openBrowser(authorizationURL); err != nil {
		fmt.Fprintln(a.stderr, "Could not open a browser automatically.")
	}
	fmt.Fprintf(a.stderr, "If the browser does not open, visit this URL on this computer:\n\n  %s\n\nWaiting for sign-in (press Ctrl+C to cancel)...\n", authorizationURL)
	waitCtx, cancel := context.WithTimeout(ctx, a.loginTimeout)
	code, err := callback.Wait(waitCtx)
	cancel()
	callback.Close()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("browser sign-in timed out after %s; no credential was stored", a.loginTimeout)
	case errors.Is(err, context.Canceled):
		return errors.New("browser sign-in was cancelled; no credential was stored")
	case err != nil:
		return fmt.Errorf("%w; no credential was stored", err)
	}
	tokens, err := client.ExchangeCode(ctx, code, verifier, callback.RedirectURI, scopes)
	if err != nil {
		return fmt.Errorf("complete browser sign-in: %w", err)
	}
	revoke := func(ctx context.Context) error { return client.RevokeAll(ctx, tokens) }
	session, err := credential.NewSession(tokens.AccessToken, credential.Session{
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    tokens.ExpiresAt,
		Scopes:       tokens.Scopes,
		Issuer:       issuer,
		ClientID:     oauth.ClientID,
		Audience:     oauth.Audience,
		APIURL:       profile.APIURL,
	})
	if err != nil {
		return a.revokeAfterFailure(ctx, err, revoke)
	}
	return a.storeLogin(cmd, cfg, name, profile, session, noVerify, revoke)
}

// storeLogin verifies and persists a new credential. A newly issued browser
// grant is revoked on any failure, so no issued credential is left untracked.
// A previous browser grant is revoked before it is replaced; if that fails,
// nothing changes and its revocation handle stays in the keychain.
func (a *application) storeLogin(
	cmd *cobra.Command,
	cfg config.Config,
	name string,
	profile config.Profile,
	value credential.Credential,
	noVerify bool,
	revokeNew func(context.Context) error,
) error {
	ctx := cmd.Context()
	fail := func(err error) error { return a.revokeAfterFailure(ctx, err, revokeNew) }
	if !noVerify {
		if err := a.verifyLogin(ctx, profile, value); err != nil {
			return fail(err)
		}
	}
	unlock, err := a.lockCredential(ctx, name)
	if err != nil {
		return fail(err)
	}
	defer unlock()
	previous, previousErr := a.credentialStore.Get(name)
	if previousErr != nil && !errors.Is(previousErr, credential.ErrNotFound) {
		return fail(previousErr)
	}
	replacedSession := previousErr == nil && previous.Managed()
	if replacedSession {
		if err := a.revokeSession(ctx, name, previous); err != nil {
			return fail(fmt.Errorf("revoke the previous browser login for profile %s: %w; the previous login was kept", name, err))
		}
	}
	// rollback undoes this login's keychain write. A still-valid previous
	// credential is restored; a revoked previous browser login is removed,
	// because it can no longer be used. Rollback failures are reported.
	rollback := func(cause error) error {
		if previousErr == nil && !replacedSession {
			if err := a.credentialStore.Set(name, previous); err != nil {
				return fmt.Errorf("%w; restoring the previous credential failed: %v", cause, err)
			}
			return fmt.Errorf("%w; the previous credential was restored", cause)
		}
		if replacedSession {
			cause = fmt.Errorf("%w; the previous browser login had already been revoked", cause)
		}
		if err := a.credentialStore.Delete(name); err != nil && !errors.Is(err, credential.ErrNotFound) {
			return fmt.Errorf("%w; removing the stored credential failed: %v", cause, err)
		}
		return cause
	}
	if err := a.credentialStore.Set(name, value); err != nil {
		if replacedSession {
			// The keychain still holds the revoked previous login.
			return fail(rollback(err))
		}
		return fail(err)
	}
	// Workspace flags affect verification, not the saved profile selection.
	profile.WorkspaceID = cfg.Profiles[name].WorkspaceID
	profile.CredentialType = value.Type
	cfg.Profiles[name] = profile
	cfg.CurrentProfile = name
	if err := a.configStore.Save(cfg); err != nil {
		return fail(rollback(err))
	}
	if value.Managed() {
		fmt.Fprintf(a.stdout, "Signed in with browser login for profile %s (scopes: %s)\n", name, strings.Join(value.Session.Scopes, " "))
	} else {
		fmt.Fprintf(a.stdout, "Stored %s credential for profile %s\n", value.Type, name)
	}
	if os.Getenv(credential.EnvironmentToken) != "" {
		fmt.Fprintf(a.stderr, "Note: %s is set and takes precedence over the stored credential.\n", credential.EnvironmentToken)
	}
	return nil
}

// verifyLogin checks a new credential with a read-only Character API request.
// Imported credentials must pass it, as before. A browser login has already
// authenticated through the code exchange, so only HTTP 401 (the API does not
// accept the credential) fails it; Workspace, plan, and scope denials are
// authorization decisions that Character API makes per command, and are
// reported as a warning. Without the read scope the check is skipped.
func (a *application) verifyLogin(ctx context.Context, profile config.Profile, value credential.Credential) error {
	if value.Managed() && !slices.Contains(value.Session.Scopes, verificationScope) {
		return nil
	}
	client, err := api.New(profile.APIURL, value, a.httpClient)
	if err != nil {
		return err
	}
	_, _, err = client.WithWorkspace(profile.WorkspaceID).ListCharacters(ctx, 1, 0)
	if err == nil {
		return nil
	}
	var response *api.Error
	if !value.Managed() || (errors.As(err, &response) && response.StatusCode == http.StatusUnauthorized) {
		return fmt.Errorf("verify credential: %w", err)
	}
	fmt.Fprintf(a.stderr, "Warning: signed in, but the read-only Character API check did not pass: %s\n", RedactError(err))
	return nil
}

func (a *application) revokeAfterFailure(ctx context.Context, err error, revoke func(context.Context) error) error {
	if revoke == nil {
		return err
	}
	if cleanup := revoke(context.WithoutCancel(ctx)); cleanup != nil {
		return fmt.Errorf("%w; the new browser login could not be revoked and expires on its own: %v", err, cleanup)
	}
	return fmt.Errorf("%w; the new browser login was revoked", err)
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
			format, err := output.Parse(a.outputFlag)
			if err != nil {
				return err
			}
			var resolved credential.Credential
			var source string
			if check {
				resolved, source, err = a.credentialFor(cmd.Context(), profileName, profile)
			} else {
				resolved, source, err = credential.Resolve(a.credentialStore, profileName)
			}
			if err != nil {
				return err
			}
			status := map[string]any{
				"profile":         profileName,
				"api_url":         profile.APIURL,
				"credential_type": resolved.Type,
				"source":          source,
				"workspace_id":    profile.WorkspaceID,
				"login":           "imported",
			}
			if resolved.Managed() {
				session := resolved.Session
				status["login"] = "browser"
				status["issuer"] = session.Issuer
				status["client_id"] = session.ClientID
				status["bound_api_url"] = session.APIURL
				status["scopes"] = session.Scopes
				status["expires_at"] = session.ExpiresAt.UTC().Format(time.RFC3339)
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
			if format != output.Table {
				return output.Structured(cmd.OutOrStdout(), format, status)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Profile: %s\nAPI URL: %s\nWorkspace: %s\nCredential: %s (%s)\n", profileName, profile.APIURL, workspaceLabel(profile.WorkspaceID), resolved.Type, source)
			if resolved.Managed() {
				session := resolved.Session
				fmt.Fprintf(out, "Login: browser (issuer %s, client %s)\n", session.Issuer, session.ClientID)
				if session.APIURL != profile.APIURL {
					fmt.Fprintf(out, "Bound API URL: %s (log in again for %s)\n", session.APIURL, profile.APIURL)
				}
				fmt.Fprintf(out, "Scopes: %s\nAccess expires: %s (refreshed automatically)\n", strings.Join(session.Scopes, " "), session.ExpiresAt.UTC().Format(time.RFC3339))
			}
			if check {
				fmt.Fprintln(out, "Verified: true")
			}
			return nil
		},
	}
	command.Flags().BoolVar(&check, "check", false, "verify with a read-only Character API request")
	return command
}

func (a *application) authLogoutCommand() *cobra.Command {
	var local bool
	command := &cobra.Command{
		Use:   "logout",
		Short: "Revoke a browser login and remove the profile credential from the keychain",
		Long: "Logout revokes a browser login through Convai sign-in, then removes it from the OS keychain.\n" +
			"If revocation fails, the credential is kept so logout can be retried; --local removes only\n" +
			"the local copy. API keys, PATs, and imported tokens are removed locally; revoke them where\n" +
			"they were issued.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, profileName, _, err := a.profile()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			unlock, err := a.lockCredential(ctx, profileName)
			if err != nil {
				return err
			}
			defer unlock()
			current, getErr := a.credentialStore.Get(profileName)
			if errors.Is(getErr, credential.ErrNotFound) {
				fmt.Fprintf(cmd.OutOrStdout(), "No stored credential for profile %s\n", profileName)
				return nil
			}
			if !local {
				if getErr != nil {
					return fmt.Errorf("%w; run `convai auth logout --local` to remove it without server revocation", getErr)
				}
				if current.Managed() {
					if err := a.revokeSession(ctx, profileName, current); err != nil {
						return fmt.Errorf("revoke browser login for profile %s: %w; the local credential was kept so logout can be retried, or run `convai auth logout --local` to remove only the local copy", profileName, err)
					}
				}
			}
			if err := a.credentialStore.Delete(profileName); err != nil && !errors.Is(err, credential.ErrNotFound) {
				return err
			}
			switch {
			case local && (getErr != nil || current.Managed()):
				fmt.Fprintf(cmd.OutOrStdout(), "Removed local credential for profile %s\n", profileName)
				fmt.Fprintln(a.stderr, "Warning: any browser login was not revoked and stays valid until it expires.")
			case current.Managed():
				fmt.Fprintf(cmd.OutOrStdout(), "Revoked browser login and removed credential for profile %s\n", profileName)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "Removed credential for profile %s\n", profileName)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&local, "local", false, "remove only the local keychain copy without revoking a browser login")
	return command
}

// credentialFor is the single resolution path for every REST request:
// environment credentials first, then the keychain. A managed browser login is
// checked against its binding and refreshed before it nears expiry.
func (a *application) credentialFor(ctx context.Context, name string, profile config.Profile) (credential.Credential, string, error) {
	resolved, source, err := credential.Resolve(a.credentialStore, name)
	if err != nil || !resolved.Managed() {
		return resolved, source, err
	}
	if err := a.checkBinding(name, resolved, profile.APIURL); err != nil {
		return credential.Credential{}, "", err
	}
	if !a.needsRefresh(resolved) {
		return resolved, source, nil
	}
	refreshed, err := a.refresh(ctx, name, profile.APIURL)
	return refreshed, source, err
}

// checkBinding prevents a stored browser login from being forwarded to any API
// origin, issuer, client, or audience other than the one it was issued for.
func (a *application) checkBinding(name string, value credential.Credential, apiURL string) error {
	session := value.Session
	if session.APIURL != apiURL {
		return fmt.Errorf("the browser login for profile %s was issued for %s, not %s; run `convai auth login --profile %s` for this API", name, session.APIURL, apiURL, name)
	}
	issuer, trusted := oauth.IssuerFor(a.environments, apiURL)
	if !trusted || session.Issuer != issuer || session.ClientID != oauth.ClientID || session.Audience != oauth.Audience {
		return fmt.Errorf("the browser login for profile %s does not match this CLI's trusted sign-in configuration; run `convai auth login --profile %s`", name, name)
	}
	return nil
}

func (a *application) needsRefresh(value credential.Credential) bool {
	return value.Session.ExpiresAt.Sub(a.now()) < refreshSkew
}

// refresh rotates the stored grant while holding the cross-process lock for
// the keychain entry. It re-reads under the lock so a concurrent process's
// rotation is reused instead of replaying the consumed refresh token.
func (a *application) refresh(ctx context.Context, name, apiURL string) (credential.Credential, error) {
	unlock, err := a.lockCredential(ctx, name)
	if err != nil {
		return credential.Credential{}, err
	}
	defer unlock()
	current, err := a.credentialStore.Get(name)
	if err != nil {
		return credential.Credential{}, fmt.Errorf("re-read credential for profile %s: %w", name, err)
	}
	if !current.Managed() {
		// Another process replaced the login with an imported credential.
		return current, nil
	}
	if err := a.checkBinding(name, current, apiURL); err != nil {
		return credential.Credential{}, err
	}
	if !a.needsRefresh(current) {
		return current, nil
	}
	session := current.Session
	client, err := oauth.Discover(ctx, a.httpClient, session.Issuer, a.now)
	if err != nil {
		return credential.Credential{}, fmt.Errorf("refresh browser login for profile %s: %w", name, err)
	}
	tokens, err := client.Refresh(ctx, session.RefreshToken, session.Scopes)
	if err != nil {
		if oauth.InvalidGrant(err) || errors.Is(err, oauth.ErrInvalidResponse) {
			// The refresh token is known to be consumed or dead; never present it again.
			discardErr := a.credentialStore.Delete(name)
			if discardErr != nil && !errors.Is(discardErr, credential.ErrNotFound) {
				return credential.Credential{}, fmt.Errorf("the browser login for profile %s is no longer valid (%w), and removing it failed: %w; run `convai auth login --profile %s`", name, err, discardErr, name)
			}
			return credential.Credential{}, fmt.Errorf("the browser login for profile %s is no longer valid and was removed (%w); run `convai auth login --profile %s`", name, err, name)
		}
		// The request may have reached Auth and rotated the grant. The stored
		// token is kept and not retried now; if Auth did consume it, the next
		// refresh receives invalid_grant and removes it.
		return credential.Credential{}, fmt.Errorf("refresh browser login for profile %s: %w; it was not retried, and if the next command reports that the login is no longer valid, run `convai auth login --profile %s`", name, err, name)
	}
	next, err := credential.NewSession(tokens.AccessToken, credential.Session{
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    tokens.ExpiresAt,
		Scopes:       tokens.Scopes,
		Issuer:       session.Issuer,
		ClientID:     session.ClientID,
		Audience:     session.Audience,
		APIURL:       session.APIURL,
	})
	if err == nil {
		err = a.credentialStore.Set(name, next)
	}
	if err != nil {
		// The previous refresh token is consumed. Revoke the unsaved pair and
		// drop the dead entry rather than leaving either one usable or retried.
		failure := fmt.Errorf("save refreshed browser login for profile %s: %w", name, err)
		if cleanup := client.RevokeAll(context.WithoutCancel(ctx), tokens); cleanup != nil {
			failure = fmt.Errorf("%w; revoking the unsaved login failed: %v", failure, cleanup)
		}
		if discardErr := a.credentialStore.Delete(name); discardErr != nil && !errors.Is(discardErr, credential.ErrNotFound) {
			failure = fmt.Errorf("%w; removing the consumed login failed: %v", failure, discardErr)
		}
		return credential.Credential{}, fmt.Errorf("%w; run `convai auth login --profile %s`", failure, name)
	}
	return next, nil
}

// revokeSession withdraws a stored browser grant. Every binding is checked
// first: a record for another client or audience would get an RFC 7009
// "unknown token" acknowledgment, which must not be reported as revocation.
// Its refresh token is sent only to the issuer trusted for its bound API.
func (a *application) revokeSession(ctx context.Context, name string, value credential.Credential) error {
	session := value.Session
	if err := a.checkBinding(name, value, session.APIURL); err != nil {
		return fmt.Errorf("%w; it cannot be revoked by this CLI, so use `convai auth logout --local` to remove it", err)
	}
	client, err := oauth.Discover(ctx, a.httpClient, session.Issuer, a.now)
	if err != nil {
		return err
	}
	return client.RevokeAll(ctx, oauth.Tokens{AccessToken: value.Value, RefreshToken: session.RefreshToken})
}

func (a *application) lockCredential(ctx context.Context, name string) (func(), error) {
	locker := a.locker
	if locker == nil {
		dir, err := credential.DefaultLockDir()
		if err != nil {
			return nil, err
		}
		locker = credential.FileLocker{Dir: dir}
	}
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	return locker.Lock(ctx, name)
}

func (a *application) trustedAPIs() string {
	urls := make([]string, 0, len(a.environments))
	for _, environment := range a.environments {
		urls = append(urls, environment.APIURL)
	}
	return strings.Join(urls, ", ")
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

func stdinIsTerminal(stdin io.Reader) func() bool {
	return func() bool {
		file, ok := stdin.(*os.File)
		return ok && term.IsTerminal(int(file.Fd()))
	}
}
