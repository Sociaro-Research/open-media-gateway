# OpenAI-style asynchronous API

The public generation API uses OpenAI endpoint and parameter names wherever their
meaning matches. Execution always remains asynchronous for **every provider and
media type**. These are gateway jobs, not a promise of synchronous OpenAI SDK
compatibility. No provider feature was removed to fit an OpenAI schema.

## Endpoints

| Operation | Endpoint | Result |
| --- | --- | --- |
| Available models | `GET /v1/models` and `GET /v1/models/{model}` | OpenAI-shaped model objects plus gateway capabilities |
| Create image | `POST /v1/images/generations` | HTTP 202, generation object |
| Edit image | `POST /v1/images/edits` | HTTP 202, generation object |
| Create video | `POST /v1/videos` | HTTP 202, video object |
| Create speech | `POST /v1/audio/speech` | HTTP 202, generation object, **not audio bytes** |
| Create music | `POST /v1/audio/generations` | Gateway extension, HTTP 202 |
| Create any supported media | `POST /v1/generations` | Gateway extension; type can be inferred from model |
| Estimate before creating | `POST /v1/generations/estimate` | Same input, customer cost, no generation |
| Read status | `GET /v1/generations/{id}` or `GET /v1/videos/{id}` | Owner-scoped generation/video |
| Download result | `GET /v1/generations/{id}/content` or `GET /v1/videos/{id}/content` | Authenticated media, supports ranges and HEAD |
| Generation history | `GET /v1/generations` | List envelope; 1-100 jobs per page, cursor or offset pagination |

Authenticate using `Authorization: Bearer <GATEWAY_API_KEY>`. Existing key scopes,
organization membership, ownership, provider connection availability, storage
policy, completed-spending budget checks, markup, and idempotency apply through
the same admission pipeline. Estimates and pending tasks do not reserve funds.
For shared providers, new requests are blocked when the available organization
balance or member weekly allowance is zero or negative; an estimate can exceed
the remaining amount. Accepted work can therefore take spending beyond a limit.
The catalog and responses expose only gateway customer prices to regular users;
provider accounting remains visible only to authorized system administrators.

Request admission can return HTTP 429 `rate_limit_exceeded` with `Retry-After`
in seconds; see [configurable quotas](request-limits.md).

## Generation history

History is returned in pages, newest first (creation time, then ID descending).
Each response contains `object: "list"`, `data` and `has_more`. The `limit`
defaults to 50 and must be an integer from 1 to 100. Choose one paging mode:

- **Cursor:** include `cursor=` with an empty value for the first page. While
  `has_more` is true, copy the response's `next_cursor` into the next request's
  `cursor` parameter. Treat it as an opaque position; do not build it yourself.
  Replace `NEXT_CURSOR` below with that returned value:

  ```http
  GET /v1/generations?limit=50&cursor=
  GET /v1/generations?limit=50&cursor=NEXT_CURSOR
  ```

  New arrivals do not shift subsequent cursor pages. A nonempty page returns
  `next_cursor` even when `has_more` is false; stop paging when `has_more` is
  false. An empty page returns `next_cursor: null`. Cursor responses also include
  `journal_scope` and, unless `include_total=false`, the exact `total` for the
  full viewer/project filter. They do not include `next_offset`.
- **Offset:** omit `cursor`, start with `offset=0`, then use the response's
  `next_offset` as the next request's `offset` while `has_more` is true.
  The offset defaults to 0 and must be an integer from 0 to 1,000,000.
  Offset responses include `next_offset`, not `next_cursor`.

Do not send `offset` with `cursor`, even as `offset=0` or an empty `offset=`;
the API returns HTTP 400. Keep the same authenticated viewer and `project_id`
filter when following a cursor. Omit `project_id` for all visible generations,
use a project UUID for one project, or `project_id=none` for unassigned ones.
Malformed cursors or cursors from another viewer/project filter return HTTP 400.
If permissions change and invalidate a saved cursor or change `journal_scope`,
discard the previous view and restart with `cursor=`.

For refreshing a previously loaded range, cursor mode also accepts `through`,
an inclusive oldest position saved from `next_cursor`. Start again with `cursor=`
and that `through` value, following `has_more`/`next_cursor` within the range.
The total still counts the full filtered history. See the
[OpenAPI contract](openapi.yaml) for `through` and the cursor-only `view`, `ids`
and `include_total` controls.

## Requests and provider extensions

