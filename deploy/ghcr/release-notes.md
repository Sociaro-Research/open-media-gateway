Open Media Gateway 1.0.1 is the first maintenance update. It includes the fixes
listed in the [changelog](https://github.com/Sociaro-Research/open-media-gateway/blob/v1.0.1/CHANGELOG.md):

- Correct legal pages and administrator editing, documentation themes and schemas,
  chart labels, catalogue statuses, model names and mock audio playback.
- Repair storage diagnostics, pricing/catalogue parsing, multipart video requests,
  negative cost-limit errors and organization artifact HEAD access.
- Preserve accepted optional nulls and video-duration spellings, improve invitation
  feedback and installation guides, and ship Original as the only fresh theme.
- Build Go components with the patched Go 1.27.3 toolchain.

Four image families (gateway/worker, UI, PostgreSQL and Redis) are rebuilt for
Linux AMD64 and ARM64. Download the installation archive and its SHA-256 file,
verify the checksum and follow the bundled README. Image references are pinned
by digest; CycloneDX inventories for both architectures are attached.

For an existing installation, back up the database, media and configuration,
retain the Compose project name, secrets and volumes, and follow the bundled
update and worker-drain procedure. This update adds the legal-document database
migration; an image rollback does not undo database migrations. Existing saved
themes and user preferences remain supported.

No running installation is deployed by this release. Generation through the
included mock provider is free; live provider credentials and charges are separate.
The application is Apache-2.0; bundled components retain their own notices.
