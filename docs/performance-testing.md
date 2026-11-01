# Performance and reliability testing

Start with [the testing guide](testing.md) for required checks, evidence handling
and verification boundaries. This document describes how to run
the suites; an available command is not evidence that its scenario has passed.

These suites exercise the real API, task repositories, Redis queue, workers,
webhook sender and local result storage with a deterministic HTTP provider.
The provider identifies itself as `mock`, produces real downloadable bytes and
confirms zero cost. No external provider credentials are required.

## Isolated correctness checks

Run the full CI checks against disposable native PostgreSQL and Redis:

```sh
POSTGRES_BIN=/path/to/postgresql/bin make test-isolated
```

`scripts/with-test-services.py` creates a new PostgreSQL cluster and Redis
process, binds them to loopback, replaces inherited test URLs, records source
and service versions, and removes service data on exit. PostgreSQL and Redis
binaries must already be installed. It never connects to existing services.
Use `--output dist/performance/checks` to retain logs at a chosen location:

```sh
python3 scripts/with-test-services.py --output dist/performance/checks \
  -- make verify-ci TEST_FLAGS=-count=1
```

The same Go tests also run against CI's explicitly provisioned PostgreSQL 17
and Redis 7.4 services. Ordinary `go test` skips database-dependent tests when
their URLs are absent; that is not a successful integration run. `make verify-ci`
rejects absent URLs. Never supply production URLs to any test command.

### Reliability contracts

| Scenario | Required behavior |
| --- | --- |
| Concurrent idempotent retries and two workers | One task and one provider submission per key; changed input returns 409; one terminal event; downloaded result and last frame retain their bytes and SHA-256. |
| Lost admission response | A proxy closes the client socket after the backend commits its 202 response. Retrying the same request/key returns that original task with one provider submission and an intact result. |
| Provider submission, polling and storage failures | Explicit failed state, no downloadable partial success; transient polling failures recover; a subsequent healthy task completes. |
| Redis coordination data loss | PostgreSQL reconstructs delivery before and after provider IDs are persisted; 100 accepted tasks finish without another submission. Only the test queue's keys are removed. |
| Hard worker termination | A real child worker process receives SIGKILL during submission, polling or a partially written archive. A persisted provider ID resumes; an ambiguous submission without a durable ID fails explicitly instead of submitting twice. Orphan temporary files are reported separately. |
| Release with active jobs | Distinct real gateway/worker builds are exchanged with SIGTERM while jobs are queued, awaiting provider responses, polling or partially archived. Current requests, including synchronous results, drain with renewed leases; the next release resumes durable jobs and supports its added provider. All 35 tasks submit once and download intact. |
| Shutdown deadline and release stop guard | An expired drain budget returns an explicit error and never repeats an ambiguous submission. The release helper refuses migrations after a nonzero/forced worker exit and derives its stop allowance from both incoming and running workers. |
| PostgreSQL and Redis outages | A TCP fault proxy cuts live connections and refuses reconnects. Health reports 503, admission fails closed, and existing/new tasks finish after reconnection. This tests network unavailability, not service crash durability. |
| Native PostgreSQL crash recovery | Immediate server shutdown and restart must preserve 20 committed provider IDs, complete their results and accept a new request without repeating submissions. |
| Native PostgreSQL backup restoration | A readable custom-format pg_dump snapshot is restored after dropping only the test schema. Thirty tasks, authorization and subsequent generation must recover. |
| Archival burst | Four occupied archive slots do not prevent new provider submissions; 33 real 1 MiB results download intact. |
| Webhook retries | Real sender retries an HTTP 503; stable event IDs, payloads and HMAC signatures survive the retry. Delivery is at least once; receivers must deduplicate. |
| Quota race and owner isolation | Forty concurrent requests across two keys share one user's 12-request quota; denied requests have Retry-After; another user has an independent quota. A pending owner does not block another owner's five completed tasks. |
| Reports and cancellation | Full summary and CSV exclude another owner's rows; CSV ignores table pagination; cancellation releases the database connection. |
| Interrupted storage transfers | Cancellation and truncated downloads leave no committed or temporary result files. |
| Large streaming transfers | Four simultaneous 64 MiB archives preserve all bytes and SHA-256 with less than 64 MiB total Go allocation. The provider and verification also stream bytes. |
| S3 HTTP retries and cancellation | Actual local HTTP uploads preserve full bytes and hash metadata on retry, return failure after three unsuccessful attempts, and remove staging after success, exhaustion or cancellation. This is an HTTP fixture, not a live cloud service. |
| Connection reuse | Concurrent HTTP and PostgreSQL waves retain reusable connections within bounded idle pools. |

Fast correctness CI expires crash-test leases directly. To measure actual
recovery after the production 60-second lease duration, run:

