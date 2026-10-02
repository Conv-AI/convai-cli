# API Contract

`character.openapi.json` is the pinned Character REST API contract used to
generate the low-level Go client. It is copied byte-for-byte from Character API's
reviewed `contracts/openapi/character-mcp.v1.json` at commit
`610418a1b3ec3a1b3d60fa24a9300bbeb753890b`. `owner.lock.json` records the exact
repository, commit, path, and SHA-256; loading the catalog verifies the checksum.

`catalog.yaml` is the reviewed command catalog. It selects operations for the
hand-authored CLI. The owner artifact supplies approval, scopes, safety, and
wire-schema constraints; authorization remains in REST. The catalog selects 38
operations plus four association commands mapped to `character_update`.

Regenerate the client after updating the contract:

```bash
make generate
```

Generation derives SDK selection and dispatch from the catalog. A disposable
`.tools/character.codegen.json` adds only Go-specific annotations: deterministic
identifiers for colliding snake/camel aliases and string timestamps. No wire
fields or validation constraints are removed from the pinned owner artifact.
Timestamps are displayed verbatim because legacy REST responses may omit an
offset; the CLI does not infer their timezone. The original schemas, including
`$ref` siblings, are used directly for local JSON Schema validation.
Successful responses are validated against the status-specific owner schema
before decoding/output, including required fields, types and reference branches.
Response `format` remains an annotation (not an assertion), preserving the
documented legacy timestamp behavior; input formats are asserted for fast feedback.

`make contract-check` compares the reviewed operations to staging OpenAPI. CI
and a daily scheduled workflow fail on any selected wire-contract change:
parameters, serialization, request/response schemas and encodings, defaults,
headers, errors, and all owner tool metadata. Reachable local component references
are compared in full, preserving reference siblings and unknown keywords;
recursive schema graphs are visited once. Missing/external references, dynamic
references, and duplicate selected operation IDs fail closed pending explicit
contract support.
It also compares effective OpenAPI authentication requirements (including root
inheritance and explicit operation overrides) and referenced security schemes.
Changed API-key headers or Bearer schemes require review. Alternative and scope
ordering are ignored; other scheme changes are conservatively checked in full.
Effective parameters merge Path Item inheritance and operation overrides by
name and location, resolving local parameter references. A reviewed route
moving behind a Path Item reference fails closed pending a contract refresh.
Effective server declarations follow [OpenAPI operation, Path Item, then root precedence](https://spec.openapis.org/oas/v3.1.0.html#operation-object),
including variables and the default `/` origin-relative server. Changes require
review; the CLI never automatically changes its configured origin or forwards
credentials to a host advertised by an upstream contract.
Tradeoff: even compatible additive schema changes (and schema annotation edits)
require a reviewed pin refresh. This is deliberately a drift gate, not a partial
semantic compatibility solver. Operation-level prose/tags, parameter ordering,
equivalent inheritance, and unrelated routes/components do not cause failures.
The gate is not a substitute for E2E authorization or owner business-rule tests.
Enforcement of custom keywords and business policy remains in REST rather than
a separate CLI policy.

To refresh: select an immutable owner commit, copy its reviewed artifact, update
the lock checksum/provenance, regenerate, review the complete diff, and run native
tests and staging E2E. Never replace the pin automatically with a mutable live
download or bypass a failed drift check.
