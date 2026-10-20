# Browser monitoring with Grafana and Prometheus

## Open the dashboard

Administrators open **Administration > Monitoring** in the gateway console, or
visit `https://gateway.example.com/monitoring/` on your installation. Sign in with the normal gateway account if needed. The entry page
opens the provisioned **Gateway overview** dashboard; no SSH tunnel, Grafana
password, manual data-source setup or PromQL query is required.

Choose a time range at the top right (the default is one hour). The dashboard
refreshes every 30 seconds; its Route filter affects API traffic and latency
only. Sections show service/dependency availability, active alerts, request
rate/errors/p95 latency, durable tasks, Redis queues, scheduling delays, webhooks,
worker progress and application memory/goroutines/database pool contention.
**No data** means a measurement is unavailable, not a healthy zero; first check
availability and collector panels. An empty Active alerts table means no rules
are pending or firing. Generation outcomes and billing remain in Reports.

Access is limited to current system Administrators with an active browser
session. The UI proxy checks the session and role on every Grafana request,
removes caller-supplied identity headers and passes only a verified internal user
ID. API keys cannot open monitoring. Grafana users are automatically created as
read-only Viewers; dashboards and the Prometheus data source are provisioned from
`monitoring/grafana/` and cannot be changed through this UI. Role/session
revocation takes effect on subsequent requests. Sign out through the gateway
console; Grafana has no independent login session.

Grafana 13.2.2 has no published port. It shares an internal Docker network with
the UI proxy and Prometheus. Its `grafana-data` volume preserves preferences and
its internal database; dashboards remain in Git. The metrics listeners and raw
Prometheus interface remain private. Grafana displays Prometheus alert states;
outbound notification delivery still requires a configured channel and
Alertmanager. This dashboard does not send messages.

## Collection

