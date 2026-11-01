# Configuration and deployment

Copy `.env.example` to `.env` for local development. `.env` is intentionally
ignored by Git; never commit a populated copy.

For independent UI builds and backend-only operation, see [Frontend separation](frontend-separation.md).

For first-administrator setup, global settings and access recovery, see [System administration](system-administration.md).

## Core settings

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `PORT` | no | `8080` | Gateway HTTP port inside a native deployment |
| `METRICS_ADDR` | no | disabled natively; `:9091` in Compose | Separate private Prometheus listener; use distinct loopback ports for native gateway and worker |
| `PROMETHEUS_PORT` | no | `9090` | Loopback host port of the optional Compose monitoring profile |
| `DATABASE_URL` | yes outside Compose | — | PostgreSQL connection URL |
| `REDIS_URL` | yes outside Compose | — | Redis connection URL |
| `CLIENT_API_KEY` | yes | development value in Compose | Legacy deployment credential for the separate system owner |
| `GOOGLE_OAUTH_CLIENT_ID` | optional Google login | — | Google OpenID Connect web client ID |
| `GOOGLE_OAUTH_CLIENT_SECRET` | optional Google login | — | Google OpenID Connect client secret |
| `GOOGLE_OAUTH_REDIRECT_URL` | optional Google login | — | Absolute callback URL ending in `/auth/google/callback` |
| `SESSION_SIGNING_SECRET` | internet UI | — | Signs short-lived OAuth state and protects the login flow |
| `AUTH_PUBLIC_URL` | cookie login or separate docs | — | Public API and OAuth callback origin |
| `UI_PUBLIC_URL` | no | API origin | Email links and OAuth/checkout return origin |
| `DOCS_PUBLIC_URL` | no | — | Separate documentation origin |
| `UI_PORT` | no | `8088` | Published local UI port in the optional Compose profile |
| `DOCS_HOST` | separate docs UI | `docs.invalid` | Documentation hostname in the independent UI server |
| `SESSION_TTL` | no | `720h` | Lifetime of an opaque browser session |
| `MIGRATE` | no | `auto` | `auto` applies embedded migrations; `off` disables it |
| `MEDIA_PROVIDER` | no | `mock` | Default adapter for internal task-service configuration |
| `DEFAULT_MEDIA_MODEL` | no | provider default | Default model for the internal task service's default adapter |

