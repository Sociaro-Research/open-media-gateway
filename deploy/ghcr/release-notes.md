Media Gateway 1.0.4 adds video options, improves report accuracy and makes common console and API failures easier to understand. See the [changelog](https://github.com/Sociaro-Research/open-media-gateway/blob/v1.0.4/CHANGELOG.md).

- **Providers:** Add BytePlus Seedance 2.5 Flash and 4K output for base Seedance 2.5, and raise the reference-video pixel limit for both. Add 4K and HDR with matching billing for supported Kling Video 4.0 models. Update Alibaba and legacy Kling Image tariff parsing and OpenAI deprecation parsing.
- **Reporting and console:** Fix report dates around midnight timezone changes and normalize user and API-key filters. Keep Audit Log pagination tied to the applied filters, reduce unrelated logo reads, and improve mobile layouts and template draft cleanup.
- **Access and API:** Recheck invitation issuer authority, limit revocation by role, and make invalid references, request fields, and idempotency keys easier to correct. Avoid automatic SDK resubmission when generation admission is unconfirmed, and never substitute the full video for an unsupported download variant.

Four image families (gateway/worker, UI, PostgreSQL and Redis) support Linux AMD64 and ARM64. The versioned workflow requires native builds, strict security scans, and fresh HTTP/HTTPS source-free installation checks. The installation archive pins all four images by digest and includes SHA-256 checksums and CycloneDX inventories.

Installations already on schema 98 need no new migration. Back up PostgreSQL, Redis, media, configuration and active overrides before updating. Retain keys, the Compose project and volumes, and drain workers with the bundled update procedure. Older installations should follow the documented upgrade and recovery instructions.

Publishing this release does not deploy a running installation or change saved catalogue schedules or warnings. The mock provider is free; live provider credentials and charges are separate.
