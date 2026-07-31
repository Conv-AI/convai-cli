# Convai CLI

`convai` is a standalone client for Convai REST APIs v2. It uses a generated
OpenAPI client for wire contracts and hand-authored commands for stable,
safe developer workflows.

The CLI does not call the hosted MCP server. REST remains authoritative for
authorization, entitlements, validation, ownership, and writes.

> Status: private pre-release. Production REST v2 and PAT rollout are not yet
> complete.

## Initial Commands

```text
convai auth login|status|logout
convai config get|set|use
convai character list|get|create
convai doctor
convai version
```

All resource commands support `--output table|json|yaml`.

## Build

Go 1.25.12 or newer:

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

Interactive credentials are stored in the operating-system keychain. For CI:

```bash
export CONVAI_TOKEN="cvpat_..."
convai character list --output json
```

Do not pass credentials as command arguments or commit them to configuration.

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

The REST contract requires `charName`, `voiceType`, and a non-empty
`backstory`. The server validates the voice catalog, plan access, ownership,
and all optional fields.

## Contract Generation

The pinned OpenAPI snapshot and curated command catalog live under [`api/`](api/).
Generated code is never edited manually:

```bash
make generate
git diff -- internal/characterapi/client.gen.go
```

See [`docs/architecture.md`](docs/architecture.md) for the CLI, local MCP, and
distribution boundaries.

## Releases

Tags matching `v*` publish checksummed GitHub Release archives for macOS and
Linux on amd64/arm64 and Windows on amd64. The release workflow also publishes
an SPDX JSON SBOM and GitHub build-provenance attestations.

Homebrew, WinGet, OCI, and `conv-ai/setup-convai` remain planned distribution
channels. They require their own repositories or publishing credentials and
are intentionally not represented as active channels yet.
