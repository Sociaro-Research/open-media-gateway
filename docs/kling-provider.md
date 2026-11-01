# Direct Kling video generation

Contract reviewed on **2026-10-29** using Kling's official
[authentication reference](https://kling.ai/document-api/api/get-started/authentication),
[Kling 4.0 text-to-video](https://kling.ai/document-api/api/video/4-0/text-to-video),
[Kling 4.0 image-to-video](https://kling.ai/document-api/api/video/4-0/image-to-video),
[text-to-video API](https://kling.ai/document-api/api/video/3-0-omni/text-to-video),
[image-to-video API](https://kling.ai/document-api/api/video/3-0-omni/image-to-video)
and [video pricing](https://kling.ai/document-api/pricing/base/video), including
the **Balance** USD prices. Official `.md` exports expose the same contracts without JavaScript; discover
them through the [documentation index](https://kling.ai/document-api/llms.txt).
A successful document fetch alone does not verify account access or actual charges.

This adapter calls Kling directly at `https://api-singapore.klingai.com`, the
documented endpoint for servers outside China. It uses the current **API key**
with `Authorization: Bearer …`. Legacy Access Key / Secret Key JWT credentials,
consumer subscriptions, reseller APIs and Alibaba models are separate contracts.

## Kling 4.0 access and pricing

Kling 4.0 is fully available in the current official reference. Its account
funding prerequisite is a Flagship Resource Pack or account-manager balance
top-up; a catalogue entry does not establish entitlement for a particular key.

Published USD Balance prices supply estimates automatically for both text and
image-keyframe operations. No manual tariff is required. Optional account-specific
USD/second overrides remain available in `kling4_720p_price` and
`kling4_1080p_price` (environment: `KLING4_720P_PRICE`, `KLING4_1080P_PRICE`).
An override takes precedence over synchronized public prices. Remove an override
to return that resolution to published list pricing. Accepted tasks retain their
original credential and tariff revisions.

## Connection and catalogue

Add a **Kling AI** connection in Providers or Shared providers, select its
reviewed catalogue (turn off **Manual catalog**), and save an API key supplied by
the operator. Enable **Available for generation** when ready. Keys use the
existing encrypted, owner-scoped connection store and revision pinning.
Environment bootstrap is optional: `KLING_API_KEY`, `KLING_BASE_URL` and
`KLING_MODEL` (default `kling-4.0-text-to-video`). No database migration or
Compose change is required.

Saving credentials, reading the connection and loading its four-model local
catalogue do not contact Kling. **Check connection** explicitly calls the
[free account query](https://kling.ai/document-api/api/assets/account-usage.md):
`GET /account/costs?start_time=…&end_time=…`, with a 24-hour UTC window in Unix
milliseconds and the saved API key. Following the official
[service-code semantics](https://kling.ai/document-api/api/get-started/error-codes.md)
(rechecked 2026-10-30), successful HTTP/JSON and explicit zero outer/nested service
codes establish **Authentication verified**. Resource-package details are
separate: a missing, null or unrecognized list retains a visible warning without
inferring a balance or subscription. An empty list is valid. Missing/invalid
service codes, malformed envelopes, transport failures and nonzero service codes
remain unverified. A disabled connection can be checked without enabling
generation.

This check does not establish model entitlement, available funding or generation
output. Package/balance values can lag 12 hours and are discarded. The adapter
keeps only a timestamp, latency, safe error and optional-details warning in
memory. Success is cached for five minutes; credential or connection changes invalidate it. Restarts and other
replicas require their own check. Generation does not persist a verification
badge. Fresh account queries are serialized at one per second per gateway
process; other clients or replicas can still hit Kling's QPS limit. No generation
is submitted by a connection check. Permission restrictions are reported
separately from authentication failure.

The following IDs are Gateway operation IDs for upstream **`kling-4.0`** and
**`kling-3.0`**.
Their separate descriptors keep image-derived aspect ratios out of text controls.
Public IDs default to `kling/<operation-id>` for the built-in connection; a
separately named connection uses its own connection ID prefix.

| Gateway operation ID | Official POST route | Inputs |
| --- | --- | --- |
| `kling-4.0-text-to-video` | `/text-to-video/kling-4.0` | Prompt; aspect ratio `21:9`, `16:9`, `9:16` or `1:1` |
| `kling-4.0-image-to-video` | `/image-to-video/kling-4.0` | Prompt and 1–10 ordered keyframes |
| `kling-3.0-text-to-video` | `/text-to-video/kling-3.0` | Prompt; aspect ratio `16:9`, `9:16` or `1:1` |
| `kling-3.0-image-to-video` | `/image-to-video/kling-3.0` | Prompt and first frame; optional last frame |

Kling **4.0** supports 3–30 whole seconds (default 10), `1080p` (default) or
`720p`. The specific API reference does not expose an audio switch,
`multi_shot`, or 4K, even though the broader release note describes additional
capabilities. Direct audio requirements through the prompt. Gateway's existing
10,000-character prompt cap is conservatively below Kling 4.0's published
language-dependent limits; the adapter does not expand that core cap.

For 4.0 images, `first_frame` takes one image, `first_last_frame` takes two,
and `keyframes` takes 3–10. Order is first frame, middle frames, last frame.
Each is assigned a stable `image_1`, `image_2`, … ID for optional `@image_1`
prompt references. Frame roles are inferred from count if the mode is omitted.

Kling **3.0** supports 3–15 whole seconds (default 5), `720p` (default), `1080p` or `4k`,
optional native audio (default off), and `options.parameters.multi_shot`
(boolean, default true). Prompts contain 1–3072 characters. Multi-shot prompt
syntax is described in the official reference; Kling checks shot timing and
content. Image generation, Omni references, Elements, voice references, motion
control, editing, Flash and legacy versions are outside this initial adapter.
Generic manual-model profiles are unavailable because the
upstream version lives in the route rather than a replaceable JSON model field.

Image inputs use public HTTPS URLs in first/last order, with
`options.reference_mode` set to `first_frame` or `first_last_frame` (or inferred
from count); 4.0 also accepts the `keyframes` mode above. Last-frame-only input
is excluded. Kling requires JPEG/PNG, at most 30 MB for 4.0 and 50 MB for 3.0,
both dimensions at least 300 px and image ratio from 1:2.5 to 2.5:1.
The provider validates the remote image bytes and fetchability. This adapter
does not fetch reference URLs or accept Base64 inputs. Image-to-video takes its
aspect ratio from the frame; do not send `options.ratio` for that operation.

## API and Playground

Use the existing `POST /v1/videos` or `POST /v1/generations`, then poll the
returned Gateway status URL. The Playground uses the same catalogue and shared
controls. For the built-in `kling` connection:

```json
{
  "model": "kling/kling-4.0-text-to-video",
  "prompt": "A paper boat floating across a still pond",
  "seconds": "10",
  "size": "1080p",
  "options": {
    "ratio": "16:9"
  }
}
```

For image-to-video select `kling/kling-4.0-image-to-video`, omit ratio and pass
`input_reference` with ordered HTTPS image URLs on either route. The same pattern
applies to the 3.0 IDs, whose audio and multi-shot controls are separate.
Requests for unsupported controls fail before submission. Native requests use `settings` and `contents`,
not the legacy `model_name` payload.

The provider's task ID persists without in-memory state. `GET /tasks?task_ids=…`
maps `submitted`, `processing`, `succeeded` and `failed` to Gateway states.
Polling verifies that exactly one returned task matches the saved ID and that
success contains one HTTPS video output and a finite positive duration. Gateway
archives the finished video through its existing storage flow; Kling documents
a 30-day retention period for provider results. Submission errors are never
automatically retried. After a timeout or ambiguous response, inspect provider
history before resubmitting. No upstream cancellation API is exposed.

## Estimates and billing

Published USD **Balance list prices per output second**:

| Model / audio | 720p | 1080p | 4k |
| --- | --- | --- | --- |
| Kling 4.0 (text or image keyframes) | $0.157 | $0.235 | Unsupported |
| Kling 3.0 / off | $0.084 | $0.112 | $0.420 |
| Kling 3.0 / native | $0.126 | $0.168 | $0.420 |

A default 10-second 1080p Kling 4.0 output has a **$2.35** provider list estimate,
before Gateway markup or account discounts. Image keyframes do not count as input
video. The cheaper video-input rows have different duration rules and are not
used by these adapters. Turbo, Omni and Flash prices are separate contracts.

The catalogue synchronizer imports explicit USD prices from the official
[Markdown pricing table](https://kling.ai/document-api/pricing/base/video.md).
It matches only reviewed model, resolution, audio and output-duration rules;
it never converts Units to dollars. Existing schedules using exact old default
Kling page URLs resolve to their official `.md` exports at fetch time, retaining
the actual fetched URL as evidence. Custom source URLs and saved schedule
settings are preserved. Missing/changed headers, rows, currency, preview markers
or billing rules fail interpretation, retain last confirmed prices and produce a
warning. Unknown document hashes are not acknowledged automatically. Model
discovery remains a reviewed local catalogue; new model announcements do not
automatically enable new routes or controls.

A silent five-second 720p video is estimated at **$0.42**. Quotes use integer
microdollars and retain the reviewed source/date. Output duration meters the
accepted tariff after completion; it remains an estimate pending billing.
Provider task billing counters retain only validated amounts and recognized
denominations: cash in USD/CNY, test balances, or prepaid video units. They are
not promoted to confirmed USD charges. Package purchase economics, account
discounts, free test balances and currency conversion are not inferred.

Automated bill import/reconciliation is not implemented. The documented
`POST /account/billing/balance` supplies historical deductions, not a prospective
tariff catalogue. It needs separate account-scoped pagination, task matching and
settlement handling; the adapter does not silently treat task counters as bills.
Confirm actual charges in the Kling account. HTTP and JSON error messages use status/machine codes;
arbitrary provider text, prompts, reference URLs and credentials are excluded
from errors and usage records. Result URLs remain in the existing private
result-storage lifecycle, separate from usage metadata.

Local regression tests use synthetic HTTP fixtures only. No key creation,
credential entry, paid generation or live account validation is part of those
tests.
