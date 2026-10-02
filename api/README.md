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

`make contract-check` compares the reviewed operations to staging OpenAPI. CI
and a daily scheduled workflow fail on withdrawn approval, changed scopes or
safety, removed routes/inputs/success outputs, or tightened input constraints.
Complex schema changes conservatively require review; this structural gate is
not a substitute for E2E authorization or owner business-rule tests.

To refresh: select an immutable owner commit, copy its reviewed artifact, update
the lock checksum/provenance, regenerate, review the complete diff, and run native
tests and staging E2E. Never replace the pin automatically with a mutable live
download or bypass a failed drift check.
