# Convai CLI

`convai` is a standalone client for Convai REST APIs v2. It uses a generated
OpenAPI client for wire contracts and hand-authored commands for stable,
safe developer workflows.

The CLI does not call the hosted MCP server. REST remains authoritative for
authorization, entitlements, validation, ownership, and writes.

Native beta binaries are published through [GitHub Releases](https://github.com/Conv-AI/convai-cli/releases).
See [Release Verification](docs/releases.md) for supported platforms and verification.

For explicit private API/OAuth mappings and customer CA setup, see
[On-Prem Profiles](docs/onprem-profiles.md). Portable on-prem OAuth and backend
eligibility enforcement are external dependencies, not activated by a profile.
API origins reject invalid/empty ports before canonicalizing numeric port
spellings; fixed SaaS mappings cannot be replaced through default-port aliases.
The profile runbook covers owner-reviewed metadata recovery, explicit local
cleanup and re-login limits for older noncanonical bindings.
See [Release Verification](docs/releases.md) for local archive checks and
separate publication/installation gates.

## Commands

```text
convai auth login|status|logout
convai config get|set|use|import
convai character list|get|create|update|delete|clone
convai character version list|release|promote|fork|deprecate|discard-draft|revert|diff
convai voice list
convai language list
convai model list
convai narrative section create|update|delete|get|list
convai narrative trigger create|update|delete|get|list
convai narrative decision create|update|delete
convai narrative start
convai function create|update|delete|get|list|test
convai function template list
convai function-association connect|disconnect
convai kb connect|disconnect
convai doctor
convai version
```

All resource commands support `--output table|json|yaml`. Use command `--help`
for positional IDs and query filters. Resource bodies accept JSON/YAML files or
JSON stdin. Updates, deletes, promotion, draft replacement, and association
changes require a terminal confirmation or `--yes`. No automatic mutation retry
is performed.

## License

Convai CLI is licensed under [Apache-2.0](LICENSE), with attribution in
[NOTICE](NOTICE). Bundled dependencies retain their own licenses, reproduced
in [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt). This license applies to
this CLI repository, not to separate Convai services or trademarks.

## Build From Source

Go 1.27.3 or newer:

```bash
make generate
make verify
./bin/convai --help
```

## Authentication

Create a staging profile:

```bash
convai config set api-url https://api2-stg.convai.com --profile staging
convai auth login --profile staging
convai config use staging
convai doctor
```

### Browser login

> Activation gate: browser login uses the dedicated `convai-cli` OAuth client.
> It works only after that client is registered with Convai Authentication in
> the target environment and the staging customer flow has passed. Until then,
> use token import below.

In an interactive terminal, `convai auth login` opens your browser to the normal
Convai sign-in and consent page, receives the authorization on a one-time
loopback address (`http://127.0.0.1:<random port>/callback`), and stores a
refreshable session in the operating-system keychain. If the browser cannot be
opened, the CLI prints the sign-in URL; it contains no credential. Sign-in
times out after five minutes, and Ctrl+C cancels it without storing anything.

Browser login is available only for the Convai-hosted APIs, each with a fixed
sign-in issuer:

| API URL | Sign-in issuer |
| --- | --- |
| `https://api2-stg.convai.com` | `https://login-stg.convai.com` |
| `https://api2-preview.convai.com` | `https://login-preview.convai.com` |
| `https://api2.convai.com` | `https://login.convai.com` |

Other API URLs support token import, or browser login only with the explicitly
approved mapping and backend dependencies in [On-Prem Profiles](docs/onprem-profiles.md).
By default the CLI requests the scopes
that its commands use, `character:read character:write`. Narrow them with
`--read-only` (`character:read`) or `--scopes character:read`; Convai sign-in may
also grant fewer scopes than requested. The REST API enforces scopes, Workspace
access, ownership, and plan entitlements on every command.

Login is complete when Convai sign-in issues the session. If the session has
`character:read`, login also checks it with a read-only character list: HTTP 401
fails and revokes the new session; other denials, such as a Workspace or plan
403, are reported as a warning because they are command authorization, not
sign-in. `--no-verify` skips the check.

Sessions refresh automatically shortly before the access token expires,
including in `auth status --check` and `doctor`. Refresh is serialized across
concurrent CLI processes for a profile, and a refresh token is never retried after
Convai sign-in has rejected or consumed it. A stored session is bound to its API
URL, issuer, client, and audience: switching the profile or `--api-url` to
another API requires a new login rather than sending the session elsewhere.
`auth status` shows the login type, scopes, issuer, and access-token expiry, never
the tokens.

Configuration changes made in another terminal while you sign in, such as
`config set workspace-id` or `config use`, are kept. If the profile's API URL is
changed or the profile is removed during sign-in, login fails and revokes the
new session instead of overwriting that change.

`convai auth logout` revokes a browser login through Convai sign-in and then
removes it from the keychain. If revocation fails, the session is kept so logout
can be retried; `convai auth logout --local` removes only the local copy and
leaves the session valid until it expires. Logging in again revokes the
previous browser session first.

### Token import and CI

Import an API key, scoped PAT (`cvpat_`), or already-issued OAuth access token
(`cvoat_`) into the keychain:

```bash
convai auth login --method token          # prompts without echo
printf '%s\n' "$TOKEN" | convai auth login --token-stdin
```

PAT/OAuth use Bearer authentication; API keys use `CONVAI-API-KEY`. OAuth refresh
tokens, authorization codes, Web SDK JWTs, and stored browser-login records are
not accepted as credentials.
Imported credentials are not refreshed. Obtain a PAT from its existing issuing
service; the CLI does not create or manage PATs.

For CI, inject a secret as `CONVAI_TOKEN` through the environment. Browser login
is rejected immediately without a terminal.

```bash
export CONVAI_TOKEN="cvpat_..."
convai character list --output json
```

Do not pass credentials as command arguments or commit them to configuration.
`auth logout` removes an imported credential from the keychain only; revoke API
keys and PATs where they were issued. Environment credentials take precedence
over the keychain, including a browser login, until the variable is removed.

Keychain entries written by earlier CLI versions keep working. A browser-login
entry uses a versioned record that earlier CLI versions cannot read; log in again
after downgrading.

Select a Workspace explicitly or persist it in the profile:

```bash
convai config set workspace-id WORKSPACE_UUID
convai character list --all --sort last_updated
convai character get CHARACTER_ID --workspace personal
```

`--all` preserves a supplied total and otherwise continues to a short or empty
page, with a 1000-page request budget. An absent total stays `null` in structured
output (`unknown` in table output), rather than becoming the current page size.

Unsupported Workspace/resource combinations fail rather than silently using
Personal Workspace. Workspace selection never bypasses server authorization.
`--workspace` is command-scoped, including during login verification; only
`config set workspace-id` changes the saved Workspace selection.
`config get`, `auth status`, and `doctor` show the effective Workspace in their
default output, using `personal` when no Workspace UUID is selected.

## Character Workflows

```bash
convai character list
convai character get CHARACTER_ID --output yaml

convai character create \
  --name "Ari" \
  --voice "VOICE_ID" \
  --backstory "A practical systems engineer."
```

Create from JSON, YAML, or stdin:

```bash
convai character create --file character.yaml
cat character.json | convai character create --file -
```

The REST contract requires `charName` and a non-empty `backstory`. `voiceType`
is optional: omitting `--voice` lets REST choose its configured default. The
server validates catalog selections, plan access, ownership, and business rules.

```bash
convai character update CHARACTER_ID --file update.json --yes
convai character version release CHARACTER_ID 1.0
convai character version promote CHARACTER_ID 1.0 --yes
convai kb connect CHARACTER_ID DOCUMENT_ID --yes
convai function-association disconnect CHARACTER_ID FUNCTION_ID --yes
```

Update files preserve omitted fields, explicit nulls, false values, and empty
collections. The CLI validates the reviewed owner JSON Schema before sending.
Version release explicitly keeps `make_latest=false` unless requested in its
input file; `true` requires confirmation. Deprecate explicitly sends
`deprecated=true` unless overridden in its input file.
KB commands modify character associations through Character API; they do not
upload, process, or delete Knowledge Bank documents. Document lifecycle,
multipart backstory generation, chat history, stored external-MCP management,
and runtime/internal operations are outside this command catalog.

With JSON/YAML output, failures are structured on stderr and successes on stdout.
Exit codes: `0` success; `1` local/transport failure; `2` HTTP 422; `3` HTTP
401/403; `4` HTTP 404; `5` HTTP 409; `6` HTTP 429; `7` HTTP 5xx. HTTP failures
include `status_code` and a request ID when supplied by REST.

## Contract Generation

The pinned OpenAPI snapshot and curated command catalog live under [`api/`](api/).
Generated code is never edited manually:

```bash
make generate
git diff -- internal/characterapi/client.gen.go
```

See [`docs/architecture.md`](docs/architecture.md) for the CLI, local MCP, and
distribution boundaries, and [`docs/testing.md`](docs/testing.md) for staging
verification and the remaining credential-dependent release gates.
`make e2e` is exploratory and may skip absent staging fixtures. `make e2e-auth`
is the strict, developer-attended PAT/OAuth release gate: missing prerequisites
fail before issuance, and successful runs must include cleanup and revoked-token
denial. See the testing guide for management-login and browser-consent setup.

## Releases

Tags matching `v*` publish checksummed GitHub Release archives for macOS and
Linux on amd64/arm64 and Windows on amd64. The release workflow also publishes
an SPDX JSON SBOM and GitHub build-provenance attestations.

Homebrew, WinGet, OCI, and `conv-ai/setup-convai` remain planned distribution
channels. They require their own repositories or publishing credentials and
are intentionally not represented as active channels yet.
