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

For routes that use `prompt`, Gateway trims surrounding whitespace and normally
requires nonblank text. An empty prompt is admitted when the selected model
advertises `prompt_optional` or `no_prompt`, or for music with `supports_lyrics`
and nonblank `options.lyrics`. The selected model and effective reference mode
still have to pass native validation: `prompt_optional` does not waive every
mode's requirements. A `no_prompt` model rejects text that remains after trimming.
JSON `prompt: null` behaves as omission on these routes; multipart `prompt=null`
is literal text. Speech uses `input` and rejects `prompt`, including JSON null.

Use the model's `max_prompt_characters` and any reviewed `prompt_policy`; limits
count Unicode code points **after trimming**, separately from transport byte
limits. Ordinary models use the 10,000 ceiling or a lower native bound; manual
models retain 10,000. Only an explicit reviewed native policy can extend it, up
to 40,000.
See [prompt limits](prompt-limits.md) for mixed ASCII/non-ASCII budgets. Respect
the model's duration bounds and `duration_from_input`: a reference-driven duration
cannot be overridden.

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

JSON image edits require `image`: a nonempty URL string, an object containing
exactly one `image_url` or `file_id` string, or a nonempty single-level array mixing
these forms in order. Null, empty strings, nested arrays and extra object keys are
rejected. A plain string is treated as a URL/asset value; use `{ "file_id":
"REFERENCE_ID" }` for a library ID lookup. The file must be an accessible image in
the caller's library or the authorized shared Template library; ID lookup requires
reference storage and a media signer. References may come from direct uploads or
local library uploads. Model capabilities and reference limits still apply.
`input_reference` accepts these same JSON forms for video; omission remains
subject to the selected model's requirements. Provider `asset://ID` values require
an advertised asset-reference capability and are separate from Gateway file IDs.

`/v1/images/edits` also accepts multipart files named `image`, `image[]`, or both.
Either field may repeat; there must be 1–16 files across both names together,
subject to stricter model limits. Multipart edits require files and do not accept
a scalar image URL together with them.

For `/v1/videos` multipart, omit the reference when the selected model allows it,
or send one scalar `input_reference` image URL. To upload an image, send exactly
one file under `input_reference` or the file-only alias `input_reference[]`.
Unlike image edits, these two video fields cannot coexist, and multiple files,
repeated scalar references and scalar/file combinations are rejected. A scalar
`input_reference` is plain text: JSON objects/arrays and library IDs are not
resolved from that text. Use an `application/json` reference object/array for
those forms. Model URL/reference capabilities still apply; merely parsing a
string does not establish admission. Scalar-only video forms require no reference
storage or media signer. Each scalar field is limited to 64 KiB; scalar-only
parsing also caps the form at 1000 parts and an 11 MiB captured prefix including
headers, within the overall multipart limit below.

