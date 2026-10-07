# CLI Verification

## Offline Checks

```bash
make verify
make vuln
make contract-check
```

Tests exercise every newly selected operation's command, method, route, distinct
resource IDs, exact query/body bindings, Workspace injection, auth header, local
schema rejection, mutation confirmation, and KB/function association wire
mapping. Separate matrices cover API key/PAT/OAuth headers and
HTTP 401/403/404/409/422/429/502, request IDs, secret redaction, redirect rejection,
legacy timestamps, and null/false/empty update preservation. Drift tests include
request-level `$ref` siblings, recursive/missing references, parameter
serialization, unknown owner rules, removed constraints, and additive changes.
All command paths test the same response limit, malformed/null/empty responses,
body closure, redacted request IDs, and single-request/no-retry behavior.
Successful HTTP statuses must be explicitly declared by the selected operation;
an unexpected `202` is not mutation completion. Successful payloads must match
their status-specific owner schemas, not merely parse as JSON. Response formats
are annotations, retaining legacy string timestamps without timezone inference.
Pagination tests cover optional
totals, full/short/empty pages, changing total presence, offsets, and request caps.
Typed list/get/create and catalog commands validate the same original owner
schemas before any HTTP request. Tests reject malformed IDs/version selectors,
invalid pagination/sorting, invalid create fields, and Workspace conflicts with
zero outbound requests. Accepted version selectors are forwarded unchanged.
Create and catalog file inputs share a bounded parser that rejects trailing
JSON/YAML documents rather than silently executing only the first document.
Precision regressions preserve large integers in file-to-wire bindings,
typed and catalog response maps, and JSON/YAML/table output. Validation and
contract comparison decode numeric lexemes rather than rounding through floats.
YAML input rejects scalars the YAML-to-JSON converter cannot represent exactly;
use JSON for arbitrary-precision numbers. YAML output emits the JSON syntax tree
without converting numeric scalars to floats.
Generation is checked for committed reproducibility.
The E2E package's offline tests also validate credential isolation, fail-closed
fixture requirements, Auth PAT issuance/revocation bindings, revocation of both
OAuth tokens even when one fails, post-revocation refresh denial and cleanup of
unexpectedly refreshed credentials, bounded/redacted management errors, staging-only
OAuth discovery, and single-use state-checked loopback callbacks. These run in
the normal CI `make verify`; they do not need live secrets or browser consent.

These tests verify client behavior; they do not prove live entitlement policy.

### Browser login

Browser login is tested offline against a local TLS Auth stub and a local REST
stub, through the real `convai` command tree with injected browser, clock,
interactivity, lock-directory, and trusted-environment seams. The stub enforces
the `convai-cli` client, absence of `resource`, PKCE S256, single-use codes,
rotating single-use refresh tokens, paired revocation, and active-token
introspection; the production trust checks are not relaxed for it. Tests use
`t.TempDir()` lock directories, in-memory or mock keychains, and no network
beyond loopback.

Coverage includes login, a REST request, expiry refresh and rotation, status,
and logout revocation; browser-launch failure with the printed URL;
noninteractive, custom-API, and invalid-flag rejection before any request;
denial, timeout, and cancellation; scope defaults, `--read-only`, explicit and
server-narrowed scopes, and overbroad grants; the managed verification policy
(401 revokes, 403 warns, no check without `character:read`) versus strict token
import; keychain-write, config-save, and rollback failures; replacing a previous
session, including failed revocation and failure after revocation; API-origin
binding and mismatched issuer, client, or audience records; logout failure and
`--local`; configuration changes during consent made through real `config set`
and `config use` commands (merged), conflicting API URL changes and profile
removal (rejected and revoked), and concurrent configuration writers on
different profiles alongside a login (no lost update), all on a temporary-file
configuration with its real lock; `invalid_grant`, oversized, truncated, and unsaved refresh responses
with no later replay; an ambiguous transport failure; concurrent CLI instances
refreshing once; `auth status --check` and `doctor` refresh; `CONVAI_TOKEN`
precedence without refresh; and no mutation replay after HTTP 401. Every
invocation is checked for issued tokens and codes in stdout, stderr, and errors.

Protocol tests cover the trusted environment mapping, the RFC 7636 PKCE vector,
discovery issuer and endpoint trust and redirect rejection, callback method,
path, `Host`, state, duplicate-parameter, issuer, denial, and replay handling,
token-response validation (type, lifetime bounds including int64 overflow,
duplicates, trailing data, scopes), revocation of every token in a rejected
successful response, and redaction of request secrets from error codes,
descriptions, and transport errors before truncation. Credential tests cover the
versioned keychain record through the go-keyring mock, legacy raw entries,
configuration load, save, and lock through symlinked file and directory paths
(one canonical file, symlink preserved, dangling links rejected),
strict record decoding, and a child-process test proving the file lock blocks
another process for the same profile, case-insensitively.

