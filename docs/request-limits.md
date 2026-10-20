# Request admission and execution

System administrators configure `System request limits` in Administration →
Settings and `Organization request limits` in an organization's Settings.
Organization administrators cannot read or edit these settings. The API uses
`GET/PUT /v1/system/limits` and `GET/PUT /v1/organizations/{org}/limits`, requiring
both the current system administrator role and `system:manage` for API keys.

| Setting | Default |
|---|---:|
| Generation requests per key, rolling minute | 100 |
| Generation requests per user, rolling minute | 300 |
| Generation requests per key, rolling second | 20 |
| Generation requests per user, rolling second | 50 |
| Status/list requests per key, rolling minute | 1,200 |
| Status/list requests per user, rolling minute | 3,600 |
| Key creation requests per user, rolling minute | 5 |
| Active keys per user | 20 |

All settings require positive integers. Each organization field can be `null`
to inherit its system default. An explicit organization value replaces that
one default. User counters combine all keys belonging to the user **within the
same organization**. Personal keys share a separate user counter. There is no
organization-wide request quota. New/replacement keys cannot reset user usage.
Expired and revoked keys do not count toward the active key maximum. Lowering
that maximum does not revoke existing keys; additional keys require room.

Redis atomically checks every applicable window and counter before admitting a
request. Replicas share counters and Redis's clock. Limits are rolling windows,
not calendar-minute counters, so a boundary does not permit a double burst.
Denied requests consume no additional tokens. Changing quotas does not reset
counters. Requests that pass rate admission consume a token even if validation
or a later dependency fails. Status/list quotas cover the canonical generation
endpoints. Internal provider polls do not consume
customer quotas. Downloads, estimates and unrelated management operations are
not generation or status requests.

Exceeding a rate quota immediately returns HTTP 429 `rate_limit_exceeded` with
`Retry-After` in seconds. Nothing is delayed for later admission. Active key
exhaustion returns HTTP 409 `active_key_limit`: revoke a key or wait for its
expiry. If quota storage is unavailable, admission fails with HTTP 503 rather
than bypassing protection. Clients should honor Retry-After and use jitter.
Existing authentication abuse protection, budgets, storage capacities, payload
size checks and provider timeouts still apply.

## Worker scheduling

`WORKER_OPERATION_CONCURRENCY` bounds current operations per worker (default 64,
range 1–256). A slot is reserved before dequeueing and released after claim
cleanup; saturation leaves excess accepted work in the queue. The limit covers
submissions, polls and archival, including waits for an archive slot. Replicas
multiply the process-local limit. Asynchronous submissions persist the provider ID and their
next poll time. Each subsequent claim performs one poll; pending results release
the goroutine and lease until the next due time. Error counts and the original
start time survive process restarts, so retries cannot reset the task timeout.
Providers that return the result synchronously still use concurrent HTTP calls.

PostgreSQL is the durable record of accepted work; Redis is a delivery mechanism.
The outbox, expiring claims and fencing remain necessary for crash recovery.
This is not a product-level waiting queue or a promise of infinite capacity:
submission, database, network and provider capacity still affect actual latency.
Full reconciliation batches are drained without a fixed sleep between batches;
enqueues and outbox cleanup are batched. Poll intervals respect provider load.

`WORKER_CONCURRENCY` is obsolete and no longer read. `ARCHIVE_CONCURRENCY` still
bounds actual result file transfers per process (default 4); it protects disk,
network and memory. Pending generations release operation slots between polls;
current synchronous calls and archives retain them until the claim finishes.
It does not apply to direct browser ↔ S3 transfers. Database and HTTP connection
pools are resource controls, not limits on the number of accepted generations.
