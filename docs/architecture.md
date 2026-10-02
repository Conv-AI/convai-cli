# Convai CLI Architecture

## Boundary

The CLI is a standalone REST API client. It is not a client of the hosted MCP
server and does not own business logic.

```text
Developer / CI -> convai CLI -> generated Go client -> Convai REST APIs v2
Claude / ChatGPT -> hosted Convai MCP -> Convai REST APIs v2
Claude Code / Codex -> native convai CLI -> generated Go client -> REST APIs v2
Future local stdio adapter -> generated Go client -> REST APIs v2
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

Interactive API keys, PATs, and imported OAuth access tokens are stored in the operating-system credential
store through:

- macOS Keychain
- Windows Credential Manager
- Linux Secret Service

Config files contain profile metadata only. CI supplies `CONVAI_TOKEN`, which
takes precedence over the keychain. Credentials are never accepted as command
arguments.

The Web SDK runtime token is not an account-authoring credential and is not
accepted as a special CLI mode.

Profile metadata contains an API origin and optional Workspace UUID, never a
credential. API origins require HTTPS except loopback development endpoints;
redirects are not followed, preventing credential forwarding across origins.
Mutations have bounded inputs and explicit confirmation where owner safety
metadata requires it. HTTP errors retain status and request IDs while redacting
the exact supplied credential.

The CLI accepts existing audience-valid OAuth access tokens but does not acquire
or refresh them. Browser PKCE login requires a dedicated registered CLI client,
approved loopback redirects, scopes/resources, keychain refresh-token lifecycle,
and revocation tests. Do not reuse the Codex connector client identity. This is a
separate activation gate, not part of an API-key/PAT authoring MVP.

## MCP

`convai mcp serve --transport stdio` will be a local adapter over the same
generated REST client. The hosted Streamable HTTP MCP service remains a
separately deployed integration product and release train.

Neither the local stdio adapter nor Codex/Claude plugin installation is shipped
in this change. Both agents can execute the native CLI as an ordinary shell
command; MCP is optional tool discovery, not a dependency of native commands.

## Architecture Documentation

The Confluence companion is [Convai CLI and Agent Plugins: Architecture and Distribution](https://convaixr.atlassian.net/wiki/spaces/Engineerin/pages/532185115/Convai+CLI+and+Agent+Plugins+Architecture+and+Distribution).
After merge, its current-command/status sections need the expanded catalog,
immutable owner-contract pin, optional default voice, Workspace selection,
OAuth-import versus browser-login boundary, and the test gates below. Release
and installer claims must remain pre-release until publishing is verified.

## Distribution

The intended release artifacts are:

- GitHub Releases for macOS, Linux, and Windows on amd64 and arm64
- Homebrew tap for macOS and Linux
- WinGet package for Windows
- OCI image for CI
- `conv-ai/setup-convai` GitHub Action

Releases use SemVer with stable and beta channels. There is no automatic
self-update in the initial release.
