# Convai CLI Architecture

## Boundary

The CLI is a standalone REST API client. It is not a client of the hosted MCP
server and does not own business logic.

```text
Developer / CI -> convai CLI -> generated Go client -> Convai REST APIs v2
Claude / ChatGPT -> hosted Convai MCP -> Convai REST APIs v2
Claude Code / Codex -> convai mcp serve -> generated Go client -> REST APIs v2
OpenAPI catalog -> generated Go client, hosted MCP, docs
```

Authorization, entitlements, validation, ownership checks, and writes stay in
REST. The CLI adds local input validation only for fast feedback and safe
command UX.

## Contracts

The low-level client is generated from a pinned OpenAPI snapshot. The reviewed
operation catalog selects stable operation IDs and records command grouping,
stability, destructive behavior, and required PAT scopes.

Command UX is hand-authored. Fully generated CLIs tend to expose unstable
schema names, poor defaults, and unsafe mutation behavior.

## Authentication

Interactive API keys and PATs are stored in the operating-system credential
store through:

- macOS Keychain
- Windows Credential Manager
- Linux Secret Service

Config files contain profile metadata only. CI supplies `CONVAI_TOKEN`, which
takes precedence over the keychain. Credentials are never accepted as command
arguments.

The Web SDK runtime token is not an account-authoring credential and is not
accepted as a special CLI mode.

## MCP

`convai mcp serve --transport stdio` will be a local adapter over the same
generated REST client. The hosted Streamable HTTP MCP service remains a
separately deployed integration product and release train.

## Distribution

The intended release artifacts are:

- GitHub Releases for macOS, Linux, and Windows on amd64 and arm64
- Homebrew tap for macOS and Linux
- WinGet package for Windows
- OCI image for CI
- `conv-ai/setup-convai` GitHub Action

Releases use SemVer with stable and beta channels. There is no automatic
self-update in the initial release.