## On-Prem Offline Coverage

`TestOnPrem*` exercises production custom-profile selection (no injected trust
mapping) against TLS Auth/REST mocks with an approved CA. Coverage includes
strict profile import and readback; resource on authorization/code/refresh;
login/read/doctor/status/rotation/logout; wrong origin/issuer/audience/resource/
client/CA bindings; changed issuer/resource/CA configuration; trust changes
during consent and refresh with unsaved-grant revocation; and Personal-key
import through direct REST with raw ID/runtime/refresh token rejection.
Transport tests reject unknown CAs, wrong hostnames, invalid/missing/oversized
bundles, key material, insecure certificate settings and hostname substitution.
Existing SaaS lifecycle/rotation/concurrency/secret-redaction tests remain intact.
Equivalent profile origin spellings preserve canonically bound refresh sessions
in SaaS and custom profiles. Port regressions cover numeric bounds/empty ports,
IPv6, leading-zero default/custom ports, all three protected SaaS mappings,
persisted import/config-set rejection and obsolete stored-record rejection.
Explicit `auth logout --local --profile NAME` recovery tests cover malformed,
missing and invalid metadata, malformed stored records, missing entries, invalid
names and failed deletion, with no credential reads/writes or HTTP. Remote
logout and ordinary profile commands still fail closed on invalid metadata.
These tests use synthetic memory stores, not the real OS keychain or grants.
`.gitattributes` preserves the checksum-pinned OpenAPI snapshot's LF
bytes during Windows checkout; checksum validation is not relaxed.
The loopback receipt regression proves no redirect/script/secret echo; it does
not reproduce Brave policy or establish a browser root cause.

`make release-check` and the native archive CI matrix verify all five archive
checksums/contents and the installed native executable without publication.
Archive regression tests reject duplicate/unexpected/traversing paths and
invalid checksum manifests, and keep the temporary binary separate from Linux
XDG configuration paths. These source and snapshot tests do not prove live
on-prem OAuth, AD authorization, published artifact provenance, customer
installation or both CPU architectures on every supported OS.

## Opt-In Staging E2E

Inject `CONVAI_E2E_TOKEN` from a local secret store or CI secret, not a command
argument. The harness builds the actual executable, isolates configuration in a
temporary HOME, pins `https://api2-stg.convai.com`, and checks that diagnostics do
not expose the credential.

```bash
make e2e
CONVAI_E2E_MUTATIONS=1 make e2e
```

The read test verifies authentication, owned-character list/get, voice/language/
model discovery, versions, and HTTP 401 for an invalid PAT. The mutation test
creates only temporary fixtures, updates and reads them back, releases/promotes
version 1.0 and verifies latest, clones, and deletes both characters in cleanup.
It does not modify account plans, DB rows directly, existing characters, or live
environments outside staging. Failed cleanup is a test failure.

Additional fixtures enable real server-side negative gates:

| Environment variable | Fixture requirement |
| --- | --- |
| `CONVAI_E2E_READ_ONLY_PAT` | Valid PAT for the primary account permitting character read but not write; expects HTTP 403 for a valid update to a disposable character and verifies unchanged owner readback. Requires mutation opt-in for fixture creation. |
| `CONVAI_E2E_FOREIGN_CHARACTER_ID` | Existing character owned by a different account; expects HTTP 404. |
| `CONVAI_E2E_FORBIDDEN_WORKSPACE` | Existing Workspace inaccessible to the primary token; expects HTTP 403/404. |
| `CONVAI_E2E_FOREIGN_OWNER_TOKEN` | Credential that can get the foreign character in the specified Workspace and list that Workspace. Both must succeed before a denial counts as ownership evidence. |
| `CONVAI_E2E_UNENTITLED_TOKEN` | Separately provisioned account whose plan denies Character APIs; expects HTTP 403. |
| `CONVAI_E2E_WORKSPACE` | Authorized Workspace UUID for the positive read/mutation lifecycle; default is Personal Workspace. |

Exploratory `make e2e` runs skip explicitly when fixtures are absent. They are
not a release gate. Set `CONVAI_E2E_REQUIRED=1` to fail missing fixtures in the
selected tests. Denials require actual HTTP status evidence and matching process
exit status, not merely a nonzero CLI result. API-key success does not validate
PAT or OAuth behavior.

## Managed Credential Matrix

Two tests provision fresh credentials through staging Auth and execute the
actual CLI against staging REST. They never insert credentials directly in a DB
or use an administrative minting bypass:

