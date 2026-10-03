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
operation catalog selects stable operation IDs and records only command UX:
grouping, arguments, and file inputs. Approval, destructive behavior, and scopes
come from the owner contract, not a duplicate CLI policy.

Command UX is hand-authored. Fully generated CLIs tend to expose unstable
schema names, poor defaults, and unsafe mutation behavior.

## Authentication

Browser-login sessions and imported API keys, PATs, and OAuth access tokens are
stored in the operating-system credential store through:

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
metadata requires it. Confirmation shows resource targets as well as the origin
and Workspace. Every command shares a 16 MiB bounded response reader, with no
automatic retries. Only explicitly declared successful statuses are accepted;
an unexpected queued response is not completion. HTTP errors retain status and request IDs while redacting
the exact supplied credential.
Successful payloads are schema-validated before output; `format` remains an
annotation for response strings so legacy timestamps are preserved verbatim.

### Browser login

`convai auth login` is a public OAuth client of the existing Convai
Authentication service; the CLI adds no authorization server, hosted callback,
or credential minting path.

```text
convai auth login
  -> GET {issuer}/.well-known/oauth-authorization-server (exact issuer, same-origin HTTPS endpoints)
  -> browser: {authorization_endpoint}?client_id=convai-cli&code_challenge=S256(...)&state=...
  -> Convai sign-in and consent (Firebase session stays in the browser)
  -> http://127.0.0.1:<random>/callback?code=...&state=...&iss={issuer}
  -> POST {token_endpoint} code + PKCE verifier -> cvoat_ access + cvort_ refresh
  -> OS keychain (versioned record) -> Character API with Bearer cvoat_
```

- Client: the dedicated predefined public client `convai-cli` (display name
  Convai CLI), registered redirect `http://127.0.0.1/callback`. Auth accepts
  any loopback port for it; the CLI binds a random port and requires the exact
  path, the exact `Host`, one GET, and a 256-bit `state`. PKCE S256 is
  mandatory and there is no client secret. The Codex and MCP client identities
  are not reused.
- Audience: requests omit the optional `resource` parameter, so Auth issues its
  default audience `convai-api`, which Character API already accepts through
  introspection. `convai-api` is not an RFC 8707 URI and is never sent.
- Trust: only the staging, Preview, and production API URLs map to their
  fixed issuers. API URLs from config or `--api-url` are compared in canonical
  form (lower-case host, no trailing slash or scheme-default port). Discovery must return that exact issuer, S256 support, and
  token, authorization, and revocation endpoints on the issuer's HTTPS origin
  without userinfo, query, fragment, encoded path, or another port. OAuth
  requests never follow redirects. Custom API URLs support token import only.
- Scopes: the default is the union of the owner-declared `requiredScopes` of
  the curated command catalog (`character:read character:write`), and
  `--read-only` keeps operations that need only read scopes. Auth's other
  scopes, such as `chat-history:read`, are never requested by default. Grants
  wider than the request are revoked and rejected; narrower grants are stored
  as granted.
- Storage: access token, refresh token, expiry, scopes, and the non-secret
  issuer, client, audience, and API URL binding form one versioned keychain
  record. Raw entries from earlier versions still parse. Config files keep only
  `credential_type: oauth_access_token`.
- Binding: a session is used only when its API URL, issuer, client, and
  audience match the effective profile and trusted mapping; otherwise the
  command fails before any request and asks for a new login. `CONVAI_TOKEN`
  still takes precedence and is never refreshed.
- Refresh: every REST command, `auth status --check`, and `doctor` resolve
  credentials through one path, which rotates a session within two minutes of
  expiry. An OS file lock (`gofrs/flock`) per keychain service and profile
  serializes login, token import, refresh, and logout across processes; it lives
  in `~/.convai/locks` under the operating-system account home, so processes
  with different `HOME` or config directories still share it. The record is
  re-read under the lock, so a concurrent rotation is reused. A refresh token
  rejected or consumed by Auth (`invalid_grant`, or any successful but
  unusable response) is removed and never sent again; an unsaved rotated pair is
  revoked. A transport failure whose outcome at Auth is unknown is reported
  and not retried in that command. An HTTP 401 from REST is returned as-is; no
  request, including a mutation, is replayed.
- Logout and replacement: logout revokes the refresh and access tokens through
  RFC 7009 before deleting the record, after validating every binding; on
  failure the record is kept for retry, and `--local` removes only the local
  copy. A new login revokes the previous browser grant before replacing it and
  keeps it if that revocation fails. Every failure after issuance (invalid
  response, verification 401, keychain or config failure) revokes the new grant
  and reports any rollback failure.
- Verification: a code exchange authenticates the account. A read-only list
  check runs only with `character:read`; it fails login only on HTTP 401.
  Workspace, plan, and scope decisions stay with Character API per command.

Activation requires the `convai-cli` client registration in each
environment's Auth `OAUTH_CLIENTS` (staging first) and a passing staging
customer E2E before Preview and production. The CLI does not provide a PAT
management UI.

The staging test harness may obtain disposable OAuth credentials using the
already-registered staging `codex` client and MCP resource, then inject the
access token into the native REST CLI. This verifies imported-token authoring
and denial behavior; it does not exercise the `convai-cli` browser login. Auth alone issues/revokes credentials, and
Character API remains responsible for scope, Workspace, and ownership checks.
The strict `make e2e-auth` gate is developer-attended and requires both managed
PAT and OAuth matrices, including cleanup and post-revocation denial.

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
token import versus managed browser login, the `convai-cli` client and
`convai-api` audience ownership, strict versus exploratory staging tests, the
native customer E2E, live credential evidence, and publish/install gates. Release
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
