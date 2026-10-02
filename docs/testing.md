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
OAuth tokens even when one fails, bounded/redacted management errors, staging-only
OAuth discovery, and single-use state-checked loopback callbacks. These run in
the normal CI `make verify`; they do not need live secrets or browser consent.

These tests verify client behavior; they do not prove live entitlement policy.

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
4. Cleanup: characters are deleted and owner get verifies 404 while write credentials are valid, then
   PATs or OAuth access and refresh tokens are revoked through Auth. Reusing
   each revoked access credential must return 401 and process exit code 3.

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
cleanup after process termination. This harness does not implement product CLI
browser login, token refresh, publishing, or account-plan mutation.

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
plans. Product browser OAuth acquisition/refresh, public releases/installers,
and local stdio/plugin packaging remain separate work.