These variables configure internal task-service defaults. Every public generation
request must supply `model` with the exact public ID returned by `GET /v1/models`.
Separate `provider` and `media_type` request fields are rejected; the endpoint and
model determine the media type. See the [API request contract](openai-api.md#requests-and-provider-extensions).

See [Grafana and Prometheus monitoring](monitoring.md) for the Administrator
browser dashboard, metrics, alert rules, private access,
replica discovery and verification. The metrics endpoint is separate from the
public API and is not forwarded by the UI or Caddy.

## Secrets

| Variable | Required | Purpose |
| --- | --- | --- |
| `WEBHOOK_SIGNING_SECRET` | yes | HMAC-SHA256 signing for webhook deliveries |
| `MEDIA_URL_SIGNING_SECRET` | yes | Signs temporary media access URLs |
| `PROVIDER_CREDENTIALS_ENCRYPTION_KEY` | production | Encrypts each user's credentials stored in PostgreSQL; falls back to the media signing secret for local compatibility |

Use independent, high-entropy values. Losing or changing the provider encryption
key makes stored provider credentials unreadable. Changing the media signing
secret invalidates outstanding signed URLs. Rotate the webhook secret in
coordination with every receiver.

## Provider bootstrap

Provider definitions own their fields, defaults and environment aliases. Compose
loads the optional `.env` into gateway and worker; new providers need no Compose
wiring. `PROVIDER_SETTINGS` accepts a JSON object keyed by provider ID, overriding
module defaults and environment aliases. See [Provider modules](provider-modules.md).

Environment provider credentials seed only a provider row that does not yet
contain credentials. Once settings are saved through the admin console, the
database becomes authoritative and later environment changes do not overwrite
them.

| Variable | Provider | Purpose |
| --- | --- | --- |
| `OPENAI_API_KEY` | OpenAI | Project inference key; model-list read access is required |
| `OPENAI_BASE_URL` | OpenAI | Fixed official API base, `https://api.openai.com/v1` |
| `OPENAI_MODEL` | OpenAI | Initial default model, `gpt-image-2.5-flare` |
| `AGNES_API_KEY` | Agnes AI | Inference key; explicit Check connection verifies authentication through a read-only model query |
| `AGNES_BASE_URL` | Agnes AI | Fixed international API base, `https://apihub.agnes-ai.com/v1` |
| `AGNES_MODEL` | Agnes AI | Initial default model, `agnes-image-2.5-flash` |
| `SONILO_API_KEY` | Sonilo | Platform inference key; authenticated account-services access verifies it |
| `SONILO_BASE_URL` | Sonilo | Fixed API base, `https://api.sonilo.com/v1` |
| `SONILO_MODEL` | Sonilo | Initial default operation, `text-to-music` |
| `SONILO_VIDEO_MUSIC_PRICE` | Sonilo | Account-confirmed USD/second tariff for music-scored video output |
| `SONILO_VIDEO_SFX_PRICE` | Sonilo | Account-confirmed USD/second tariff for video output with sound effects |
| `SONILO_SOUND_PRICE` | Sonilo | Account-confirmed USD/second tariff for combined music/effects audio |
| `SONILO_VIDEO_SOUND_PRICE` | Sonilo | Account-confirmed USD/second tariff for combined music/effects video |
| `BYTEDANCE_API_KEY` | BytePlus | ModelArk inference key |
| `BYTEDANCE_BASE_URL` | BytePlus | Regional ModelArk API base URL |
| `BYTEDANCE_MODEL` | BytePlus | Initial default model |
| `BYTEPLUS_BILLING_ACCESS_KEY_ID` | BytePlus | Read-only billing AccessKey ID |
| `BYTEPLUS_BILLING_SECRET_ACCESS_KEY` | BytePlus | Read-only billing secret |
| `DASHSCOPE_API_KEY` | Alibaba | DashScope inference key |
| `DASHSCOPE_BASE_URL` | Alibaba | Regional DashScope or workspace API base URL |
| `DASHSCOPE_MODEL` | Alibaba | Initial default model |
| `DASHSCOPE_MUSIC_API_KEY` | Alibaba | Optional Beijing key for Fun-Music |
| `DASHSCOPE_MUSIC_BASE_URL` | Alibaba | Optional Beijing/workspace API base URL |
| `ALIBABA_BILLING_ACCESS_KEY_ID` | Alibaba | Read-only RAM AccessKey ID for BSS |
| `ALIBABA_BILLING_ACCESS_KEY_SECRET` | Alibaba | Read-only RAM AccessKey secret for BSS |

The inference key and endpoint must belong to the same provider region. Use the
admin UI to validate credentials before enabling a provider. Billing credentials
are separate because inference keys do not provide access to itemized provider charges.

## Provider response deadlines

The generation field `timeout_seconds` overrides the selected provider's
`connection.timeout_seconds`; an unset provider value inherits
`PROVIDER_HTTP_TIMEOUT` (30 seconds by default). Values are positive integer
seconds, with no minutes-based product cap. The maximum representable value is
9,223,372,036 seconds. Zero, negatives, fractions, strings and request null are
rejected. Omit the generation field to inherit; omit/null the saved connection
value to restore the installation default. A supplied connection object replaces
the previous object, so retain its other fields when updating it through the API.

Set **Request timeout (seconds)** in provider settings. For example, a provider
can default to 180 seconds while one API generation specifies
`"timeout_seconds": 600`. The request override is durably queued and included in
idempotency matching. Saved connection settings, including its timeout, are
revision-pinned for accepted work. No timeout is sent as a vendor model option.

The budget covers each outgoing generation HTTP request through response-body
reading. It also applies to synchronous image/audio generation. Catalog and task
status requests use the provider setting without the generation override;
credential checks and existing caller/network deadlines can finish earlier.
The asynchronous render/polling lifetime remains `PROVIDER_TASK_TIMEOUT`.
A timeout never causes an automatic paid submission retry. Worker shutdown still
obeys `WORKER_SHUTDOWN_TIMEOUT`; configure the operational drain budget for long
calls. The historical `OPENAI_GENERATION_TIMEOUT`, `AGNES_GENERATION_TIMEOUT` and
`BYTEPLUS_IMAGE_TIMEOUT` environment settings are superseded by the shared
hierarchy and no longer select the provider deadline. The provisional
BytePlus-only three-minute default was removed before release.

## Worker and network settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `PROVIDER_POLL_INTERVAL` | `1s` | Delay between async provider polls |
| `PROVIDER_TASK_TIMEOUT` | `30m` | Maximum task polling time |
| `MAX_POLL_ERRORS` | `5` | Consecutive provider polling failures before failure; integer from 1 to 2147483647 (persisted counter capacity) |
| `PROVIDER_HTTP_TIMEOUT` | `30s` | Default provider API timeout; each saved connection can override it with `connection.timeout_seconds` |
| `DISPATCH_INTERVAL` | `1s` | PostgreSQL-to-Redis reconciliation interval |
| `WORKER_OPERATION_CONCURRENCY` | `64` | Current claims/operations per worker, range 1–256; stop dequeueing at saturation, release pending jobs between polls |
| `ARCHIVE_CONCURRENCY` | `4` | Concurrent result transfers per worker, range 1–256; archival also occupies an operation slot |
| `WORKER_SHUTDOWN_TIMEOUT` | `25m` | Maximum time to drain current worker operations after SIGTERM; queued work and future polls wait for the next worker |
| `WORKER_STOP_GRACE_PERIOD` | `26m` | Compose worker stop grace period; keep longer than the drain budget and final lease cleanup |
| `WEBHOOK_HTTP_TIMEOUT` | `10s` | Per-delivery timeout |
| `WEBHOOK_MAX_ATTEMPTS` | `8` | Maximum webhook attempts |
| `WEBHOOK_RETRY_BASE` | `1s` | Initial exponential retry delay |

Synchronous provider calls can last ten minutes and then transfer results.
Shutdown stops intake immediately, lets current operations persist their outcome,
and keeps ownership leases alive during that drain. It does not wait for pending
asynchronous provider jobs to finish. After the drain deadline, remaining calls
are cancelled and the worker exits unsuccessfully; ambiguous submissions are
never blindly retried. Use the guarded release stop from
[deployment](deployment.md#later-releases-and-rollback). If increasing provider
or transfer timeouts, also increase the drain and container stop budgets. The
release helper reads both the merged Compose configuration and existing worker
containers, allowing the larger drain budget plus 60 seconds. A shorter explicit
override is rejected before stopping.

## Storage

| Variable | Default | Purpose |
| --- | --- | --- |
| `STORAGE_DRIVER` | `local` | Fallback for accounts without saved settings; keep `local` and choose S3 in the console |
| `STORAGE_LOCAL_PATH` | `./data/media` | Filesystem media root |
| `STORAGE_MAX_BYTES` | `2147483648` | Maximum archived result size |
| `STORAGE_DOWNLOAD_TIMEOUT` | `5m` | Timeout per provider download or S3 HTTP request |
| `MEDIA_URL_TTL` | `1h` | Signed preview/download URL lifetime |

The Gateway and worker must mount the same storage volume when using `local`.
Use replicated object storage before horizontally scaling across hosts.

Workers sweep abandoned private files at startup and once per minute. On Linux
and macOS, writers hold kernel file locks through atomic publication; the cleaner
skips active writers, including stalled transfers in another process. Each storage
root is scanned incrementally, with a 10,000-entry/five-second budget per pass;
large trees resume on later passes. Published results and references are excluded.
Cleanup errors are logged and do not stop task processing.

S3 uploads use a private `gateway-s3-staging-v2` directory under the process's
temporary directory. Per-upload ownership lasts through download, hashing and
S3 retries; abandoned staging directories are swept by the worker, including
already downloaded files awaiting upload. The staging parent remains empty
between uploads. Cleanup needs no disk allocation and can reclaim space on a
full filesystem. Other platforms retain ordinary writes but do not sweep without
supported kernel locks. Storage filesystems must support those locks.

Before first adopting this cleanup, stop all older gateway/worker writers; they
did not participate in file ownership. Legacy local `.media-<number>.part` and
`.reference-<number>` leftovers become eligible after the greater of 24 hours or
the current download timeout plus one hour. New locked files need no age delay.
Old `gateway-s3-<number>` staging outside the new parent is excluded; replacing
the old container removes its ephemeral temporary filesystem.

## Production baseline

The repository does not yet provide a production deployment chart. At minimum:

1. Put the Gateway behind TLS and an authenticated reverse proxy.
2. Separate administrative access from ordinary client network access.
3. Inject secrets from a secret manager and use a managed PostgreSQL/Redis setup.
4. Restrict outbound DNS and traffic to approved provider, media, and webhook
   destinations.
5. Centralize redacted logs and metrics, configure backups, and test restore.
6. Pin and regularly update container images and Go dependencies.

Consult [SECURITY.md](../SECURITY.md) and [architecture.md](architecture.md) for
the preview's trust boundaries.

## Personal keys and upgrading

With browser authentication configured, sign in with Google or email and open **API keys** in your organization or personal settings.
Issue a named key for each integration; generation is enabled by default. Add
provider or key management permissions only when the integration requires them.
The default console expiration is 90 days; the API also allows no expiration.
No new signing or encryption environment variable is needed for personal keys.

Migration `010_personal_api_keys.sql` adds `user_api_keys` and the nullable
`tasks.api_key_id`. Apply it before starting updated Gateway/worker binaries
(`MIGRATE=auto` applies it at startup). Existing sessions, tasks, provider
credentials and the system service key keep their ownership. Deploy Gateway and
worker from the same revision because both read the task schema.

For a persistence integration check, set `GATEWAY_TEST_DATABASE_URL` to a dedicated,
disposable PostgreSQL database and run `go test ./internal/postgres -run
TestPersonalAPIKeyPersistence -count=1`. The test applies all embedded migrations;
never point it at an application database.

## Account storage (local / S3)

Open **Storage** in the administration console. Select **Own S3 Compatible Storage**
and choose the storage provider. The shared, personal and organization editors
show that provider's required fields with its own credential names:

| Provider | Main fields |
| --- | --- |
| Amazon S3 | Region, Bucket name, Access key ID, Secret access key |
| Cloudflare R2 | S3 Endpoint, Bucket name, Access Key ID, Secret Access Key |
| Backblaze B2 | S3 Endpoint, Bucket name, keyID, applicationKey |
| Custom S3 | S3 Endpoint, Region, Bucket name, Access key ID, Secret access key, path-style addressing |

**Optional settings** contains the folder prefix, which groups Gateway objects.
**Test connection** tests the unsaved form without changing bucket CORS.
**Save** tests object access, configures missing browser access automatically for
`UI_PUBLIC_URL` (or `AUTH_PUBLIC_URL`), preserves other CORS rules, and activates the
connection after confirming the saved policy. If the key lacks CORS management
rights, the error names the required permission. Already-ready buckets work with
object-only keys. For an unchanged saved connection, **Set up browser uploads**
appears after a failed browser check. The editor waits up to a minute for policy
propagation and reports readiness separately from saving settings. See
[direct file transfers](direct-storage.md#bucket-configuration) for provider rights.
Without configured public URLs or a request Origin, headless requests retain the
server-only probe. Selecting **Local storage** switches subsequent archives back
to the server volume. No restart is required.

| Service | Endpoint | Region | Addressing |
| --- | --- | --- | --- |
| Amazon S3 | Leave blank | The bucket's AWS region | Virtual hosted (path-style off) |
| Cloudflare R2 | `https://<account-id>.r2.cloudflarestorage.com` | `auto` | Path-style on |
| Backblaze B2 | S3 endpoint from the bucket details, e.g. `https://s3.us-west-004.backblazeb2.com` | Region from that endpoint | Path-style on |
| MinIO / another compatible service | Its HTTP(S) S3 endpoint | Service's signing region | Usually path-style on |

Endpoints pasted without a scheme automatically receive `https://` in the UI and
API. Explicit `http://` endpoints remain supported for custom services. For B2,
copy **S3 Endpoint** and **Bucket Name** from **Backblaze → Buckets**. The editor
derives the region automatically: `s3.us-west-004.backblazeb2.com` uses
`us-west-004`. Copy **keyID** and **applicationKey** directly from **App Keys**
into the identically named fields. **keyName** is only the key's descriptive name
and is not used to authenticate. The application key is shown only once when
created; create a new key if you did not retain it. The master application key
cannot be used with the S3-compatible API. See the official
[Backblaze app-key guide](https://www.backblaze.com/docs/cloud-storage-s3-compatible-app-keys).

R2 uses signing region `auto` internally, so no Region field is shown. The named
providers use their addressing defaults without a switch; existing saved
addressing is retained. Custom S3 exposes the signing region and addressing
switch for services that need them. Changing providers clears the credential
inputs; enter the selected provider's credentials. Ordinary edits and connection
checks preserve an untouched saved secret.

For R2, create S3 API credentials with Object Read & Write permission for the
bucket. For B2, use an application key ID and application key with object read,
write and delete access. A private bucket is sufficient: clients use Gateway's
authenticated or signed media URLs, which redirect to short-lived S3 URLs. S3 handles
HEAD, Range and file bytes directly. Public bucket access is not required. Browser
uploads require CORS; see [direct file transfers](direct-storage.md).

Grant `s3:PutObject`, `s3:GetObject`, `s3:DeleteObject`, `s3:ListMultipartUploadParts`
and `s3:AbortMultipartUpload` for the selected prefix, plus bucket-level
`s3:ListBucketMultipartUploads` for abandoned-upload cleanup
(or the provider's equivalent permissions). The check writes a tiny random object
under `<prefix>/.gateway-check/`, reads it back and removes it. The check reports
failure if cleanup is denied; bucket versioning may retain probe versions or delete
markers according to the bucket's lifecycle policy.

Settings and historical connections are scoped to the account in PostgreSQL;
credentials are encrypted with `PROVIDER_CREDENTIALS_ENCRYPTION_KEY`. Gateway and
worker must use the same database and encryption key. Back up this key with the
DB. Authorized storage managers see the saved access key ID. The secret is never
returned: the UI displays a mask for the saved secret and only sends a replacement
when entered. Blank credential values in API requests keep the saved keys;
changing endpoint requires both credentials in the request. The UI shows the
active storage and any unsaved changes; Save settings is disabled until a setting
changes and becomes disabled again after saving or reverting the changes.
Personal API keys need `storage:manage` to read, change or test storage settings.
Generation-only keys cannot configure storage.

Each generation's result and optional video last frame use one destination selected
when archiving begins. S3 object keys include the prefix, account ID and task ID.
The existing `STORAGE_MAX_BYTES` limit applies separately to each artifact. Worker archival uploads
use a bounded temporary file for hashing and retries, removed after each attempt;
allow that much temporary disk space on workers. Durable copies are stored only in
the selected destination. S3 previews and downloads redirect to signed storage URLs;
S3 delivers the bytes and handles Range requests directly. Browser library uploads
also go directly to S3; see [direct transfers](direct-storage.md).

Switching storage does not migrate or delete old files. Legacy local keys remain
readable, and S3 keys retain their original connection even after bucket/endpoint
changes or switching back to local. Keep the old volume/buckets and credentials
available to preserve history. Updating credentials for the current, unchanged
S3 destination also updates reads using that connection. A failed upload fails
the generation instead of silently falling back to local storage.

The API exposes `GET/PUT /v1/admin/storage` and
`POST /v1/admin/storage/check`; see the OpenAPI reference for request fields.
Migration `011_storage_settings.sql` is applied automatically with `MIGRATE=auto`.

Provider setup references: [Cloudflare R2 Go SDK](https://developers.cloudflare.com/r2/examples/aws/aws-sdk-go/),
[AWS endpoint configuration](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-endpoints.html).

## Generation credentials and reference duplicates

Generation uses a real Gateway API key for estimates, catalog, tasks, results and
reference operations. Browser sessions authorize account administration only. Paste
a saved key or click **Generate new key**: the console creates a generation-only
key, fills the field, and remembers it in sessionStorage for 24 hours. Quick-created
keys have no separate spending limit or credential expiration. Organization balance
and member weekly limits govern shared generation. Revoke keys under **Configuration → API keys**. The 24-hour period limits browser storage,
not the key's lifetime. Sign-out, login changes and session expiry clear the saved
secret. Browser storage is not an encrypted credential vault.

Reference uploads are deduplicated by SHA-256 of the entire file within each
account. A duplicate returns the existing image with `duplicate: true` and HTTP 200;
a new image returns HTTP 201. Legacy local images are hashed when an upload has the same size. Direct S3
uploads deduplicate only previously indexed hashes, without reading legacy objects. Existing duplicate records are not deleted, so their URLs keep working.
Dragging selected references changes their order without uploading any bytes.

### Deleting library images from versioned storage

Deleting a library reference removes all versions of its exact object key, including delete markers. Versioned buckets such as Backblaze B2 need `s3:ListBucketVersions` and `s3:DeleteObjectVersion` in addition to ordinary object read/write/delete permissions. A failed purge leaves the library record available for retry. Bucket retention and Object Lock are not bypassed; lifecycle rules and unrelated objects are not modified.

## OpenAI upgrade notes

Enable **Available for generation** on the OpenAI connection in personal or shared
provider settings. There is no installation-wide provider allowlist; model defaults
and organization-specific model grants still apply.
No SQL migration is needed. API keys use the existing encrypted provider record.
OpenAI uses one inference key; separate cloud billing credentials do not apply.
See [OpenAI setup and cost limitations](openai-provider.md).


## Email sign-in with Resend

Email registration, email confirmation, password login and password recovery share
browser sessions with Google. See [email authentication](email-authentication.md)
for configuration, account identity rules and the delivery-disabled rollout.

| Variable | Default | Purpose |
| --- | --- | --- |
| `RESEND_API_KEY` | empty | Resend sending API key; server-side only |
| `EMAIL_FROM` | empty | Sender address on a verified Resend domain |
| `DOCS_PUBLIC_URL` | empty (local `/docs`) | Separate HTTPS origin for public docs; requires browser authentication and an explicit distinct `AUTH_PUBLIC_URL` |
| `AUTH_PUBLIC_URL` | Google callback origin | HTTPS origin for verification/reset links; required for email-only deployments |

Both Resend fields must be configured together. Leave both empty until delivery
is approved and ready. No messages are queued, sent or written to logs in that
state; email login is hidden while preserving its enabled preference, and registration/recovery return `503` before creating pending records. Existing sessions and password API behavior are unchanged. Configure Google or delivery before signing out of the initial owner session; see the existing administrator recovery procedure if no browser method is available.

## Request limits and task scheduling

System administrators configure request quotas in the console or through
`/v1/system/limits`, with per-field organization overrides at
`/v1/organizations/{org}/limits`. See [request limits](request-limits.md) for
values, inheritance, rolling windows and error handling.

The worker no longer reads `WORKER_CONCURRENCY`. `WORKER_OPERATION_CONCURRENCY`
bounds current claims before dequeueing; submissions, polls and archival share
this per-worker limit. Pending asynchronous tasks release their slot and
processing goroutine between polls. `ARCHIVE_CONCURRENCY` separately bounds
result transfers within that operation budget. Saturation delays further
dequeues while reconciliation and active ownership heartbeats continue. Existing
leases, durable acceptance and recovery remain enabled.