Select the exact public ID from `/v1/models`, initially `provider/model` (for example
`openai/gpt-image-1.5`). System administrators can change this public ID in provider
settings. Use the exact ID returned by the catalog; old names are not aliases. Separate `provider` and `media_type` request fields are not
accepted. The endpoint and model determine the media type.

Standard fields have one location: `prompt` for images/video/music/audio, `input` for
speech, `size`, `seconds` (integer string), image `quality`, `background`,
`output_format`, speech `voice`, `instructions`, `response_format`, image-edit
`image`, and video `input_reference`. These controls are not accepted inside
`options`, even when equal to their top-level counterpart.

Image `1024x1024` is translated to the provider's catalog representation. Video
`1280x720` maps to `720p` and `16:9` when supported; catalog size values such as
`720p` are also supported with `options.ratio`.

`options` contains provider extensions: negative prompts, seeds, reference modes,
video/audio reference arrays, reference voices, audio creation, draft, watermark,
fixed camera, last frame, language, lyrics, gender, catalog-advertised music/audio/video
format, music/audio duration and advertised
`parameters`. Singular `video_url` and `audio_url` are not accepted.

`prompt_optional` permits an empty prompt on media-driven models; `no_prompt`
requires it to be omitted or empty. Respect the model's duration bounds and
`duration_from_input`: a reference-driven duration cannot be overridden.

`webhook_url` and `max_expected_cost_usd` stay top-level gateway extensions.
The price ceiling includes markup. Use the same `Idempotency-Key` when retrying
the same intended generation; use a new key for a new operation. All generation routes normalize into the same core request before
hashing/admission.

### Images

```json
{
  "model": "openai/gpt-image-1.5",
  "prompt": "A mountain lake at sunrise",
  "size": "1024x1024",
  "quality": "medium",
  "output_format": "png",
  "n": 1
}
```

For image requests, `n` is a positive integer up to the selected model's
`capabilities.max_outputs` from `/v1/models`; an omitted capability means a maximum
of one. Omitting `n` selects one output. The saved response options may contain
`output_count` for a larger count; the normalized single-output default is omitted.
Ordinary generation requests reject `options.output_count`: send top-level `n`.

For edits, add `image`: a URL, `{ "image_url": "https://…" }`,
`{ "file_id": "REFERENCE_ID" }`, or an array. Reference IDs come from the existing
`POST /v1/references/uploads` direct-upload library, and are checked against the requesting owner.
`input_reference` accepts these same forms for video.

`/v1/images/edits` also accepts multipart `image` or `image[]` files;
`/v1/videos` accepts multipart forms with an optional `input_reference` image.
Text-only forms require no reference storage or media signer and use the same
model validation as JSON. Multipart file uploads require local storage. With S3,
file uploads return `409 direct_upload_required` after inspecting part headers,
without draining or spooling the file. Upload directly using
`/v1/references/uploads`, then send the saved reference ID or URL. Total multipart size is
32 MiB; at most 16 edit images or one video reference. Each image still obeys the
library's MIME/30 MiB limit and the model's capabilities. Scalar controls are form
strings; `options` is JSON text. Uploaded files are stored in the reusable library,
not embedded in the queued job. If subsequent admission fails (for example budget
exhaustion), the uploaded references remain in the library and can be reused or
deleted. JSON bodies retain the existing 64 KiB limit; use uploads for large files.

### Speech

```json
{
  "model": "openai/gpt-4o-mini-tts",
  "input": "Welcome to Sociaro",
  "voice": "alloy",
  "instructions": "Speak warmly",
  "response_format": "mp3"
}
```

Use models/voices/encodings actually returned by your account catalog. The response
is a job object; download its audio when the status is `completed`.

### Music and sound effects

Music supports `POST /v1/audio/generations` or the generic route. Sound effects,
mixed soundtracks and media post-production use `POST /v1/generations` with the
exact public model ID. For example:

```json
{"model":"sonilo/text-to-music","prompt":"Warm instrumental piano","options":{"duration":30,"format":"wav"}}
```

Fractional effects use the model's declared number parameter rather than an
integer duration field. Video-based Sonilo operations and narration mixing
accept signed reference-library URLs through `options.video_urls` or
`options.audio_urls`. See [Sonilo controls and tariffs](sonilo-provider.md).

## Lifecycle

Creation returns HTTP 202 and a `Location` header matching `status_url`:

```json
{
  "id": "GENERATION_ID",
  "object": "video",
  "status": "queued",
  "model": "mock/mock-video-v1",
  "media_type": "video",
  "created_at": 1770000000,
  "updated_at": 1770000000,
  "completed_at": null,
  "error": null,
  "status_url": "/v1/videos/GENERATION_ID",
  "content_url": "/v1/videos/GENERATION_ID/content"
}
```

