# Direct Kling integration

Kling 4.0 Flash contracts were reviewed on **2026-11-04**; the existing families
were reviewed on **2026-10-31** against the official
[API index](https://kling.ai/document-api/llms.txt), including its endpoint-specific
Markdown exports. The adapter uses `https://api-singapore.klingai.com` and a
current API key in the Bearer authorization header. Authentication, account
funding and model entitlement are separate: a successful connection check does
not prove that a generation is funded or permitted.

Add a Kling connection, select its reviewed catalogue, save the operator's API
key and enable availability. Environment bootstrap uses `KLING_API_KEY`,
`KLING_BASE_URL` and `KLING_MODEL`. Loading models or estimates does not submit a
generation. The explicit connection check uses the documented free account query;
it discards balances, caches successful authentication for five minutes and
limits fresh checks to one per second per process.

## Operation families

Gateway IDs identify reviewed serializers and exact upstream versions. They are
not arbitrary upstream `model_name` overrides. Public model IDs include the
concrete connection prefix, for example `kling/kling-4.0-omni-video`.

| Family | Gateway operations | Official contract |
| --- | --- | --- |
| Flash video | `kling-4.0-flash-text-to-video`, `kling-4.0-flash-image-to-video`, `kling-4.0-flash-omni-video` | [Text](https://kling.ai/document-api/api/video/4-0-flash/text-to-video.md), [Image](https://kling.ai/document-api/api/video/4-0-flash/image-to-video.md), [Omni](https://kling.ai/document-api/api/video/4-0-flash/video-omni.md) |
| Native video | Text/image operations for4.0,3.0,3.0Turbo,2.6,2.5Turbo | [Video capability map](https://kling.ai/document-api/guides/capability-map/video.md); each specific endpoint supplies its limits |
| Omni video | `kling-4.0-omni-video`, `kling-3.0-omni-video`, `kling-o1-omni-video` | [4Omni](https://kling.ai/document-api/api/video/4-0/video-omni.md), [3Omni](https://kling.ai/document-api/api/video/3-0-omni/video-omni.md), [O1](https://kling.ai/document-api/api/video/o1/video-omni.md) |
| Motion control | `kling-3.0-motion-control`, `kling-2.6-motion-control` | [Motion control](https://kling.ai/document-api/api/video/motion-control.md) |
| Images | Standard3/2.1, Omni3/O1, multi-image, outpainting, AI Multi-Shot | [Image capability map](https://kling.ai/document-api/guides/capability-map/image.md) |
| Audio and Avatar | Text-to-audio, video-to-audio, TTS, Avatar image-to-video | [TTS](https://kling.ai/document-api/api/video/avatar/text-to-speech.md), [Avatar](https://kling.ai/document-api/api/video/avatar.md) |
| Solutions | Effects, Apparel, Goods Studio, Video Commerce, Virtual Try-On | [Apparel](https://kling.ai/document-api/api/ecommerce-replication/apparel.md), [Goods Studio](https://kling.ai/document-api/api/ecommerce-replication/goods-studio.md), [Video Commerce](https://kling.ai/document-api/api/ecommerce-replication/video-commerce.md), [Virtual Try-On](https://kling.ai/document-api/api/ecommerce-replication/virtual-try-on.md) |
| Media editing | Lip-sync and multi-element editing; inspected face inputs are prepared within those workflows | [Recognition](https://kling.ai/document-api/api/video/image-recognition.md), [Face detection](https://kling.ai/document-api/api/video/lip-sync/face-detection.md), [Lip-sync](https://kling.ai/document-api/api/video/lip-sync.md), [Editing](https://kling.ai/document-api/api/video/multi-elements.md) |
| Internal input preparation | Element/voice creation, editing initialization and selections, scoped to an authorized media consumer | [Elements](https://kling.ai/document-api/api/video/4-0/elements.md), [Voices](https://kling.ai/document-api/api/video/4-0/voice-customization.md) |

Use the authorized `/v1/models` response for exact operation IDs, controls,
reference modes, resource schemas and price availability. Capability maps do not
override specific endpoint contracts:4.0's direct API currently exposes720p and
1080p. Flash has its own reviewed routes and 720p-only contract. No undocumented
4K parameter is synthesized.

## Kling 4.0 Flash

Flash is a separate family, not a standard 4.0 alias. All three endpoints accept
integer output durations from 3 to 20 seconds at 720p. Text-to-video supports
21:9, 16:9, 9:16 and 1:1. Image-to-video accepts exactly one first-frame image,
without a separate aspect-ratio control or middle/last keyframes. Gateway accepts
public HTTPS image URLs rather than exposing the provider's inline-base64 input.
Images are JPEG/PNG, with a conservative inclusive 30,000,000-byte ceiling,
dimensions at least 300 pixels and an aspect ratio between 0.4 and 2.5.

Flash Omni supports first-frame/reference images, reference/source videos and
managed Elements, but no standalone voices or middle/last keyframes. The limits
are 10 images, 7 Elements (at most 3 video-character Elements), 5 combined
videos/video-character Elements and 15 total inputs. At most one source video is
allowed. Each video and the combined video/Element duration must be 3–20 seconds.
MP4/MOV inputs use the inclusive 300,000,000-byte, 700–4553-pixel, 8,294,400-pixel
area, 24–60 fps and 0.4–2.5 aspect-ratio limits. Element preparation retains its
separate, stricter source contract; generation-reference limits do not widen it.

Omni's `options.parameters.audio` is `native` or `original`. Original audio
requires an actual reference/source video, not merely a video-character Element.
The provider prefers source-video audio, otherwise the first reference video.
The reference order is preserved. Omni supports adaptive aspect ratio; text and
image routes do not acquire that option. All Flash routes support the existing
watermark control and Gateway task ID, serialized in native nested `options`.
Provider callbacks are not exposed; Gateway polls and archives the primary output.

The prompt policy uses Gateway's explicit ASCII/non-ASCII 26,000/11,000 weighted
budget, as for standard Omni. Omitting duration preserves provider prompt/input
inference. A quote requires an explicit output duration and inspected input
duration when video is present; it never assumes unknown input is zero.

[Published Flash list pricing](https://kling.ai/document-api/pricing/base/video.md)
is USD 0.094 per output second without video input. With video input, including
video-character Elements, it is USD 0.063 times
`output + max(total input, output)`. Thus 10 seconds without video is USD 0.94,
and 10 seconds with at most 10 input seconds is USD 1.26. Standard 4.0 account
overrides do not apply to Flash. Package units, account eligibility and actual
charges remain distinct from these estimates.

Flash task handles bind polling to the operation, owner and connection revision.
The artifact retains its physical file duration. Billable usage comes from
`usage.output_duration` and provider-normalized `usage.input_duration`; missing
or invalid billed output duration remains unknown, while an otherwise valid
primary video can still be archived. Billing records do not become confirmed USD
merely because they were returned. No paid-generation success is implied by the
offline contract tests.

Flash operations declare their exact endpoint documents as expected operational
sources; Omni also expects its Elements document. New default registrations do
not rewrite existing saved schedules. A saved schedule without those exact
Flash pages remains `not_checked`/incomplete for the affected operations.
Successful retrieval and pricing-row evidence alone do not verify the complete
operational contract or acknowledge a changed document.

## Kling4 keyframes and Omni references

The ordinary `kling-4.0-image-to-video` operation accepts ordered image keyframes.
The separate Omni operation accepts image and video references. Kling's ability
to use video references must not be inferred from the ordinary keyframe route.
Templates apply their selected slots, fixed media and settings to each operation's
actual capabilities and validation; provider availability alone is insufficient.

4Omni supports image roles `first_frame`, `middle_frame`, `last_frame` and
`refer_image`, and video roles `feature_video` and `base_video`. Use the catalogue's
`reference_role_schemas` and `options.reference_roles`, keyed by `image_1`,
`video_1`, etc. Omitting inferred duration/aspect controls preserves native
prompt/base-video inference instead of eagerly sending unrelated defaults.

```json
{
  "model": "kling/kling-4.0-omni-video",
  "prompt": "Follow @video_1 and preserve @image_1",
  "seconds": "10",
  "size": "720p",
  "input_reference": ["https://media.example/subject.png"],
  "options": {
    "reference_mode": "references",
    "video_urls": ["https://media.example/reference.mp4"],
    "reference_roles": {"image_1": "refer_image", "video_1": "feature_video"}
  }
}
```

The endpoint permits10images,7Elements (at most3video-character Elements),
5videos including video-character Elements,3voices and15combined inputs. At
most one video is a base video. Video duration is3–30seconds per input and in
aggregate, including Element videos. Reusable video Elements need trusted stored
type/duration evidence; an arbitrary remote ID or client duration cannot satisfy
quotas or pricing. Direct voice references select managed voice IDs; arbitrary
background audio is not a documented voice reference.

The reviewed4Omni prompt policy uses ASCII/non-ASCII budgets26,000/11,000 and
the weighted sum≤1. This is Gateway's explicit classification; the upstream
contract does not define punctuation/emoji/combining-character classifications.
Ordinary4.0 text/keyframe prompts retain Gateway's conservative10,000-character
ceiling.3Omni and O1 have different prompt, video, keyframe, Element, audio and
multi-shot combinations; they do not inherit4Omni's schema.

Managed uploads retain Gateway's ordinary200MiB video limit. An authorized
Kling4 Omni target uses its inclusive300,000,000-byte endpoint limit across
upload, stored-reference validation and template promotion; manual and other
models retain their existing limits. Uninspected external video duration remains unknown;
Gateway does not fetch arbitrary remote references as a side effect of quoting.
A required input-duration estimate cannot treat unknown metadata as zero.

## Media-scoped input preparation

Standalone recognition/resource/state operations are not public generation models.
Follow the [media-only product scope](media-scope.md).

`POST /v1/provider-resources/estimate` and `POST /v1/provider-resources` require
`workflow_model`, an authorized media consumer on the same connection, and accept
local reference UUIDs or an eligible owned generation. The host inspects source
bytes, pins the actual connection/revision and constructs private native inputs.
Public bodies never accept native IDs, source URLs, hashes or duration assertions.
Completed generations return local `resources[{binding_id,reference_id}]`.
Read local bindings with `GET /v1/provider-resources?provider=CONNECTION` and
`GET /v1/provider-resources/{id}`. This is owner-local inventory.

```json
{
  "provider": "kling",
  "model": "kling-selection-init",
  "workflow_model": "kling-multi-elements-editing",
  "kind": "editing_session",
  "subtype": "selection",
  "name": "My selection",
  "reference_ids": ["00000000-0000-4000-8000-000000000001"]
}
```

Editing initialization requires an inspected MP4/MOV of2–5 or7–10seconds,
720–2160pixels on each side and exactly24,30or60fps. Its session lasts24hours.
Face detection uses a separate source contract and host retention policy; its
response exposes local1-based face indices and archived preview selectors.

Selection operations use
`POST /v1/provider-resources/{id}/actions/{add|delete|clear|preview}`, with
`/estimate` appended for a quote. Submit a stable `Idempotency-Key`.

```json
{
  "workflow_model": "kling-multi-elements-editing",
  "expected_revision": 0,
  "frame": {
    "frame_index": 0,
    "points": [{"x": 0.123456789012345, "y": 0.5}]
  },
  "max_expected_usd": 0.1
}
```

`frame` is required only for add/delete. Frame indices are zero-based and less
than `session.frame_count`; coordinates are finite0..1. At most10frames and
10points per frame can be marked. Preserve exact coordinates when deleting.
Clear/preview forbid frame data. Mutations freeze concurrent uses and update the
same binding at revision+1. A failed or uncertain mutation leaves the binding
unusable; only an explicit clear with `recover:true` can restore a known state.
Never silently retry an uncertain paid provider submission.

Standalone resource deletion is not exposed. Existing bindings and previously
admitted lifecycle jobs retain authorized read/poll access; no stored resources
or history are deleted by this scope correction. Face-input preparation uses
`POST /v1/media-inputs/faces` and `/estimate`, with `provider`, `workflow_model`
and one owned `reference_id`. It is available only to a media consumer declaring
a `face_session` input. Read eligible schemas with
`GET /v1/models/{model}/preparations?provider=CONNECTION`. Selection preview is
retained as a genuine media operation; state mutations require the media consumer.


Lip-sync selects one local face index and exactly one allowed sound source. Crop
and insertion controls are milliseconds; crops must fit inspected source duration
and overlap the face interval by at least2seconds. Both volume controls permit0.
Multi-element editing distinguishes addition (1–2cropped JPEG/PNG images), swap
(exactly1) and removal (no image). Prompt aliases use native
`<<<video_1>>>`/`<<<image_1>>>` syntax. Generated TTS reuse selects an owned completed `kling-tts` task and its local
`generated_artifact_id`. The host reopens the archived audio, verifies byte hash,
size and format, inspects duration and pins owner, actual connection revision,
source model, artifact and expiry before admission. The preparation binds that
existing output locally without another provider request or charge; native IDs
remain private. Avatar accepts inspected TTS audio of 2–300 seconds; lip-sync
accepts 2–60 seconds. Both require an output generated within 30 days and use
exactly one audio source: the bound TTS output or the existing upload workflow.
Upload limits and existing upload behavior are unchanged. Already admitted work
retains immutable source evidence; new admission rejects expired/deleted sources.

For media models declaring `supports_presets`, the input library offers **Saved
inputs** and **Official presets** using the shared selector and dialog. Discovery
requires the selected `workflow_model`; it lists official element/voice presets,
not private account inventory. Import uses an opaque five-minute ticket bound to
the owner, provider owner, actual connection/revision and canonical selected
consumer. It creates or reuses a local read-only binding without generating or
registering provider content. Native IDs, provider URLs and previews remain
private. Video presets without inspectable evidence remain unavailable. An
uncertain import is never automatically resubmitted; inspect saved inputs first.

## Pricing and outputs

Published USD Balance prices are synchronized from the separate official
[video](https://kling.ai/document-api/pricing/base/video.md),
[image](https://kling.ai/document-api/pricing/base/image.md),
[effects](https://kling.ai/document-api/pricing/solutions/effects.md) and
[ecommerce](https://kling.ai/document-api/pricing/solutions/ecommerce.md) sources.
Units, USD/CNY cash, list prices, discounts and actual invoices remain separate.
Never apply one universal Unit-to-USD conversion.

4Omni without video input costs output seconds×USD0.157at720p or0.235at1080p.
With reference/base/video-Element input, billed seconds are
`output + max(total_input, output)`, atUSD0.094/0.141 respectively. Missing Element
duration cannot be guessed.3Omni's resource tariff rules remain separate.
Images charge per output image; fixed series count overrides `n`, while automatic
series count is unknown in advance. AI Multi-Shot instead charges per call.

A verified tariff with unknown quantity is retained as `quantity_unknown` and
omits the total; it is not free. Such a request cannot satisfy an explicit cost
cap. Unknown tariffs stay unavailable. Unpublished session/Element costs,
lip-sync partial-block rounding and Avatar/multi-edit mode-to-resolution mapping
are not invented. Optional positive operator-confirmed USD/request fields can
supply explicit account tariffs for these named operations; they are labeled
account-specific and never replaced by a public-source refresh. Effects uses the
optional `effect_prices` (`KLING_EFFECT_PRICES`) JSON object mapping exact
catalogue `effect_scene` keys to positive decimal USD/request strings. Unknown
scene keys, duplicates and malformed prices are rejected. Leaving a scene
unpriced keeps it unavailable; no template-dependent fee is guessed. Existing
`kling4_720p_price`/`kling4_1080p_price` USD/second overrides remain supported.

All required normalized outputs are archived before success: multiple images/audio,
recognition JSON, masks, previews and supported watermarked variants. The
standard Kling 3.0/4.0 video task path returns the primary video only; an
unsolicited alternate `watermark_url` is not archived or returned. The
watermark request option is not supported on this path. Read
`artifacts[{id,media_type,content_url,content_type,size_bytes,sha256}]` from the
normal generation response. `/v1/generations/{id}/media-access` issues signed
per-artifact preview/download links. Owner checks, expiry and deletion apply to
every artifact. Scalar primary-result routes remain compatible.

Selection preview also returns `tracking_output`, whose serialization is not
documented. Gateway preserves it as a scoped private `binary`/`bin` source,
archives at most32MiB under the storage cap, and serves an
`application/octet-stream` attachment. It is never relabeled as validated JSON or
exposed as an upstream URL. Credentials, provider task/resource IDs, inspected
source proof and connection revisions remain private in generation/report views
and download errors.

See [the OpenAI-style API](openai-api.md), [Templates API](template-api.md) and
[testing guide](testing.md). Provider fixtures use synthetic inputs and mocked
HTTP. Local tests do not establish live model entitlement or paid output quality.
