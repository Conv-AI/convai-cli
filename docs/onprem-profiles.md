# On-Prem Profiles

The native CLI calls the owner REST API directly. Codex CLI and Claude Code can
run that executable through ordinary shell tools, with local network reachability
and user-authorized execution. No MCP adapter, hosted connector, public relay,
or additional entitlement implementation is involved.

## Approved Personal-Key Profile

Obtain the approved API origin, Workspace UUID and CA file from the release
owner. This example is synthetic, not a known customer deployment. The PEM file
must contain CA certificates only, at an absolute local path; it adds trust to
system roots while preserving TLS certificate and hostname verification.

```yaml
api_url: https://api.customer.example:8443
credential_type: api_key
workspace_id: 11111111-1111-4111-8111-111111111111
ca_bundle: /absolute/path/customer-ca.pem
```

```bash
convai config import --profile onprem --file approved-profile.yaml
convai config get --profile onprem --output json
convai auth login --profile onprem --method token
convai auth status --profile onprem --check
convai character list --profile onprem --output json
```

Windows paths must also be absolute, for example `C:/Convai/customer-ca.pem`.
Omit `ca_bundle` when server certificates use system trust. Profile files contain
metadata, never keys, ID tokens, passwords or client secrets. Unknown fields,
duplicate keys and additional YAML documents fail. Imported keys/PATs/access
tokens use the existing OS credential store; imported access tokens are not
automatically refreshed. CI may supply `CONVAI_TOKEN` from its approved secret
store without putting it into flags, prompts or shell history. It overrides the
keychain and is never refreshed. Raw LM/SSO ID tokens, runtime tokens, codes and
refresh tokens are not authoring credentials. Use individual Personal keys for
approved human access, not shared machine credentials to bypass removed users.

## Approved Refreshable OAuth Profile

Only after the Auth owner supplies a portable OAuth backend, its exact issuer,
approved resource URI and dedicated public `convai-cli` registration, add this
explicit mapping. Nothing is inferred from the API host.

```yaml
api_url: https://api.customer.example:8443
credential_type: oauth_access_token
workspace_id: 11111111-1111-4111-8111-111111111111
ca_bundle: /absolute/path/customer-ca.pem
oauth:
  api_url: https://api.customer.example:8443
  issuer: https://login.customer.example
  resource: https://api.customer.example:8443/authoring
```

```bash
convai config import --profile onprem --file approved-oauth-profile.yaml
convai auth login --profile onprem --read-only
convai auth status --profile onprem --check
convai doctor --profile onprem
convai auth logout --profile onprem
```

The repeated OAuth `api_url` binds approval to one canonical HTTPS origin and
port. Custom mappings cannot replace fixed SaaS mappings. This slice supports
origin-only HTTPS issuers and same-origin endpoints, not arbitrary OIDC
deployments with path-based issuers or separate token hosts. The exact resource
URI is sent on authorization, code exchange **and refresh**. The public client
has no secret and uses PKCE S256 and
`http://127.0.0.1:<random-port>/callback`. The backend must support that registered
loopback path with dynamic ports, RFC 8414 discovery, authorization-code and
rotating refresh grants, scoped opaque Convai `cvoat_`/`cvort_` credentials, and
RFC 7009 revocation. Browser CA trust must be provisioned separately; a CLI CA
file does not change browser certificate trust.

Explicit ports must be numeric and in `1..65535`. Empty, zero, malformed and
out-of-range ports are rejected before origin canonicalization. IPv6 addresses
must use valid bracketed authorities. API origins normalize case and numeric
ports (for example `:08443` becomes `:8443`, and HTTPS `:0443` is omitted);
HTTP loopback `:0080` is also omitted. Protected SaaS mappings cannot be replaced
through alternate port spellings. Approved OAuth mapping fields must already
use their canonical origins; they are never silently rewritten.

Tradeoff: older noncanonical origin-bound credentials or trust profiles may be
rejected and need owner re-review, corrected metadata and re-login. The
CLI does not silently rebind credentials or grandfather a custom issuer on a
protected SaaS origin. Healthy canonical profiles are unchanged. No credential
is changed by metadata import; invalid trust may also prevent remote logout.
For local cleanup even with invalid/malformed metadata or an obsolete stored
record, explicitly run `convai auth logout --local --profile NAME`. This validates
the name and deletes only that OS account's named credential under its existing
lock, without loading profiles, decoding the credential or sending HTTP. It
does not change metadata or environment credentials, and does not revoke the
server grant; arrange remote revocation through the authorized owner. Remote
logout still requires valid metadata and the original approved binding.

