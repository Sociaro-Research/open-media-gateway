# Changelog

All notable changes to Media Gateway will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/).

## [v1.0.2] - 2026-11-01

### Added

- Template authoring, shared reference libraries, explicit image/video covers,
  one current template definition and scoped generation with immutable admitted
  job snapshots. Published saves immediately update future generations.
- Reviewed Kling and MiniMax media generation workflows, descriptor-driven
  controls, scoped input preparations, managed provider references and editing
  sessions. Official presets are imported as owner-bound inputs for an authorized
  selected media workflow; archived TTS audio can be reused by local artifact ID.
- Multiple archived outputs, resumable accepted-result receipts and private
  source evidence so interrupted archival does not resubmit accepted generation.
- Projects, stable generation-journal cursors, shared key/budget controls,
  provider logos and connection verification, dedicated installation companies
  and central-console policy derived from the configured public origin.

### Changed

- Keep public provider catalogues and admission focused on image, video, speech
  and music. Necessary input preparation stays attached to an authorized media
  consumer; existing bindings and already admitted jobs retain recovery access.
- Treat template audio as an optional target-model preference and choose the
  nearest supported output resolution without changing the stored preference.
  Input-reference size, duration, dimensions and frame-rate validation remain.
- Render lightweight template content before media compatibility work completes,
  keep sensitive actions blocked during validation, retain settings after
  submission and let the user choose whether to open Playground.
- Unify navigation accents, spacing, reference previews, accessible dialogs,
  provider voice/language choices, prompt limits and decimal file-size displays.

### Fixed

- Explain private-address policy and DNS failures during S3 connection checks
  without exposing credentials or relaxing outbound destination protection.
- Preserve completed primary video outputs when optional provider metadata or
  last-frame output is inconsistent. Keep unknown provider prices and quantities
  unknown instead of treating them as free.
- Preserve template rollback-window edits, company proofs, resource bindings,
  accepted receipts and immutable tasks through the supported migration cycles.
- Isolate disposable Git test fixtures from the caller's repository and retain
  registered-route metrics through public-origin middleware.
- Support one optional exact retired HTTPS API origin during hostname migration,
  with unchanged authentication, CSRF, signatures and canonical links/role.
  Direct storage delivery remains isolated from both application hostnames.

### Security

- Use the upstream JOSE v3 security backport in the Caddy build and retain
  high/critical dependency and complete-history secret-scanning gates.
- Restrict reviewed scanner false-positive exceptions to exact non-secret lines
  and paths, with synthetic credential-detection regressions.

### Upgrade notes

- This release includes database migrations 071 through 087. Back up the
  database, configuration and media, preserve signing/encryption keys and drain
  workers before upgrading. Image rollback alone does not restore schema
  compatibility; follow the documented preservation and rollback procedure.
- `LEGACY_API_ORIGIN` is empty by default. It changes only exact-host API
  admission, not DNS/TLS, browser cookie or cross-origin trust. Reference
  capability URLs have no expiry, so an old hostname cannot be retired solely
  by waiting for media URL expiry.
- Native provider support and published tariff evidence do not establish a
  particular account's entitlement or verify paid generation. Unverified tariff
  conditions continue to fail closed.

## [v1.0.1] - 2026-10-23

### Fixed

- Correct public Privacy and Terms styling, provide neutral operator templates,
  and let administrators edit each legal document with conflict protection.
- Keep console and documentation display preferences consistent, ship only the
  Original theme in fresh catalogs, and preserve saved custom themes.
- Improve Usage chart precision, catalogue email statuses and duplicate model
  labels; return playable samples from mock speech and music.
- Recognize missing Backblaze CORS policies, reordered OpenAI pricing columns
  and PixVerse entries in Alibaba catalogue sources.
- Accept text-only multipart video requests, reject nested image-reference
  arrays, identify negative cost limits precisely, and permit organization
  artifact HEAD requests with generation read scopes.
- Correct composed documentation schemas, optional-null and video-duration
  contracts, generation timeout descriptions and model-selection guidance.
- Warn when invitations have no enabled sign-in method. Refresh the product
  README, Windows/WSL installation guide and deployment references.
- Align release and OpenAPI versions and validate their agreement at publication.

### Security

- Build the application, Caddy and PostgreSQL's Go helper with Go 1.27.3 to
  address the reachable certificate-verification vulnerability.

## [v1.0.0] - 2026-10-20

### Added

- Version-tagged GHCR releases for Linux AMD64/ARM64, with source-free Compose
  installation bundles, generated initial secrets, pinned image digests and
  software inventories. Publication requires verified installation and restart.

- Provider-neutral asynchronous generation for image, video, speech, and music.
- Mock, BytePlus ModelArk, Alibaba Cloud Model Studio, OpenAI, Agnes AI and Sonilo adapters.
- Provider administration, encrypted credentials, usage metering, and optional
  billing balance checks.
- PostgreSQL state, Redis-backed processing, durable local media storage, and
  signed webhooks.

### Security

- Updated `github.com/jackc/pgx/v5` to `v5.9.2` and `golang.org/x/text` to
  `v0.39.0` to resolve reachable vulnerabilities reported by `govulncheck`.
- Bound the local Compose HTTP port to loopback and excluded local credentials,
  generated data and internal documents from the future public Git tree.
