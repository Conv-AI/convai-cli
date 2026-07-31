# API Contract

`character.openapi.json` is the pinned Character REST API contract used to
generate the low-level Go client. The initial snapshot was exported from the
deployed staging service at `https://api2-stg.convai.com/openapi.json` on
2026-07-30.
Its SHA-256 is
`b2d8ca7dc71415ac530cac68976550d49f3cca959be6186420fd20c20c494130`;
the selected operations were also checked against Character API commit
`396b3c870df6e9c7533cd101484c137f103ae7a4`.

`catalog.yaml` is the reviewed command catalog. It selects operations for the
hand-authored CLI and records the intended scopes and safety metadata.

Regenerate the client after updating the contract:

```bash
make generate
```

The Character API repository must eventually publish a versioned OpenAPI
artifact and enforce breaking-change checks. Once available, updates here
should consume that artifact by immutable Character API release version rather
than downloading a mutable live endpoint.
