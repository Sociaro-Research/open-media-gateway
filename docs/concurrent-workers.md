# Concurrent generation workers

Provider submissions and polls run as independent concurrent operations, bounded
by `WORKER_OPERATION_CONCURRENCY` per worker (default **64**, range 1–256). Capacity
is reserved before dequeueing and held through processing and claim cleanup.
When all slots are occupied, excess work stays in the queue without delivery
leases, goroutines or heartbeats. Replicas multiply this process-local limit.
An asynchronous task saves its provider ID and next poll
time in PostgreSQL. Pending polls release their goroutine and delivery lease;
waiting between polls does not occupy an operation slot. A current synchronous
provider call, poll or archival does occupy one.
`WORKER_CONCURRENCY` is obsolete and no longer read.

Request admission uses shared per-key and per-user rolling quotas, configured by
system administrators with organization overrides. Excess requests receive HTTP
429 and `Retry-After`, rather than waiting for quota. See
[request limits](request-limits.md) for defaults, scope and failure behavior.
Actual throughput still depends on database, network and provider capacity.

`ARCHIVE_CONCURRENCY` separately bounds result downloads/uploads (default **4**,
range 1–256). This protects disk, memory and bandwidth during provider-result
archival. Archive transfers and operations waiting for an archive slot also
count toward the operation limit; new submissions wait if that limit is full. It does
not apply to direct browser-to-S3 uploads or S3-to-client downloads.

## Ownership

On SIGTERM, dispatch and dequeue stop immediately. Acquired operations retain
their contexts and ownership heartbeats while provider responses, usage and
results are saved. A pending upstream generation persists its next poll and
releases its claim; it does not hold shutdown open until generation completion.
`WORKER_SHUTDOWN_TIMEOUT` bounds this drain (default 25 minutes, to cover
synchronous calls and transfers). Exceeding it cancels work and produces a
nonzero exit. Release procedures must check that exit before replacing the API
or applying migrations; see [deployment](deployment.md#later-releases-and-rollback).

- Redis holds a pending list, a known-ID set, a claim-token hash and an expiry sorted
  set. Deduplicating an enqueue uses set membership, not a scan of pending/processing
  lists. Atomic scripts move an ID and its ownership together.
- Every dequeue has a unique token and a 60-second delivery lease. Only that token
  can renew, acknowledge or requeue it. Expired tokens cannot be resurrected.
- PostgreSQL grants the same token a task lease only if there is no live owner.
  Worker state changes, provider IDs, usage and completion are fenced inside database
  transactions. A stale worker cannot overwrite its successor's task state.
- Each archival delivery uses `task-id/attempts/claim-token` object paths for
  both the result and last frame. A late transfer from an expired/cancelled
  worker cannot overwrite its successor's committed bytes. Reads continue to
  use the stored key, including older result paths. Losing attempts can leave
  non-winning complete objects, which count toward shared storage usage and
  remain associated with the task for ordinary explicit result deletion.
  Temporary-file cleanup does not collect them automatically.
- After result archival reports success, Storage Manager gives the existing
  object's quota record a separate five-second budget that survives worker
  cancellation. This preserves its owner/task association and does not cancel
  or refund a provider operation. Database write errors still propagate; this
  does not make a filesystem/S3 write and PostgreSQL one atomic transaction.
- Heartbeats renew Redis and PostgreSQL every 20 seconds. Renewal failure cancels
  processing. A failed acknowledgement/requeue is repaired after lease expiry rather
  than requiring process restart. Startup recovers expired work only.
- Queue reconciliation runs independently of active operations. It visits due,
  unfinished tasks through indexed keyset pages of 1,000, skipping live database claims.
  Redis enqueue and PostgreSQL outbox cleanup use batches. Full batches are drained
  without an intervening fixed sleep. The cursor
  wraps after the last page, so already-enqueued work cannot starve later outbox rows.
  Redis recovery processes at most 100 expired entries per iteration.
- Shutdown cancels and joins the media and webhook consumers before closing their
  database/Redis resources. Work that cannot be released is recovered after expiry.

A resumed task with a saved provider ID is polled without resubmission. If a process
was interrupted during submission and never saved the provider ID, the task is
marked failed with an explicit interruption message. The gateway cannot infer
whether a remote paid provider accepted that request and must not submit it again
blindly. Leases do not promise exactly-once external side effects across a network
failure. Webhooks retain at-least-once delivery semantics and stable delivery IDs;
receivers must remain idempotent.

## Upgrade

Migration 028 introduced task claim columns and the recovery index. Migration 034
adds durable next-poll scheduling, poll error counts and original worker start time.
A process restart does not reset a task timeout. The scheduler uses versioned Redis keys `media-gateway:{media}:v2:*` and
`media-gateway:{webhook}:v2:*`.

Stop **all legacy worker replicas** before starting a worker using this protocol.
Do not mix old and new worker binaries. Upgrade the gateway and workers together.
Unfinished tasks and due webhook deliveries are recovered from PostgreSQL; old
Redis lists are not trusted as ownership records. Their old keys can be removed
separately after successful rollout. Do not delete Redis/PostgreSQL volumes.

The existing billing-history matching and upload-admission algorithms are separate
from the generation scheduler; this change does not claim unlimited throughput or
fix every load finding from the audit.

## Verification

CI provides isolated PostgreSQL and Redis services and runs the race-enabled Go
suite with `GATEWAY_TEST_DATABASE_URL` and `GATEWAY_TEST_REDIS_URL` configured.

`TestTwentyConcurrentHTTPGenerations` starts 20 concurrent HTTP
requests against the real gateway handler, PostgreSQL and Redis. A controlled
provider cannot finish any request until all 20 have started. One scenario uses a
single worker; another starts a second worker while the first
is actively submitting. Every task must succeed and the provider must receive
exactly 20 submissions. No paid provider is called.

`TestThousandPendingGenerationsLeaveSubmissionsAvailable` submits 1,000 jobs to a
controlled asynchronous provider, restarts the worker, and verifies that a new
request is submitted while the earlier jobs remain pending. It also checks that
due polls resume after restart. This is a regression check, not a production
throughput guarantee.

Additional integration tests cover live-claim preservation, expired ownership,
stale acknowledgements, bounded recovery, failed requeue transport, and fencing
of all worker task mutations. Run locally against disposable services:

```sh
GATEWAY_TEST_DATABASE_URL='postgres://user:password@127.0.0.1:5432/gateway_test?sslmode=disable' \
GATEWAY_TEST_REDIS_URL='redis://127.0.0.1:6379/0' \
go test -race ./...
```

Completed private result attempts use a separate owner/task result_objects
index (migration 065) for explicit result deletion. Shared quota remains in
stored_objects, so private local/S3 bytes do not consume shared allowance.
The existing cancellation-independent accounting budget also applies to this
private index. Older private orphan objects are not backfilled or automatically
deleted; the index covers new completed archives.