`config import`, `config set` and ordinary profile selection validate the entire
existing configuration. Re-import cannot repair an already-invalid file. Close
other CLI processes, back up the non-secret configuration at the OS user config
directory's `convai/config.yaml`, and have the authorized owner correct/remove
invalid profiles while preserving other approved profiles and a valid
`current_profile`. Review origin/issuer/resource/CA approval again, then import
only the corrected canonical profile and re-login. Do not copy/rewrite old
credential records, weaken validation or restore the unapproved SaaS mapping.
Local cleanup does not perform this metadata recovery, and neither recovery nor
re-import is server revocation.

The managed keychain record binds API origin, exact issuer, client, resource,
audience and configured CA file contents (SHA256). Changed bindings fail before
access, refresh or revocation secrets are sent. Discovery cannot substitute an
issuer, return off-origin endpoints or redirect requests. The CLI does not
decode opaque tokens to invent identity/audience validation: optional `iss` and
`resource` response assertions must match, but token issuance/introspection,
audience/scope enforcement and eligibility remain backend duties.

Log out before changing trust while the old approved mapping/CA is available.
Import does not change credentials or revoke old grants. Restore the approved
old profile to revoke an existing session, or explicitly use `auth logout
--local --profile NAME` and arrange server-side revocation through the authorized owner.
Changing even CA file contents requires re-login for managed OAuth; system trust
remains controlled by the OS administrator. Trust changes during consent or
refresh reject persistence and revoke newly issued pairs using the original
validated transport. Rotation uses the existing cross-process credential lock;
network failures are not retried automatically.

SaaS staging/Preview/Production mappings remain unchanged: fixed Convai issuers,
omitted `resource`, and the legacy `convai-api` audience.

## Dependencies And Activation Gates

Authority: [On-Prem MCP and CLI delivery](https://convaixr.atlassian.net/wiki/spaces/Engineerin/pages/568229892)
and [AD RFC](https://convaixr.atlassian.net/wiki/spaces/Engineerin/pages/556728326).
The [portable Auth source foundation](https://github.com/Conv-AI/ConvAI-Authentication/pull/103)
is deliberately unwired; it is not an enabled HTTP OAuth service or a verified
SSO/AD-policy adapter.

Local mocks prove CLI protocol behavior only. They do not activate on-prem
OAuth or certify a customer environment. Auth must provide reviewed portable
persistence/identity and registration. Resource owners must provide portable
introspection/service authentication and enforce issuer, audience, scope,
revocation and authorization. The AD owner controls verified subject linkage,
application/personal authoring eligibility, reconciliation, provisioning policy
and agreed outage/removal rules. SSO or CLI login success is not AD readiness.

Customer acceptance separately needs exact approved artifacts/contracts,
network/browser trust, actual registration, eligible/ineligible actors,
cross-Workspace denials, refresh rotation/replay, logout and post-removal denials
through REST, CLI and supported clients. Empty character results are not proof
of ownership or deployment.

## Brave Callback Diagnostics

Recorded SaaS runs completed authentication despite Brave showing
`ERR_BLOCKED_BY_CLIENT`. That message alone does not prove OAuth failure. The
CLI callback is a single-use plain-text receipt with no redirects, scripts,
external resources or echoed code. A regression test verifies that receipt;
no verified CLI-source defect or Brave root cause was established here.

1. Return to the terminal. Completed login followed by `auth status --check`
   distinguishes CLI authentication from browser display failure. A timeout
   says no credential was stored; do not infer success from the page.
2. Record Brave version, OS, time, terminal outcome, affected origin/path
   (without query), and Shields/extension policy. Never share callback queries,
   codes, state, tokens or unredacted HAR.
3. With local security approval, compare the printed authorization URL in a
   supported clean browser while the CLI waits. Do not replay a callback code
   or change its redirect host/path to evade browser protections.
4. Use Brave's [Shields debugging guide](https://github.com/brave/brave-browser/wiki/Shields-Debugging-Guide)
   and [site-specific settings](https://support.brave.com/hc/en-us/articles/360023646212-How-do-I-configure-global-and-site-specific-Shields-settings)
   for browser-side evidence. Do not disable protections globally or relax CLI
   TLS/issuer/state checks. Customer policy changes require customer approval
   and a reproducible attended test.

No live login, customer credential or browser-policy change is required for
offline regression tests.