```sh
python3 scripts/with-test-services.py --output dist/performance/recovery \
  -- env GATEWAY_REAL_LEASE_EXPIRY=1 \
  go test -race -count=1 -timeout=5m -v -run '^TestReliability' ./internal/reliability
```

`scripts/verify-headless.py` additionally builds and runs separate gateway and
worker executables, initializes the first administrator, tests recovery keys,
then generates and downloads an explicit mock result. Run it inside the same
disposable-service wrapper; `psql` must be available in PATH.

The two native PostgreSQL recovery tests require the wrapper's temporary
service-control descriptor and PostgreSQL's `pg_ctl`, `pg_dump` and `pg_restore`.
Run `POSTGRES_BIN=/path/to/postgresql/bin make test-recovery` to execute them
alongside real worker lease expiry. This dedicated run sets
`GATEWAY_NATIVE_RECOVERY=1` and tests only the reliability package, so a server
restart cannot interrupt other test packages using the same cluster. The two
tests skip in ordinary full-suite/container CI. A database URL alone never enables
server process control. Restoring a test schema does not verify a complete
production backup containing media, configuration and deployment state.

### Release while jobs are active

The normal reliability suite includes a release test using separate real
gateway and worker processes. It builds versions A and B with race detection;
temporary Go build overlays route the mock adapter to a loopback HTTP fixture
and add a free provider to version B. No shipped provider source is replaced.
The test sends real SIGTERM, waits for an ownership heartbeat during drain,
then starts version B through the normal entrypoints and migration path. It
does not shorten the production 60-second ownership lease. Run it separately:

```sh
python3 scripts/with-test-services.py --output dist/performance/release \
  -- go test -race -count=1 -timeout=5m -v \
  -run '^TestReliability(ReleaseWithActiveTasks|ShutdownDeadlineIsExplicit)$' ./internal/reliability
python3 -m unittest discover -s scripts/tests -p 'test_stop_worker_release.py' -v
```

The Python tests simulate Docker responses and require no daemon. Together,
these checks verify native process draining and the release safety decision;
they do not themselves execute Compose/container replacement or a full-stack
restart. Run the disposable Docker campaign below for container-level recovery.
Host reboot, sustained load and external integrations require separate controlled
environment acceptance.

### Disposable Docker recovery campaign

With Python 3.12+, Docker and Compose available, run:

```sh
python3 scripts/reliability/compose-recovery.py \
  --output dist/performance/compose-recovery-new-run
```

The output directory must be new. The runner generates fresh project names,
volumes and loopback ports, discards inherited application environment values,
and copies source into temporary build directories. It refuses external/shared
volumes or public test ports. It does not read an installation's `.env` or accept
an existing project name. At least 3 GiB free space is required before builds.
Use a controlled runner; builds and test containers consume CPU, memory and disk.

The shipped Dockerfiles and base Compose file build two versions; only disposable
copies of the mock adapter and registry connect the HTTP fixture and add a free
provider. Requests and downloads pass through a separate Caddy, the UI proxy and
the real gateway. The campaign exercises a guarded release, a graceful restart
of gateway/worker/UI/Caddy/PostgreSQL/Redis, then SIGKILL of those six services
with the real 60-second ownership lease. An ambiguous provider submission must
fail explicitly without being repeated; other accepted work must recover.

The campaign also verifies that crash leftovers disappear. It freezes one real
worker during a partial transfer and starts a second on the same media volume:
the second must remove new/aged legacy orphans while preserving the owner's
locked file. Unfreezing must produce an intact result. A separate non-networked
container runs the storage pressure test on a fresh **8 MiB tmpfs**, reaches real
`ENOSPC`, reclaims an orphan without deleting active/published files, and archives
a complete new result. It never fills a host filesystem or an application volume.

Native `go test -race ./internal/storage` covers separate-process local downloads
and a local HTTP S3 fixture, both during active work and after SIGKILL. The pressure
test intentionally skips there; its opt-in execution is the bounded Docker tmpfs
step above. Do not set `GATEWAY_STORAGE_PRESSURE_ROOT` to an ordinary filesystem.

It also backs up application/configuration, Docker images, PostgreSQL and media,
verifies the backups, and restores them into a separate Compose project with
new volumes. The original administrator key, saved results, pending jobs and
a new generation must work. The fixture's durable state represents an external
provider and stays outside application recovery.

Evidence includes command logs, image IDs, per-scenario reconciliation, synthetic
backups and SHA-256 manifests. Cleanup stops only generated projects and removes
only their labelled volumes and image tags; it never uses `down -v` or prunes
unrelated Docker resources. Keep the evidence directory private. This tests
container/service recovery, not a host reboot, Cloudflare, real providers or S3.

### Ready-made bundle recovery guide

To exercise the exact commands shipped in the bundle's
[backup and recovery guide](../deploy/ghcr/backup-restore.md), use four explicit
local test images (the same images accepted by `scripts/verify-release.py`):

