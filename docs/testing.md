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
an unexpected `202` is not mutation completion. Pagination tests cover optional
totals, full/short/empty pages, changing total presence, offsets, and request caps.
Generation is checked for committed reproducibility.

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
| `CONVAI_E2E_READ_ONLY_PAT` | Valid same-account PAT permitting character read but not write; expects HTTP 403 for an idempotent update. |
| `CONVAI_E2E_FOREIGN_CHARACTER_ID` | Existing character owned by a different account; expects HTTP 404. |
| `CONVAI_E2E_FORBIDDEN_WORKSPACE` | Existing Workspace inaccessible to the primary token; expects HTTP 403/404. |
| `CONVAI_E2E_UNENTITLED_TOKEN` | Separately provisioned account whose plan denies Character APIs; expects HTTP 403. |
| `CONVAI_E2E_WORKSPACE` | Authorized Workspace UUID for the positive read/mutation lifecycle; default is Personal Workspace. |

Scope/ownership/Workspace/plan tests skip explicitly when fixtures are absent.
They require actual HTTP status evidence, not any nonzero CLI result. Repeat
positive tests with a write-scoped PAT and an OAuth access token through
`CONVAI_E2E_TOKEN`; API-key success alone does not validate those credentials.

## Evidence and Remaining Gates

On 2026-10-01, the API-key staging read/authentication and authoring lifecycle
passed, including fixture cleanup. The first read uncovered naive timestamp
responses, now preserved verbatim with a regression test. Dedicated PAT/OAuth,
foreign ownership, Workspace, and unentitled-plan fixtures were not available;
their live gates are not claimed complete. Browser OAuth acquisition/refresh,
public releases/installers, and local stdio/plugin packaging remain separate work.
