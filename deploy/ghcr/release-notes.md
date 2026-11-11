Media Gateway 1.0.3 improves template safety, saved console preferences, provider
guidance and catalogue recovery. See the [changelog](https://github.com/Sociaro-Research/open-media-gateway/blob/v1.0.3/CHANGELOG.md).

- Save table columns and width choices per signed-in profile.
- Configure the footer copyright holder independently from legal documents;
  retain existing saved branding and use neutral defaults for new installations.
- Confirm template Delete against the saved version, preserve unrelated work and
  count reference labels correctly without discarding incomplete drafts.
- Reject repeated company-member assignment without changing its existing role.
- Preserve detached provider assets during concurrent deletion and keep failed
  asset lookups from monopolizing background polling.
- Reject contradictory WAV timing and unsupported chained or multiplexed Ogg.
- Add dismissible provider guidance and reviewed GPT Image 2.6, Qwen Audio 3.1
  and guarded PixVerse V6 media workflows, alongside expanded lifecycle sources.
- Update Go toolchains to 1.27.4, Caddy's x/net to 0.62.0 and Redis to 7.4.12 on
  supported Alpine 3.22, with corresponding component notices.

Four image families (gateway/worker, UI, PostgreSQL and Redis) support Linux
AMD64 and ARM64. The versioned workflow requires native builds, strict security
scans and fresh HTTP/HTTPS source-free installation checks before publishing
the final version manifests and downloads. The installation archive pins all
four images by digest; SHA-256 checksums and CycloneDX inventories accompany it.

For an existing schema 96 installation, this release adds migrations 097 and 098
for per-user table preferences and the independent versioned footer. Back up
PostgreSQL, the complete Redis volume, media, configuration and active overrides;
retain keys, Compose project and volumes, and drain workers with the bundled
update procedure. Migration 081 is unchanged from v1.0.2 and must not be replayed
on an installation that already applied it. Image rollback does not undo schema
changes; use the documented compatibility and recovery procedure.

Saved catalogue schedules and warnings remain operator decisions. Source changes
do not establish pricing imports, paid generation or account entitlement;
PixVerse keeps its reviewed Singapore-connection guard. Publishing this release
does not deploy a running installation. The mock provider is free; live provider
credentials and charges are separate. The application is Apache-2.0; bundled
components retain their own notices.
