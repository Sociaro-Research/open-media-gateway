# Sonilo music and sound effects

Contract reviewed on **2026-10-09** against the official
[API overview](https://platform.sonilo.com/docs/api), endpoint pages,
[task reference](https://platform.sonilo.com/docs/api/get-task),
[OpenAPI](https://sonilo.com/openapi.json) and
[Platform pricing](https://platform.sonilo.com/pricing).

## Setup

Create a key in [Sonilo Platform](https://platform.sonilo.com/api-keys), whose
accounts are separate from the sonilo.com creator application. Add a Sonilo
connection in Providers or Shared providers, save the key, check it and turn on
Available for generation. Existing model access controls apply. The API endpoint
is fixed at `https://api.sonilo.com/v1`.

Discovery calls authenticated `GET /account/services`, caches the result for
five minutes and exposes only enabled, implemented services. Underscore and
hyphen service identifiers are accepted. A failed check never substitutes an
unrestricted fallback catalog. Discovery verifies key/service access, not balance,
trial eligibility or successful generation. Credentials use the existing
encrypted, owner-scoped configuration and never reach the browser.

Environment bootstrap is optional: `SONILO_API_KEY`, `SONILO_BASE_URL` and
`SONILO_MODEL`. Saved console settings become authoritative. No Compose or
migration change is needed.

## Operations

These IDs are Gateway operation profiles, not vendor model versions. Public IDs
default to `sonilo/<operation>` and can be renamed in Models.

| Operation | Output | Required input | Controls |
| --- | --- | --- | --- |
| `text-to-music` | Music | Prompt | 5-360 whole seconds, default 30; music timeline; m4a/wav/mp3 |
| `text-to-sfx` | Audio | Prompt | `parameters.duration_seconds`: 0.5-180 seconds, default 8; aac/wav/mp3/flac |
| `video-to-music` | Music | One video URL | Optional prompt; music timeline; prompt influence; preserve speech/ducking; m4a/wav/mp3 |
| `video-to-sfx` | Audio | One video URL | Optional prompt; SFX timeline; aac/wav/mp3/flac |
| `video-to-video-music` | Video | One video URL | Optional prompt; music timeline; prompt influence; preserve speech/keep original sound/ducking |
| `video-to-video-sfx` | Video | One video URL | Optional prompt; SFX timeline |
| `video-to-sound` | Audio | One video URL | Optional music prompt and separate SFX direction; SFX timeline; preserve speech/ducking; wav/m4a/mp3 |
| `video-to-video-sound` | Video | One video URL | Combined soundtrack controls plus keep original sound |
| `audio-ducking` | Audio | Foreground audio URL and background music URL | Background in `parameters.music_url`; no prompt |
| `video-ducking` | Video | Foreground video URL and background music URL | Audio-ducking endpoint with video foreground; no prompt |

Prompts have a 2,000-character limit. Music/ducking inputs are limited to 360
seconds; SFX/combined video inputs to 480 seconds. Sonilo validates actual remote
duration, codec, size and fetchability. Its default upload limit is 300 MB and
can differ by account. Video-to-video music copies the picture without
re-encoding and excludes animated GIF and VP8 WebM.

`POST /v1/generations` supports every output type. Music also uses
`POST /v1/audio/generations`; video uses `POST /v1/videos`. The console uses the
generic route and renders catalog-driven shared controls.
Media-driven profiles accept empty prompts; ducking hides the unused prompt.
The shared duration control works for music/audio; fractional SFX uses the
existing number parameter. There is no provider-specific screen.

Example music request:

```json
{"model":"sonilo/text-to-music","prompt":"Warm instrumental piano and strings","options":{"duration":30,"format":"wav"}}
```

Example short effect:

```json
{"model":"sonilo/text-to-sfx","prompt":"A crisp interface click","options":{"format":"wav","parameters":{"duration_seconds":0.5}}}
```

For combined sound, `prompt` maps to `music_prompt`, while
`options.parameters.sfx_prompt` independently directs the effects. Video inputs
use `options.video_urls`; audio foreground uses `options.audio_urls`.

## Timelines and results

`options.parameters.segments` is a JSON **string**, matching Sonilo's multipart
form field. Music segments use `start`, `prompt` and an optional `label`:

```json
[{"start":0,"prompt":"Soft piano","label":"intro"},{"start":20,"prompt":"Full strings","label":"chorus"}]
```

Music boundaries start at zero, are at least five seconds apart and leave five
seconds before the end. Labels are intro, verse, pre-chorus, chorus, bridge,
break, silence, outro or none. SFX spans use contiguous `start`, `end`, `prompt`
objects, beginning at zero. Each timeline has 1-30 items and each segment prompt
has 1-200 characters. Gateway validates shape, labels, bounds and continuity;
Sonilo checks the final boundary against the actual video length.

All generation uses multipart forms. Music explicitly uses `mode=async`; NDJSON
streaming is not exposed as a Gateway streaming protocol. The upstream ID and
output selection persist together, so a restarted worker polls without another
submission. `processing`, `succeeded` and `failed` map to the shared states.
Unknown/malformed states fail explicitly.

Gateway archives the primary finished result before success. Speech-preserving
music selects the vocals/music `mux`; requested ducking selects `ducked` when
present. Sonilo can omit ducking when the source has no usable audio, and its
video mix/isolation controls are best-effort. Combined sound selects the final
`output_url`; video profiles select the scored video rather than a separate
stem. Primary parallel stream zero is used. Authenticated/signed downloads,
task reuse and Gateway webhooks use the existing paths. Sonilo itself provides
neither callbacks nor a cancellation endpoint.

## Estimates and billing

Published standard rates: text music $0.00225/s; text SFX $0.0018/s;
video-to-music/video-to-SFX $0.009/s; audio ducking $0.0006/s. Quotes and metering
include the standard billing floor: 10 seconds for music/combined sound/ducking,
3 seconds for SFX.

Separate rates for finished video and combined sound are not published. Enter
the account-confirmed USD/second tariffs in the connection:

| Field | Environment alias | Operation |
| --- | --- | --- |
| `video_music_price` | `SONILO_VIDEO_MUSIC_PRICE` | `video-to-video-music` |
| `video_sfx_price` | `SONILO_VIDEO_SFX_PRICE` | `video-to-video-sfx` |
| `sound_price` | `SONILO_SOUND_PRICE` | `video-to-sound` |
| `video_sound_price` | `SONILO_VIDEO_SOUND_PRICE` | `video-to-video-sound` |

Missing tariffs remain unpriced and block new accounted tasks. Gateway does
not assume an audio tariff also applies to finished video, or that the combined
product costs the sum of independent requests. A configured zero tariff is an
operator estimate, not confirmation of a free provider.

Remote input duration is unknown before generation. Quotes reserve the endpoint
maximum (360 or 480 seconds); the console displays this assumption. Reported
duration meters the accepted tariff afterward. Account discounts, custom
contracts, taxes and free trials are not assumed. Optional upstream `cost` and
failed-task `refunded` scalars remain usage evidence. Signed URLs, generated
titles and raw error messages are excluded. Aggregated account usage cannot
reconcile individual tasks or substitute for invoices; costs remain
estimates/pending billing. HTTP 402, 403 and 429 are classified separately;
paid submissions are never automatically replayed.

## Scope and verification boundaries

The integration delivers one durable primary artifact per task. Multiple paid
variants and instrument-stem bundles are rejected rather than billed and dropped;
they require a shared multi-artifact contract. Upload local files through the
reference library and pass its signed URLs. The adapter does not fetch arbitrary
inputs or create another upload service. Video analysis, proofreading and
dubbing are outside this music/SFX integration.

The public OpenAPI snapshot omits newer video/combined operations, uses integer
SFX durations, omits MP3 music output and conflates music/SFX segment schemas.
The implementation follows the full endpoint references reviewed above.

Controlled fixtures cover all operations, payload validation, authenticated
discovery, restart polling, safe errors, tariffs and billing floors. The isolated
database/queue workflow covers real admission, workers, archival, downloads and
webhooks. Browser fixtures cover catalog-driven controls and connection fields.
These checks do not certify paid Sonilo generation, account balance, actual
invoices or production deployment; those require an operator key and a live run.
