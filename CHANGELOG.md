# Changelog

All notable changes to Media Gateway will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/).

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
