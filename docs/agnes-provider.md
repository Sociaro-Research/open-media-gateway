# Agnes AI provider

Reviewed against the [official documentation](https://www.agnes-ai.com/en/docs/overview)
and API catalog; authentication rechecked on 2026-10-30. The adapter ID is `agnes`. No vendor SDK or
database migration is required. Compose already loads the optional `.env` into
gateway and worker, so the module's environment aliases need no Compose edit.

## Setup

Save an Agnes inference API key under **Providers**, or **Administration → Shared
providers** for organization use. The API endpoint is
`https://apihub.agnes-ai.com/v1`. Turn on **Available for generation** and enable
the desired models in model access settings. The default model is
`agnes-image-2.5-flash`. New independent connections start with a manual catalog;
add the desired native model profiles or turn off **Use manual catalog only** to
discover the reviewed upstream models.

Optional native-process/bootstrap configuration:

```dotenv
AGNES_API_KEY=YOUR_AGNES_KEY
AGNES_BASE_URL=https://apihub.agnes-ai.com/v1
AGNES_MODEL=agnes-image-2.5-flash
```

Saved encrypted credentials remain authoritative. Gateway and worker need the
same encryption key and runtime settings. Keys are masked in management responses.
The module uses the shared credential controls and generic provider presentation;
no new UI component or custom layout is needed.

**Check connection** performs a read-only authenticated `GET /v1/models`.
On 2026-10-30 the official endpoint returned HTTP 401 for both a missing key
and a deliberately invalid synthetic key. This supersedes the September review,
when the endpoint was public. The [official authentication guide](https://wiki.agnes-ai.com/en/docs/overview)
requires a Bearer API key; [error documentation](https://wiki.agnes-ai.com/en/docs/code)
distinguishes 401 authentication failures from 403 access restrictions.

Only an explicit check with a successful, structurally valid model-list response
establishes **Verified**. Discovery, cached/manual models and the offline catalog
fallback cannot verify a key. HTTP errors, semantic errors and malformed replies
remain unverified. The check never generates content; it also works with a manual
catalog without following discovery or polling endpoint overrides. Authentication
expires after five minutes in process memory and resets on configuration changes.

Verification proves acceptance of the key by that read endpoint only. It does not
prove per-model generation grants, available balance, quota or successful output.
An empty authenticated catalog is not evidence of model access. Real-key success
must be checked through the normal application control; fixtures do not establish
that an operator's key is valid.


## Implemented models

| Model | Media | Implemented controls and references |
| --- | --- | --- |
| `agnes-image-2.0-flash` | Image | Text generation, editing and composition; documented exact sizes `1024x1024`, `1024x768`, `768x1024` |
| `agnes-image-2.1-flash` | Image | Text generation, editing and composition; `1K`–`4K`, eight documented aspect ratios |
| `agnes-image-2.5-flash` | Image | Same request contract as 2.1 Flash |
| `agnes-video-2.5` | Video | 4–12 seconds; `720P`, `1080P`, `1K`, `2K`; six aspect ratios; seed; first/last/both keyframes; up to eight images, three audio references and one video reference |
| `agnes-video-2.5-flash` | Video | Same duration, ratios, seed and keyframes; only `720P`; at most five images and three audio references; no video input |

Image references accept public HTTPS URLs or image Data URI Base64. The Gateway
limits image models to 14 references and inline image inputs to 15 MiB each;
these are conservative Gateway caps, not published image-model maxima. Output
encoding/quality/background, negative prompts and watermark switches are not
advertised because Agnes does not document those generation controls.

Videos generate synchronized audio; there is no documented audio-off switch.
Reference modes are `none`, `first_frame`, `last_frame`, `first_last_frame`, and
`references`. Empty mode selects text or media-reference generation from the
supplied inputs. Frame array order assigns first and last roles. The advanced
controls `options.parameters.video_start_seconds` and
`options.parameters.video_require_audio` map to the single video reference's
`start_seconds` and `require_audio`; they require a video reference.

The provider validates remote media: images below 15 MB, dimensions 256–5760;
video below 50 MB, 2–12 seconds, 24–60 FPS; audio below 15 MB each, total 2–12
seconds. Reference URLs must remain publicly reachable while Agnes processes
the task. The adapter does not download inputs to prove these remote-media
properties. Text/reasoning models are omitted: this Gateway's contract handles
media jobs, not text chat. No separate speech/music API is documented for Agnes.

Discovery intersects the public `/v1/models` response with these five reviewed
IDs and caches it for ten minutes. Unknown IDs do not inherit capabilities or
prices. A network/HTTP failure falls back to the reviewed catalog without caching
the failure; a malformed successful response is rejected. Discovery and fallback
do not guarantee account eligibility. Manual catalogs use the shared connection
profile mechanism and require an administrator-supplied per-request price.

## API and lifecycle

Submit images through Gateway `POST /v1/images/generations` or
`POST /v1/images/edits`; submit video through `POST /v1/videos`. All also work
through `POST /v1/generations`, using the same provider-qualified model ID.

```json
{
  "model": "agnes/agnes-image-2.5-flash",
  "prompt": "A quiet harbor at sunrise",
  "size": "2K",
  "options": {"ratio": "16:9"}
}
```

Agnes image calls are synchronous upstream, but remain asynchronous Gateway
jobs. Image references are serialized under `extra_body.image` and URL response
selection under `extra_body.response_format`, on `/v1/images/generations` for
both text and editing requests. Both URL and Base64 result responses are accepted.

Video submission uses `/v1/videos`. The adapter persists an opaque handle with
the returned `video_id` and model name, since Agnes's `id`/`task_id` alone cannot
be used to retrieve the result. After restart the worker polls the origin-root
`/agnesapi?video_id=...&model_name=...` endpoint. `queued`, `in_progress`,
`completed` and `failed` map to the standard Gateway states. Internal progress
fields are ignored, and only a completed task's top-level `url` is archived.

The existing worker archives results before exposing authenticated content URLs,
signed media links and webhook events. Generation requests are never retried by
the adapter. The host applies its existing durable polling schedule/error backoff
and task timeout. HTTP redirects are refused; raw vendor error messages, prompts,
signed URLs and keys are not included in persisted adapter errors.

## Usage and prices

The [official pricing page](https://www.agnes-ai.com/en/docs/pricing) distinguishes
current promotional prices from list prices. As reviewed, all three image models
and Video 2.5 Flash have a temporary $0 offer. The adapter quotes that **as an
estimate**, retaining its dated source. It does not set provider-wide free status,
confirm an actual zero charge, or assume failures are unbilled. Account eligibility
and promotion end dates can differ; update the module's tariff snapshot when the
offer changes or use an explicit manual model price for a different account tariff.

Video 2.5 is estimated at $0.025/second for 720P, $0.040 for 1080P/1K, and $0.055
for 2K. Both input and output video seconds are billable; each input image beyond
the first five adds $0.005. A URL does not supply the input video's duration before
submission, so estimates reserve the documented 12-second maximum and label it
as an upper bound. Returned output seconds are normalized as
`output_video_duration`. For video-reference jobs both output and input duration
metrics must be reported before usage is metered; missing input seconds are never
filled from the estimate. Raw `usage`, when returned, is preserved. Missing usage
is not invented from prompts or an image result count.

No public itemized-billing API or associated read-only permission is documented.
Account invoices remain the authority for actual cost. Promotional estimates and
metered usage never become invoice-confirmed charges automatically.

## Submission failures

HTTP 503 means the provider service is unavailable. It does not establish whether
an inference backend is overloaded, an account lacks model access, or a reference
could not be processed. Error messages retain the HTTP status and translate only
recognized machine codes into fixed explanations; raw vendor messages are never
persisted because they can contain private request content. Numeric or unknown
codes use the HTTP-status explanation.

A 5xx submission is not retried automatically. Check Agnes task history and billing
before explicitly resubmitting: a missing task ID alone does not prove that Agnes
created no task or incurred no charge. A successful public model-list request or
an invalid request returning 400 does not prove inference availability. Original
raw error bodies cannot be recovered from older Gateway task records.

## Verification boundary

Local HTTP fixtures cover every implemented model, image editing/composition,
Base64 and URL responses, video modes and restart-safe polling, invalid controls,
malformed responses, safe error handling, discovery, promotions and incomplete
usage. A dedicated integration test uses isolated PostgreSQL and Redis with the
real API, queue, worker and local storage. It covers image generation/editing,
video polling, generic generation, authenticated and signed downloads, and signed
webhook delivery; only upstream HTTP and media bytes are fixtures. The shared
Playground and provider controls were checked at 1440, 390 and 320 pixels,
including keyboard dismissal of a picker. No real Agnes key or paid generation
is used by these checks. Real
account generation and final invoice reconciliation require operator verification.

Sources: [Image 2.0](https://www.agnes-ai.com/en/docs/agnes-image-20-flash),
[Image 2.1](https://www.agnes-ai.com/en/docs/agnes-image-21-flash),
[Image 2.5](https://www.agnes-ai.com/en/docs/agnes-image-25-flash),
[Video 2.5](https://www.agnes-ai.com/en/docs/agnes-video-25),
[Video Flash](https://www.agnes-ai.com/en/docs/agnes-video-25-flash).

Generation HTTP deadlines follow the shared connection `timeout_seconds` setting
(default 30 seconds), overridden by top-level generation `timeout_seconds`. The
former `AGNES_GENERATION_TIMEOUT` environment setting is superseded.