Gateway and worker use the [official Prometheus Go client](https://prometheus.io/docs/guides/go-application/).
Compose provides optional Prometheus 3.15.0. Each process has its own registry
and a dedicated `GET /metrics` listener; public API, UI and documentation
origins do not expose it.

## Start and access

For a local installation:

```sh
docker compose --profile ui --profile monitoring up -d --build
```

Open `http://localhost:8088/monitoring/` for the dashboard. For the raw Prometheus
interface, open `http://127.0.0.1:9090` and check **Status > Targets**: `gateway` and
`worker` should be UP. Prometheus also scrapes itself. Scrapes and rule evaluation
run every 15 seconds. Docker DNS discovery refreshes every 15 seconds and
discovers all replicas after container replacement or scaling.

Compose fixes `METRICS_ADDR=:9091` inside both application containers and never
publishes it. `PROMETHEUS_PORT` changes the Prometheus host port, which always
binds to `127.0.0.1`. Data persists in `prometheus-data`, with 15-day and 2 GB
retention limits, whichever is reached first. WAL and temporary compaction files
need additional disk space beyond that retention size.

Native processes disable metrics by default. Set `METRICS_ADDR=127.0.0.1:9091`
for gateway and, on the same host, `METRICS_ADDR=127.0.0.1:9092` for worker;
configure private Prometheus targets accordingly. A metrics bind error fails
startup rather than silently running without monitoring.

For an existing installation, follow the backed-up, committed release
procedure in [Deployment](deployment.md), including worker draining before
replacement. Replace gateway and worker to activate their listeners. After
successful application checks, validate the merged configuration and start
Prometheus:

```sh
docker compose --profile ui --profile monitoring config --quiet </dev/null
docker compose --profile ui --profile monitoring up -d --no-deps prometheus </dev/null
docker compose --profile ui --profile monitoring up -d --no-deps --wait grafana </dev/null
```

No public Caddy routing changes are required: the versioned UI proxy serves the
authenticated `/monitoring/` path. `UI_PUBLIC_URL` must be the console origin so
Grafana builds correct subpath links (the local default is
`http://localhost:8088`). Keep the loopback binding in the merged configuration
and preserve all data volumes. For advanced raw Prometheus queries, access its
server UI through an SSH tunnel:

```sh
ssh -i "$HOME/.ssh/gateway_ed25519" -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes \
  -N -L 9090:127.0.0.1:9090 root@gateway.example.com
```

Then open `http://127.0.0.1:9090`. Change the first tunnel port if occupied.
Do not publicly expose Prometheus or metrics listeners. Anyone with access to
the monitoring network can read installation-wide metrics.

## Metrics and meaning

All application metrics have `service="gateway"` or `service="worker"`;
Prometheus adds `job` and replica `instance`. Labels contain no prompts, keys,
URLs, task IDs, models, organizations, users or provider connection identifiers.
HTTP routes use registered ServeMux patterns such as `GET /v1/generations/{id}`.
Unmatched and pre-routing responses share `unmatched`; unknown methods share
`OTHER`.

| Metric | Meaning |
| --- | --- |
| `media_gateway_http_requests_total{method,route,status}` | API request counts, including refusals and errors |
| `media_gateway_http_request_duration_seconds{method,route}` | Full request duration histogram, including response body delivery |
| `media_gateway_http_requests_in_flight` | Executing API requests |
| `media_gateway_dependency_up{dependency}` | PostgreSQL ping / Redis queue reads succeed (1) or fail (0) from this process |
| `media_gateway_collector_success{collector}` | Queue/task snapshot succeeded (1) or failed (0) |
| `media_gateway_database_connections{state}` | Open, in-use and idle connections; in-use plus idle equals open |
| `media_gateway_database_max_open_connections` | Per-process pool limit |
| `media_gateway_database_connection_waits_total`, `media_gateway_database_connection_wait_seconds_total` | Pool contention since process start |
| `media_gateway_queue_jobs{queue,state}` | Redis pending, claimed and expired entries; expired is a subset of claimed |
| `media_gateway_active_tasks{status}` | Durable queued/running tasks, exported only by gateway |
| `media_gateway_oldest_active_task_age_seconds{status}` | Oldest nonterminal task age since admission |
| `media_gateway_tasks_due{status}`, `media_gateway_oldest_task_overdue_seconds{status}` | Work due without a live database lease, and its longest scheduling delay |
| `media_gateway_pending_webhooks`, `media_gateway_oldest_webhook_overdue_seconds` | Durable pending webhook count and overdue delivery age |
| `media_gateway_worker_loop_last_success_timestamp_seconds{loop}` | Successful dispatcher/consumer iteration; empty queues count as healthy |
| `media_gateway_worker_loop_stale_after_seconds{loop}` | Threshold: 60 seconds, or three dispatch intervals if longer |
| `media_gateway_worker_loop_errors_total{loop}` | Dispatcher/consumer failures since startup |
| `media_gateway_worker_active_claims` | Concurrent claim operations, including provider calls and archival |
| `media_gateway_worker_claims_total{outcome}`, `media_gateway_worker_claim_duration_seconds{outcome}` | Finished claim operations and their duration |
| `go_*`, `process_*` | Standard Go/process metrics; available process metrics depend on the OS |

A successful claim can schedule another poll, acknowledge already-owned work
or persist a provider failure. It is **not** a completed generation counter.
Pending asynchronous jobs release their claim between polls. Reports remain
the source for generation outcomes/accounting; billed spend and user-level
usage are not exported.

Redis and PostgreSQL counts measure different states. PostgreSQL remains
authoritative after Redis loss. Global task/webhook and Redis snapshots must
not be summed across processes or gateway replicas; use `max` with appropriate
labels. Queries use existing active-row partial indexes and never scan completed
history. Reads are snapshots, not one cross-database transaction.

Dependency reads are bounded; at most two concurrent scrapes are admitted per
process. Failed snapshots emit failure indicators and omit the affected backlog
values, rather than presenting them as empty. Disabled native listeners perform
no background monitoring queries. Host/disk, PostgreSQL engine and Redis server
exporters are outside this integration.

## Queries

API requests per second and HTTP 5xx responses by route:

```promql
sum(rate(media_gateway_http_requests_total[5m]))
sum by (route) (rate(media_gateway_http_requests_total{status=~"5.."}[5m]))
```

p95 request duration by route:

```promql
histogram_quantile(0.95, sum by (le, route) (rate(media_gateway_http_request_duration_seconds_bucket[5m])))
```

Backlog without multiplying observations by replicas:

```promql
max by (status) (media_gateway_active_tasks)
max by (queue, state) (media_gateway_queue_jobs)
```

## Alerts and verification

`monitoring/alerts.yml` detects unreachable/missing targets, dependency and
snapshot failures, stalled worker loops, overdue tasks/webhooks and sustained
HTTP 5xx errors. Thresholds are initial defaults, not latency commitments;
tune them using observed traffic. Planned maintenance can trigger alerts.

Active rules also appear in the Grafana overview. Outbound notifications require a
separately configured Alertmanager and recipient/channel; this integration sends
no notifications. Grafana provides the ready-to-use browser dashboard above.

CI validates the monitoring Compose profile, Prometheus config and rule behavior:

```sh
docker compose --profile ui --profile monitoring config --quiet
docker run --rm --network none --entrypoint /bin/promtool -v "$PWD/monitoring:/etc/prometheus:ro" \
  prom/prometheus:v3.15.0 check config /etc/prometheus/prometheus.yml
docker run --rm --network none --entrypoint /bin/promtool -w /etc/prometheus -v "$PWD/monitoring:/etc/prometheus:ro" \
  prom/prometheus:v3.15.0 test rules alerts.test.yml
```

Full backend and separate-process mock/metrics checks:

```sh
python3 scripts/with-test-services.py -- make verify-ci TEST_FLAGS=-count=1
python3 scripts/with-test-services.py -- python3 scripts/verify-headless.py
```

Headless checks always verify private listeners, public `/metrics` returning 404,
dependency snapshots, labels without customer data and mock generation with an
intact SVG download. To also run a real Prometheus and query its collected series:

```sh
python3 scripts/with-test-services.py -- env GATEWAY_TEST_PROMETHEUS_BIN=/absolute/path/to/prometheus \
  python3 scripts/verify-headless.py
```

Obtain that binary from a trusted, verified release. These commands use disposable
local services and never access a deployed database.