```sh
python3 scripts/verify-bundle-recovery.py \
  --output .local/bundle-recovery-new-run \
  --gateway-image YOUR_LOCAL_GATEWAY_IMAGE \
  --ui-image YOUR_LOCAL_UI_IMAGE \
  --postgres-image YOUR_LOCAL_POSTGRES_17_IMAGE \
  --redis-image YOUR_LOCAL_REDIS_74_IMAGE
python3 -m unittest discover -s scripts/tests -p 'test_release_recovery.py' -v
```

Repeat with `--https` and another new output directory to cover the included
HTTPS configuration/volumes. The output directory must be new and private.
The runner generates its own projects, secrets, loopback API port and synthetic data; it cannot accept an
existing installation, project name or `.env`. It uses only free mock generation
and a disabled synthetic provider credential. Allow disk space for the saved
images and a second installation. It packages/extracts the actual offline guide,
executes its marked command blocks, restores into separate volumes and verifies
migration history, three saved downloads, the original administrator key,
encrypted provider settings, Redis data, unchanged `.env`, optional-volume byte
markers and a new mock generation. It exercises compatible application rollback
without replacing PostgreSQL, and verifies checksum/existing-target refusals plus
rejection of original binds, volume-driver aliases, fixed volumes/networks and
inherited container mounts before any recovery resource is created. Evidence stays in the requested directory; cleanup removes only this
run's generated containers and labelled volumes/networks, without `down -v`.

HTTPS/monitoring volumes are checked with synthetic markers; the runner does not
issue certificates or start those services. It does not verify S3 object backups,
external bind mounts, PostgreSQL major upgrades, a different application release
or live routing/cutover. Such configurations need a separate recovery drill.

## Measured load

Performance timings run without the race detector. The built-in Go arrival
scheduler offers a fixed request rate independently of response speed. It
records starts dropped by its 256-request client bound as failures.
Requests use a real personal API key, so authorization, quota checks and
accounting participate; capacity scenarios raise only the isolated schema's
request quotas to avoid confusing intentional HTTP 429 with a capacity limit.

```sh
python3 scripts/with-test-services.py --output dist/performance/matrix \
  -- env GATEWAY_PERF_HISTORY=100000 GATEWAY_PERF_STAGE=30s \
  GATEWAY_PERF_CPU_PROFILE=1 make performance
```

The default matrix has 1/2/4 workers at 1/10/50 admissions per second. Each
scenario uses a fresh migrated schema and mixes admission, generation-list
reads, report tables, summary calculation and full CSV serialization. All
accepted task IDs must reach saved success, have exactly one provider
submission, and pass SHA-256 verification through an HTTP download. Downloads
occur during the final drain, separately from the offered-load phase.
Database task and provider job counts must also match accepted IDs. After the
drain, the complete CSV is parsed as a stream and its row count and the summary
request count must equal historical rows plus newly created tasks.

The test API uses the gateway executable's shared HTTP-server constructor,
including its ordinary 15-second write deadline. CSV and summary requests have
a separate 60-second write deadline and 55-second calculation budget. CSV is
spooled before headers and carries Content-Length, so an incomplete transfer
cannot look like a complete successful response.

`TestPerformancePendingPolling` measures actual regular polling of pending
provider tasks, not just task startup. Every original task must be polled at
least twice; new admission must proceed during polling; all results must
subsequently download intact. For a larger backlog:

```sh
python3 scripts/with-test-services.py --output dist/performance/pending \
  -- env GATEWAY_PERF=1 GATEWAY_PERF_PENDING=5000 \
  GATEWAY_PERF_PENDING_DURATION=30s \
  go test -count=1 -timeout=10m -v -run '^TestPerformancePendingPolling$' ./internal/reliability
```

For a two-hour sustained run:

```sh
python3 scripts/with-test-services.py --output dist/performance/soak \
  -- env GATEWAY_PERF=1 GATEWAY_PERF_SOAK=2h GATEWAY_PERF_SOAK_RPS=2 \
  go test -count=1 -timeout=3h -v -run '^TestPerformanceSoak$' ./internal/reliability
```

