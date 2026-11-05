Open Media Gateway 1.0.2 expands media workflows and completes a set of shared
interface and recovery improvements. See the [changelog](https://github.com/Sociaro-Research/open-media-gateway/blob/v1.0.2/CHANGELOG.md).

- Reuse the existing interface with a central company through explicit approval
  and a permanent revocable company key, including published Support contacts.
- Share Requests filters, columns and details across administration and company
  views while preserving company isolation and role-based redaction.
- Improve template authoring, media references, upload cancellation, image/video
  framing, API-key controls, shared typography and accessible interactions.
- Preserve accepted-result recovery, generation admission and active-reference
  retention, with reviewed Kling and MiniMax media workflows.

The company-key connection is supported; the separate full company-interface
protocol remains gated. Existing generation-only approvals are not silently
upgraded to company access. Live provider availability and paid calls depend on
the operator's account and are not established by mock tests.

Four image families (gateway/worker, UI, PostgreSQL and Redis) are built for Linux
AMD64 and ARM64. Download the installation archive and its SHA-256 file, verify
the checksum and follow the bundled README. Image references are pinned by digest;
CycloneDX inventories for both architectures accompany the release.

For an existing installation, back up the database, media and configuration,
retain the Compose project name, secrets and volumes, and drain workers using the
bundled update procedure. This release includes migrations 071 through 096. An
image rollback does not undo migrations; preserve post-upgrade writes and follow
the documented compatibility and recovery procedure.

Publishing this release does not deploy any running installation. The included
mock provider is free; live provider credentials and charges are separate.
The application is Apache-2.0; bundled components retain their own notices.
