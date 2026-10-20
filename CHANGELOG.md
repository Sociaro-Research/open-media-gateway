# Changelog

All notable changes to Media Gateway will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/).

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
