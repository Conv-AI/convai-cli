# Convai CLI

`convai` is a standalone client for Convai REST APIs v2. It uses a generated
OpenAPI client for wire contracts and hand-authored commands for stable,
safe developer workflows.

The CLI does not call the hosted MCP server. REST remains authoritative for
authorization, entitlements, validation, ownership, and writes.

> Status: private pre-release. No public binary version or installer is published.

## Commands

```text
convai auth login|status|logout
convai config get|set|use
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

## Build

Go 1.27.0 or newer:

```bash
make generate
make verify
./bin/convai --help
```

## Configure Staging

Create a staging profile:

```bash
convai config set api-url https://api2-stg.convai.com --profile staging
convai auth login --profile staging
convai config use staging
convai doctor
```

Interactive credentials are stored in the operating-system keychain. Supported
credentials are API keys, scoped PATs (`cvpat_`), and already-issued OAuth access
tokens (`cvoat_`). PAT/OAuth use Bearer authentication; API keys use
`CONVAI-API-KEY`. OAuth refresh tokens, authorization codes, and Web SDK JWTs are
not accepted. Browser login and automatic OAuth refresh are not implemented.

For CI, inject a secret as `CONVAI_TOKEN` through the environment:

```bash
export CONVAI_TOKEN="cvpat_..."
convai character list --output json
```

Do not pass credentials as command arguments or commit them to configuration.
`auth logout` removes the local keychain credential, not the server-side grant.
Revoke grants through their issuing service. Environment credentials continue to
take precedence until the environment variable is removed.

Select a Workspace explicitly or persist it in the profile:

```bash
convai config set workspace-id WORKSPACE_UUID
convai character list --all --sort last_updated
convai character get CHARACTER_ID --workspace personal
```

Unsupported Workspace/resource combinations fail rather than silently using
Personal Workspace. Workspace selection never bypasses server authorization.
`--workspace` is command-scoped, including during login verification; only
`config set workspace-id` changes the saved Workspace selection.

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

## Releases

Tags matching `v*` publish checksummed GitHub Release archives for macOS and
Linux on amd64/arm64 and Windows on amd64. The release workflow also publishes
an SPDX JSON SBOM and GitHub build-provenance attestations.

Homebrew, WinGet, OCI, and `conv-ai/setup-convai` remain planned distribution
channels. They require their own repositories or publishing credentials and
are intentionally not represented as active channels yet.