Poll until `completed` or `failed`. Intermediate statuses are `queued` and
`in_progress`. On failure, `error` contains `code` and `message`. `created_at` and
`updated_at` are Unix seconds. `completed_at` is the stored result archival time
when known; it is not invented from a later billing update. No synthetic progress
percentage is reported. Existing `options`, `cost`, `usage`, reference and storage
metadata remain extensions. Storage timestamps inside extensions use ISO dates.

`content_url` is present from admission but returns 404 until media is available;
it also returns 404 after result deletion. Authenticate downloads with the same
key. Signed previews, last-frame downloads, result deletion and webhooks
remain available through the documented extension endpoints. Webhook `data` is
the same generation object as the HTTP response. Event types are
`generation.queued`, `generation.in_progress`, `generation.completed`, and
`generation.failed`; envelope `created_at` is also Unix seconds.

## Deliberate differences from OpenAI

- **Asynchronous only:** no immediate image/base64 or speech file response, SSE,
  partial images or synchronous mode. `stream: true` is rejected.
- **Model-dependent output count:** image `n` follows the model's `max_outputs`;
  other media requests reject `n`. Multiple outputs still belong to one
  asynchronous generation, with one task-level status, usage and cost record.
- **Provider controls:** capabilities and parameter limits come from our model
  catalog. Unsupported OpenAI controls (such as masks, input fidelity, speed or
  user tracking when not implemented) are rejected, not silently discarded.
- **No invented APIs:** chat/completions, Responses, fine-tuning, assistants,
  transcription and other unimplemented capabilities are not advertised. File
  management, administration, reports, webhooks, and result deletion retain
  gateway contracts. There is no pretend OpenAI `/files` or video deletion API.
- **Model metadata:** `created` is the provider API object timestamp, or null if
  unavailable. Release/version dates are separate in `capabilities.metadata`.
- **Errors:** `{error: {message, type, param, code}}`; existing gateway codes and
  HTTP 402/409/422 admission errors remain useful extensions.
- **SDKs:** setting `base_url` alone is insufficient for helpers expecting media
  bytes or `data[].b64_json`. Use HTTP/custom job handling and polling. Video
  operations use OpenAI terminology, but the gateway still returns 202 and has
  provider-specific sizes/capabilities; do not assume every SDK method exists.

## Single generation contract

All generation routes use the same parser and response schema. There is no
compatibility header or alternate status/date representation. The console uses
this same API. Historical jobs remain readable; their download URLs are derived
from their IDs so old stored paths do not escape into responses.

Preview access, last-frame downloads and result deletion use
`GET /v1/generations/{id}/media-access`, `GET /v1/generations/{id}/last-frame`
and `DELETE /v1/generations/{id}/content`. Deletion removes files, preserving
history and financial records.

## Reference contracts checked

- [OpenAI image generation](https://developers.openai.com/api/reference/typescript/resources/images/methods/generate)
- [OpenAI image edits](https://developers.openai.com/api/reference/typescript/resources/images/methods/edit)
- [OpenAI video creation](https://developers.openai.com/api/reference/typescript/resources/videos/methods/create)
- [OpenAI speech](https://developers.openai.com/api/reference/cli/resources/audio/subresources/speech/methods/create)
- [OpenAI models](https://developers.openai.com/api/reference/cli/resources/models/methods/retrieve)

OpenAI video generation is itself asynchronous; synchronous image/speech behavior
must not be generalized to all of OpenAI. These sources define naming, not a claim
that the gateway supports every OpenAI feature.

## Model names and organization availability

System administrators edit each model's public ID and default availability in
provider settings. A new model is enabled by default. Public IDs are persisted and
unique across providers; discovery does not reset names or availability. If a new
model's generated name is already occupied, a numeric suffix is assigned once.

Organization settings contain an **Additional models** list. These grants are
additive: a model is visible when it is enabled by default or granted to that
organization. Organization administrators cannot grant themselves access. An
organization grant does not enable a disabled provider or bypass key/account
restrictions. The policy applies to catalogs, estimates and all generation routes;
callers cannot use a hidden model by guessing its ID. Policy read failures reject
requests. Admission rechecks availability transactionally before accepting new work.

Renaming affects new requests. Accepted generations and webhook snapshots retain
the public ID used at acceptance and the native `provider_model` for provenance.
Existing jobs and archived results remain available to their original owners.
