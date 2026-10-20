# Release Verification

These source checks do not tag or publish. The tag-triggered release workflow
is a separate authorized operation.

## Audited Archive Matrix

GoReleaser v2.17.1 builds versioned executables with `CGO_ENABLED=0`:

| Target | Archive |
| --- | --- |
| macOS amd64 | `convai_Darwin_x86_64.tar.gz` |
| macOS arm64 | `convai_Darwin_arm64.tar.gz` |
| Linux amd64 | `convai_Linux_x86_64.tar.gz` |
| Linux arm64 | `convai_Linux_arm64.tar.gz` |
| Windows amd64 | `convai_Windows_x86_64.zip` |

Windows arm64 is not configured. Homebrew, WinGet, OCI and setup-action channels
remain proposals, not available installers.

```bash
make verify
make vuln
make contract-check
make release-check
```

`release-check` validates configuration, builds local unpublished snapshots,
and checks all five SHA256 checksums and archive members. It rejects traversal,
duplicate/unexpected members, links, missing binaries and non-executable Unix
binaries. The native archive's executable is installed in a temporary prefix's
`bin` directory, then tested for exact version, help, metadata-only profile
import/readback and missing credentials. Environment/config and profile name
are isolated. It does not log in, write native credentials or make an API request. Temporary installs
are removed.

CI runs native race tests and archive installation checks on macOS, Linux and
Windows, alongside the existing five-target build matrix. Those runners certify
their actual native architecture only; foreign archive checks do not prove
execution on both CPU architectures.

The release workflow checks the upstream owner contract and runs an unpublished
snapshot preflight, then its existing
tag-based GoReleaser publication, source SPDX SBOM upload and GitHub provenance
attestation. Preflight does **not** prove actual published archive download or
successful subsequent SBOM/attestation jobs. A source SBOM is not a per-binary
SBOM. macOS signing/notarization and Windows signing are not configured. No
publishing credentials, tag or customer installation was added.

The SBOM action downloads its uploaded Actions artifact before attaching it to
the release, so the publishing job requires `actions: read` in addition to
release and attestation permissions. A partially uploaded release is not ready
for handoff if this step or provenance fails. Preserve its tag; publish a new
approved version from reviewed source instead of moving the old tag or skipping
the failed security gates.

## Authorized Release Acceptance

Before customer handoff, verify reviewed tag/source SHA, green native/archive/
contract CI, successful SBOM/provenance jobs, and actual downloaded archives
and checksum manifest. Download `metadata.json` as well; the release publishes
and attests it so the same archive verifier can check the exact installed
version after download, not just in a local build directory:

```bash
gh release download v0.1.0-beta.1 --repo Conv-AI/convai-cli --dir downloaded
go run ./tools/verify-release -dir downloaded
```

Run the verifier from the reviewed source revision that built the release.
Independently verify provenance against
`Conv-AI/convai-cli` using the approved verification process; checksums alone do
not establish publisher trust. Confirm OS/CPU/client versions and signing
requirements.

Install only a verified binary into an approved user-local PATH. Compare
`convai version` to the release, inspect `--help`, import an approved profile,
and obtain credentials separately. The installed artifact, not a source build,
must pass TLS/private-network and customer authorization tests. Keep the approved
predecessor archive/checksum/provenance for rollback. Revoke managed sessions
before trust changes and re-login after restoring the predecessor. Older CLIs
cannot read custom-resource session fields; remove these through the newer CLI
before downgrading. There is no self-updater or root-install script.
