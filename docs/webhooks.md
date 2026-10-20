# Webhooks

When a task contains `webhook_url`, Media Gateway records deliveries for
`generation.queued`, `generation.in_progress`, `generation.completed`, and
`generation.failed`. The JSON envelope contains `id`, `object: "event"`, `type`,
`created_at` (Unix seconds), and `data` (the public generation object). Delivery is
asynchronous and retried with bounded exponential backoff.

## Headers

Each request is an HTTP `POST` with `Content-Type: application/json` and these
headers:

| Header | Meaning |
| --- | --- |
| `X-Media-Gateway-Delivery` | Stable delivery identifier |
| `X-Media-Gateway-Event` | Lifecycle event name |
| `X-Media-Gateway-Timestamp` | Unix timestamp used in the signature |
| `X-Media-Gateway-Signature` | `v1=` followed by a lowercase hex HMAC |

The signature input is the ASCII timestamp, a literal period, and the exact raw
request body:

```text
v1=hex(HMAC-SHA256(WEBHOOK_SIGNING_SECRET, timestamp + "." + raw_body))
```

## Receiver verification

1. Read and retain the raw body bytes before JSON decoding.
2. Reject timestamps outside a small tolerance suitable for your environment.
3. Compute the HMAC from the timestamp and raw body with the shared secret.
4. Compare the decoded signature in constant time.
5. Deduplicate by `X-Media-Gateway-Delivery` before applying side effects.
6. Return any `2xx` response only after the event is durably accepted.

Receivers must tolerate duplicate and out-of-order deliveries. Do not rely on an
event being delivered exactly once.

## Retries

Non-`2xx` responses and network failures are retried up to
`WEBHOOK_MAX_ATTEMPTS`. Delay starts at `WEBHOOK_RETRY_BASE`, doubles per
attempt, and is capped at five minutes. A response body is read only in a small,
bounded diagnostic window.

## Network safety

Webhook destinations must be public HTTP or HTTPS endpoints. URL credentials,
fragments, localhost, private, loopback, link-local, metadata-service and reserved
IP ranges are rejected. IPv4, IPv6 and IPv4-mapped IPv6 addresses share this policy.
Literal destinations are checked before task acceptance; DNS names are checked
when the worker connects. A DNS-only failure affects webhook delivery, not the
accepted generation.

Every new connection resolves all returned A/AAAA addresses, rejects mixed
public/private answers, and connects to a checked IP literal without another DNS
lookup. The original hostname remains the HTTP Host and TLS verification name.
Redirects are not followed, and environment HTTP proxies are not used. The same
checks apply to retries and deliveries queued before this policy was introduced.
Public endpoints on custom ports are supported. Local development receivers need
a public HTTPS endpoint/tunnel; private receiver URLs are not an exception.

The approach follows the [OWASP SSRF prevention guidance](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html).
Deployment egress rules should additionally isolate internal services exposed on
otherwise publicly routable addresses; IP classification cannot identify them.