Set a Go timeout longer than the soak plus setup and drain. An eight-hour run
needs a dedicated machine and `-timeout=9h`; it is not part of ordinary CI.

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `GATEWAY_PERF` | absent | Set to `1` to execute measured tests. |
| `GATEWAY_PERF_OUTPUT` | wrapper's output directory | JSON, progress records and pprof destination; required for measured tests. |
| `GATEWAY_PERF_STAGE` | `10s` | Offered-load duration per matrix cell. |
| `GATEWAY_PERF_HISTORY` | `10000` | Historical rows per scenario; use 100000 or 1000000 to test reports at scale. |
| `GATEWAY_PERF_WORKERS` | `1,2,4` | Comma-separated worker counts. |
| `GATEWAY_PERF_RATES` | `1,10,50` | Comma-separated admission rates. |
| `GATEWAY_PERF_REPORT_CLIENTS` | `1` | Independent clients cycling through table, summary and CSV. |
| `GATEWAY_PERF_PENDING` | `1000` | Number of pending tasks in the polling scenario. |
| `GATEWAY_PERF_PENDING_DURATION` | `10s` | Measured polling period after submissions. |
| `GATEWAY_PERF_SOAK` | absent | Explicit sustained-load duration. |
| `GATEWAY_PERF_SOAK_RPS` | `1` | Sustained admission rate. |
| `GATEWAY_PERF_CPU_PROFILE` | absent | Set to `1` for CPU pprof; heap pprof is always saved after drain. |

JSON includes offered/accepted/completed counts, HTTP status distribution,
errors, admission/read/report p50/p95/p99, admission-to-provider and
provider-success-observed-to-published delays, queued/running task counts, queue age,
polling overdue age, database waits and connection churn, Go heap and
goroutines. Progress records are written every 30 seconds.

The API, workers, provider fixture and load generator share a Go process.
Heap/CPU/goroutine measurements include the fixture and client. The fixture
retains per-job observations for final reconciliation, so increasing retained
heap alone is not proof of an application leak. PostgreSQL and Redis use
separate processes. Logical workers also share the fixture's 20-connection
database pool; separately deployed worker processes each have their own pool.
This fixture is not a replica-capacity model. Record hardware, versions, other
concurrent loads and
source fingerprint before comparing runs. These local numbers are regression
baselines, not production capacity or latency commitments. No portable timing
thresholds are imposed until measurements on a stable runner establish them.
Database connections are capped at 20 per process and retained across polling
waves, with five-minute idle expiry. Replicas multiply that connection budget;
size the PostgreSQL limit for the total gateway and worker process count.

`make benchmark` records three CPU/allocation samples for usage-bill matching
and in-memory report summaries. Use `go tool pprof` with measured profiles to
inspect allocation or CPU sources before changing algorithms.

### Optional k6 runner

`scripts/performance/gateway.js` can load a separately running, disposable
loopback stack with `GATEWAY_PERF_URL` and `GATEWAY_PERF_KEY`. It rejects remote hosts
and non-mock models, uses unique idempotency keys, polls terminal state and
downloads each result. This runner requires an independently installed k6.
It is an optional alternative to the executed Go load suite, and has not been
validated with a k6 runtime in the initial local audit.

## Browser budgets

```sh
node scripts/ui/performance.cjs
UI_BROWSER_ENGINE=webkit node scripts/ui/performance.cjs
```

Playwright must be available through Node's module path. The Chromium engine
uses the official `chrome` channel by default for the embedded MP4 codec; see
[Playwright's media-codec guidance](https://playwright.dev/docs/browsers#media-codecs).
CI installs that channel and WebKit in addition to bundled Chromium for other
UI checks, runs these contracts in both engines, and retains their evidence.
Optional `UI_BROWSER_PATH` selects an executable or `UI_BROWSER_CHANNEL`
selects another compatible channel;
`GATEWAY_PERF_OUTPUT` selects screenshots and JSON output. The test uses local
API fixtures and real embedded mock video, synthesized audio and an SVG image.
It records Node/Playwright versions and a source fingerprint for the fixture
and directly served UI files alongside the browser version in each result.

Fifty mixed-media cards exercise both themes and 1440/390/320 px widths.
Updating one running task must preserve unaffected card/player objects,
playing video, keyboard focus and expanded parameters, with no repeated media
authorization for the 49 unchanged results. Slow list responses must never
overlap; leaving the generation section must stop list polling. Input remains
editable, pages have no horizontal overflow or application errors. Forgetting
the API key disposes detached players and prevents a delayed response from
restoring old journal content. Chromium
long tasks and heap are observations, not timing gates; WebKit does not expose
the same measurement APIs. These are deterministic browser contracts, separate
from real backend load and real Safari/device validation.

## CI and remaining boundaries

Ordinary CI runs the reliability suite with isolated services and the browser
budgets in its container job. The manually dispatched **Performance and
recovery** workflow records machine details, uses real lease expiry, executes
the measured matrix and benchmarks, offers an optional 30-minute or two-hour
soak, and uploads evidence even after failure.
It has no automatic schedule and no production target.

External provider behavior, live S3/R2/network throughput, Docker deployment,
Cloudflare routing, filesystem exhaustion, full production backup restoration
and production failover require separate controlled
environment checks. Passing these suites does not establish those properties.
Record machine details, workload, source revision and raw results for each
campaign in ignored local output or CI artifacts. Do not treat past measurements
as a capacity promise for another installation.