- `TestStagingPATCredentialMatrix` requires a fresh Firebase ID token in
  `CONVAI_E2E_FIREBASE_ID_TOKEN`. The PAT management API requires Firebase
  authentication; an API key, OAuth token, or Web SDK token is not a substitute.
- `TestStagingOAuthCredentialMatrix` requires `CONVAI_E2E_OAUTH=1` and two manual
  browser consents. The harness prints the staging authorization URL, opens a
  random loopback callback, and exchanges the code using PKCE S256, client
  `codex`, and resource `https://mcp-api-stg.convai.com/mcp`.

Both require `CONVAI_E2E_MUTATIONS=1` and a primary API key in `CONVAI_E2E_TOKEN`.
The primary account must differ from the account issuing the managed credentials;
both need Character API access and enough temporary-character capacity. The
harness creates a primary-owned foreign fixture and checks concrete owner user
and Workspace IDs. It fails if accounts coincide, rather than treating a random
nonexistent UUID as a foreign character. The managed account's Personal Workspace
must not have access to the primary account's fixture Workspace.

For each credential type the matrix verifies:

1. Fresh `character:read character:write` credential: auth status, list/get,
   versions, create/update/readback/release/promote/clone/delete lifecycle.
2. Fresh `character:read` credential: reads pass, a valid write to a disposable
   same-account character returns 403, and owner readback remains unchanged.
3. Both credentials: real foreign character returns 404 and a verified
   inaccessible Workspace returns 403/404.
4. OAuth refresh: the fresh access credential verifies successfully, then a
   refresh grant must succeed and rotate both credentials. The CLI matrix uses
   the rotated access token. This proves the refresh token was usable, not an
   already-invalid fixture that would trivially pass a negative probe.
5. Cleanup: characters are deleted and owner get verifies 404 while write
   credentials are valid, then PATs or OAuth access and refresh tokens are
   revoked through Auth. Reusing each revoked access credential must return
   401 and process exit code 3. Each OAuth response must include a refresh token;
   the original and rotated pairs are both revoked. Refreshing either after
   cleanup must return 400 with `error=invalid_grant`.
   A revocation acknowledgment alone is insufficient. Unexpected refreshed
   credentials are revoked before failing the run.

Run one matrix while preparing fixtures; missing fixtures must fail:

```bash
CONVAI_E2E_REQUIRED=1 go test -count=1 -timeout=30m -v \
  -run '^TestStagingOAuthCredentialMatrix$' ./tests/e2e
```

The complete credential release gate preflights every prerequisite before
creating any fixture, then runs both matrices without optional skips:

```bash
make e2e-auth
```

Inject secrets from the local secret store into the environment without putting
them in shell history, command arguments, test output, or checked-in files. The
child CLI receives only its selected credential, not the Firebase management
token or other `CONVAI_E2E_*` fixtures. No credentials are written to config or
keychain. Tokens remain in process memory. This is a developer-attended staging
gate, not an unattended GitHub job: browser consent and a fresh management login
are prerequisites, not long-lived credentials to hardcode into CI.

Cleanup failures fail the run. Preserve sanitized test results, and investigate
temporary fixtures before retrying if the process is killed, the callback times
out, or Auth omits a usable revocation ID. Go cleanup callbacks cannot guarantee
cleanup after process termination. This harness uses the staging `codex` test
client to issue imported tokens; it does not exercise the `convai-cli` browser
login, publishing, or account-plan mutation.

## Evidence and Remaining Gates

On 2026-10-01, the API-key staging read/authentication and authoring lifecycle
passed, including fixture cleanup. The first read uncovered naive timestamp
responses, now preserved verbatim with a regression test. On 2026-10-02, an
owner-issued read-only OAuth credential passed CLI authentication/list/get/version
reads; after access/refresh revocation, the CLI returned 401 with exit code 3.
The writable OAuth and managed PAT matrices remain unverified until their live
runs pass. PAT issuance currently lacks a fresh Firebase management credential.
The strict release gate must remain red until both matrices pass. Unentitled-plan
testing remains a separate fixture-dependent gate; these tests do not change
plans. Public releases/installers and local stdio/plugin packaging remain
separate work.

Browser login is implemented and verified offline only. Its activation gates,
in order: the `convai-cli` client registration is applied to staging Auth; a
developer-attended staging customer flow passes on a real account (`auth login`,
`auth status --check`, a character read and disposable write, forced refresh,
`auth logout`, then HTTP 401 for the revoked access token and `invalid_grant`
for the revoked refresh token); then Preview and production registration and
their own checks. Until the staging flow passes, documentation must not present
browser login as available.