Multipart file uploads require local reference storage and a media signer. With S3,
file uploads return `409 direct_upload_required` after inspecting part headers,
without draining or spooling the file. Upload directly using
`/v1/references/uploads`, then reference the saved image in a JSON request.
Video forms can instead send its URL as scalar `input_reference`. Total multipart size is
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
  "input": "Welcome to central server",
  "voice": "alloy",
  "instructions": "Speak warmly",
  "response_format": "mp3"
}
```

Send JSON to `POST /v1/audio/speech` with an exact public model ID whose media type
is `speech`. The generic generation and estimate routes use the same Speech fields
when that model is selected; `/v1/audio/generations` selects music instead.

`input`, `voice`, `instructions` and `response_format` belong at the top level and
must be strings when supplied; JSON null is rejected. `input` becomes the core
prompt, and the other three map to the core voice, instructions and format options.
Do not send `prompt`, `output_format` or `options.format`, even as empty strings or
null. `options.voice` and `options.instructions` are also rejected. Surrounding
whitespace is trimmed from `input` before the selected model's text requirements
and Unicode code-point limits apply. Text is normally required; only model
capabilities and native validation can permit omission or blank input.

Use the selected model's advertised capabilities and native requirements. For
preset voices, an omitted or whitespace-only `voice` selects the first advertised
voice when supported; nonblank IDs must match exactly. Saved voice bindings have
their own requirements and conflict with an explicit preset. An omitted or empty
`response_format` selects the first advertised encoding when a format list exists.
There is no universal `alloy` or `mp3` default. Models with fixed output formats may
require omitting this field. Instruction support and limits also vary: for example,
OpenAI `tts-1` models reject nonempty instructions, and Kling TTS rejects nonempty
instructions and `response_format` and requires a matching voice/language choice.

The response is HTTP 202 with a queued JSON job, never immediate audio bytes.
Poll `status_url`; download `content_url` with the same API key when the status is
`completed`. `response_format` controls the eventual audio encoding, not the JSON
job response. Streaming and the standard top-level `speed` control are unsupported.

### Music and sound effects

Music supports `POST /v1/audio/generations` or the generic route. Sound effects,
mixed soundtracks and media post-production use `POST /v1/generations` with the
exact public model ID. The music route selects `media_type=music`; it does not
accept a model whose output type is `audio`. Generic generation and estimate
routes infer the output type from the selected catalog model. For example:

```json
{"model":"sonilo/text-to-music","prompt":"Warm instrumental piano","options":{"duration":30,"format":"wav"}}
```

Use top-level `prompt` for text direction. Put `lyrics`, `language`, `gender`,
`duration` and `format` inside `options`, subject to the selected model. The first
three and `format` accept strings or null; `duration` accepts an integer or null,
not a fractional number or numeric string. Optional null values act as omission.
Do not use the speech fields `input`, `voice`, `instructions` or `response_format`,
or the image field `output_format`, even as empty strings or null.

Surrounding whitespace is trimmed from `prompt`. A nonblank `options.lyrics` can
replace a missing, null or blank prompt only for `music` models advertising
`supports_lyrics`, subject to native validation. It does not waive text requirements
for `audio` models. Models with `prompt_optional` or `no_prompt` keep their separate
requirements. Lyrics are not automatically trimmed or converted into a prompt.

For music models with `supports_gender`, omitted, null or empty `gender` defaults
to `female`; otherwise choose the exact `female` or `male` value. Nonempty gender
is rejected for music models without this capability. An omitted, null or empty
`format` selects the first advertised format when a list exists. Zero, null or
omitted `duration` uses the model's declared default when it has a duration range;
`duration_from_input` models reject a nonzero duration override. Model ranges,
steps and native checks still apply. Whitespace is not trimmed from these options.

These controls are not universal audio features. Sonilo rejects nonempty lyrics,
language and gender; Alibaba Fun Music advertises lyrics, and `fun-music-v1` also
advertises vocal gender. Use only the selected model's documented language and
duration controls; a shared option field does not establish native support.
Fractional effects use the model's declared number parameter rather than an
integer duration field: Sonilo text effects use `options.parameters.duration_seconds`,
while Kling text-to-audio uses `options.parameters.duration` and rejects nonzero
`options.duration` and nonempty `options.format`. Video-based Sonilo operations and narration mixing
accept signed reference-library URLs through `options.video_urls` or
`options.audio_urls`. See [Sonilo controls and tariffs](sonilo-provider.md).

## Lifecycle

Accepted creation returns HTTP 202 and a root-relative `Location` header matching
`status_url`. A matching idempotent replay returns the original generation with
its current status, which may already be terminal; 202 does not mean a new job
was queued or that rendering succeeded. A newly queued video looks like this:

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

Poll `status_url` until `completed` or `failed`. Stored `running` maps to
`in_progress` and `succeeded` maps to `completed`; `queued` and `failed` keep their
names. Video status/content links use `/v1/videos/{id}`; other media use
`/v1/generations/{id}`, independently of the creation route.

For content-authorized views, `error` is null when no stored failure message is
available; otherwise it contains exactly `code: generation_failed` and a sanitized
`message`, not the raw provider response. `created_at` and `updated_at` are Unix
seconds. `completed_at` is Unix seconds from the recorded `stored_at`, only for a
completed generation; it remains null when that archival time is unknown and
never falls back to a later update or billing time. Result deletion does not erase
the recorded completion time. No synthetic progress percentage is reported.
Existing `options`, `cost`, `usage`, reference and storage metadata remain
extensions. Storage timestamps inside extensions use ISO dates.

For viewers with content access, `content_url` is included from admission, even
before an archive is readable. Pending/failed, deleted or missing stored results
return 404. Authenticate downloads with the same authorized key; a returned link
does not grant additional access. Metadata-only views omit content links, errors
and `completed_at`. Signed previews, last-frame downloads, result deletion and
webhooks remain available through the documented extension endpoints and their
existing permissions.

Webhook `data` is a saved lifecycle snapshot using the same base Generation
format. It does not include HTTP-only viewer permission flags, and content,
logging and financial projection can differ from a particular caller's current
HTTP response. Poll the status route for current state. Event types are
`generation.queued`, `generation.in_progress`, `generation.completed`, and
`generation.failed`; envelope `created_at` is the event-record creation time in
Unix seconds, distinct from generation timestamps and the delivery-attempt
timestamp header.

### Archived results

For viewers with content access, a completed generation can include `artifacts`,
an ordered array of archived outputs. Each entry has a local `id`, `media_type`,
`content_url`, `content_type`, `size_bytes` and `sha256`; `format`, `duration` and
`group_id` appear when available. Related format variants can share an opaque
`group_id`. Artifact IDs and group IDs do not expose provider identities or
storage paths. Use the returned values unchanged.

The generation's common `content_url` downloads the first archived result. To
retrieve every output or format variant, download each artifact's `content_url`:
`GET /v1/generations/{id}/artifacts/{artifact}/content` (also supports `HEAD`).
Authenticate these routes with an authorized generation key. Direct-storage
responses may redirect to a temporary signed URL; do not forward Gateway
credentials to the storage host. Jobs using the scalar result contract can omit
`artifacts` and retain their common `content_url`.

The worker archives the complete result array before publishing `completed`;
queued, running, failed and deleted results do not expose artifact arrays.
An interrupted or failed archive can leave private objects or recovery
checkpoints. Provider usage can already have been recorded, so an archive failure
does not establish a refund. Status, usage and cost belong to the generation;
counting artifact entries or format variants does not establish a billable count.

`GET /v1/generations/{id}/media-access` returns expiring preview/download URLs
for the primary result and, when present, each artifact. The root `expires_at`
applies to those URLs; `last_frame_url` is optional. Successful result deletion
removes all stored outputs and last frames for the job, hides `artifacts` and
makes their download routes unavailable, while retaining history and costs.

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
  When admission cannot be confirmed (HTTP 503) for a request without
  `Idempotency-Key`, the response carries `X-Should-Retry: false` so SDKs do not
  resend it automatically. Send an `Idempotency-Key` to make retries safe.

## Single generation contract

The ordinary media creation routes described above share a parser and return the
same Generation job representation, subject to their route-specific request
rules. The generic estimate route uses that parser but returns a cost estimate,
not a job. Template generation uses its own request contract before common
admission; other specialized extensions retain their documented contracts.

There is no compatibility-header switch or alternate status/date representation.
The console calls these same APIs, including the separate extension routes.
Historical jobs remain readable; their download URLs are derived from their IDs
so old stored paths do not escape into responses.

Preview access, last-frame downloads and result deletion use
`GET /v1/generations/{id}/media-access`, `GET /v1/generations/{id}/last-frame`
and `DELETE /v1/generations/{id}/content`. Deletion removes files, preserving
history and financial records.

## Reference contracts checked

- [OpenAI image generation](https://developers.openai.com/api/reference/typescript/resources/images/methods/generate)
- [OpenAI image edits](https://developers.openai.com/api/reference/typescript/resources/images/methods/edit)
- [OpenAI video creation (historical)](https://developers.openai.com/api/reference/typescript/resources/videos/methods/create)
- [OpenAI speech](https://developers.openai.com/api/reference/cli/resources/audio/subresources/speech/methods/create)
- [OpenAI models](https://developers.openai.com/api/reference/cli/resources/models/methods/retrieve)

OpenAI's retired Videos API used asynchronous generation jobs. The linked
video reference is historical: the API shut down on September 24, 2026. These
sources define naming, not current API availability or a claim that the gateway
supports every OpenAI feature.

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
