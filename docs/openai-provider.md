# OpenAI provider

Reviewed against official OpenAI documentation on **2026-09-11**.

## Supported workflows

| Workflow | Reviewed model aliases | Gateway media type |
| --- | --- | --- |
| Image generation and editing with image references | `gpt-image-2.5-flare`, `gpt-image-2.5-sunburst`, `gpt-image-2` | `image` |
| Earlier image models, while available | `gpt-image-1.5`, `gpt-image-1-mini`, `gpt-image-1`, `chatgpt-image-latest` | `image` |
| Verbatim text-to-speech | `gpt-4o-mini-tts`, `tts-1`, `tts-1-hd` | `speech` |
| Generated spoken response to a prompt | `gpt-audio-1.5`, `gpt-audio`, `gpt-audio-mini` | `speech` |

The catalog is the intersection of these reviewed aliases and the account's
`GET /v1/models` response, cached for ten minutes. There is no optimistic fallback:
a failed catalog request is an error, and an empty list does not advertise models.
A connection check explicitly refreshes the catalog. Unknown snapshots and new
models do not automatically inherit capabilities or tariffs. A successful key
check proves catalog access; it does not prove model entitlement, sufficient
quota, organization verification, or successful generation.

[Image generation](https://developers.openai.com/api/docs/guides/image-generation)
supports both creation and editing. References use the Images API JSON edit body
(`images: [{image_url: ...}]`), documented in the
[Images API](https://developers.openai.com/api/reference/resources/images).

[Text-to-speech](https://developers.openai.com/api/docs/guides/text-to-speech)
reads the supplied text. By contrast,
[audio Chat Completions](https://developers.openai.com/api/docs/guides/audio-chat-completions)
compose an answer and speak it: the prompt is an instruction, not a verbatim
script. Both produce a downloadable audio file. The audio response must finish
normally; incomplete, refused, or missing audio is rejected.

## Model retirements and scope

As of the review date, saying that Sora's API is already closed is premature:
OpenAI schedules Sora 2 and the Videos API shutdown for **2026-09-24**. They are
not added to a new integration. DALL-E 2 and 3 shut down on **2026-05-12**.
The earlier GPT-4o Audio Preview and Mini Audio Preview aliases shut down on
**2026-05-07** and are also excluded.
GPT Image 1 is scheduled to retire on **2026-10-23**; GPT Image 1.5, GPT Image 1
Mini, and ChatGPT Image Latest on **2026-12-01**. GPT Audio and GPT Audio Mini
are scheduled to retire on **2027-01-20**. Earlier supported aliases show these
dates in the catalog. See [OpenAI deprecations](https://developers.openai.com/api/docs/deprecations).

The [audio API overview](https://developers.openai.com/api/docs/guides/audio)
also describes GPT-Live and Realtime conversation/session APIs, transcription,
and translation. Live sessions are a different public contract from queued jobs
returning final media files. Transcription returns text rather than generated
media. Neither is included here. There is no separate music model advertised by
this adapter.

The adapter exposes a deliberate supported subset of each API's controls:

- Images: one output per task, published size presets, explicit quality, PNG/JPEG/
  WebP, background, and up to 16 PNG/JPEG/WebP image references. Transparent JPEG
  is rejected. Older models do not expose 2.5-only quality values. `auto` quality
  and size, arbitrary custom sizes, masks, compression, partial streaming images,
  and the conversational Responses image tool are not exposed. Medium quality
  and 1024x1024 are the defaults used by both generation and estimation.
- Speech: model-specific preset voices and MP3/WAV/FLAC/Opus/AAC. Instructions are
  supported for mini TTS, not TTS-1/HD. Input is limited to 4,096 characters.
  Language follows the supplied text; the unsupported language selector is hidden.
- Audio replies: text prompt, optional system instructions, model-specific voices,
  MP3/WAV/FLAC/Opus, and a 4,096 completion-token limit. Audio inputs, multi-turn
  conversations, custom voices and raw PCM are not exposed.

Unsupported controls fail validation before a paid request. New capabilities
must be added explicitly, with validation, tests and accurate catalog metadata.

## Connect

1. Open the appropriate personal/shared provider settings and select **OpenAI**.
2. Save an OpenAI project API key with model-list read access and permission to
   use the intended image/audio endpoints. The only supported base URL is
   `https://api.openai.com/v1`.
3. Enable **Available for generation** for that connection. Use **Check connection**
   to refresh catalog access. Model grants and organization settings still apply;
   there is no installation-wide provider allowlist.
5. Generate using a model actually returned for your account. Account quotas and
   verification may still prevent a paid call despite a successful connection check.

Environment bootstrap is optional: `OPENAI_API_KEY`, `OPENAI_BASE_URL`,
`OPENAI_MODEL` (default `gpt-image-2.5-flare`). It seeds the system-owned record;
existing database records and owner-specific credentials remain authoritative.
Generation HTTP requests use the connection's **Request timeout (seconds)**,
defaulting to 30 seconds, with top-level `timeout_seconds` as a per-generation
override. This includes synchronous image, speech and audio responses. The former
`OPENAI_GENERATION_TIMEOUT` environment setting is superseded. Parent context
cancellation still applies.

Keys use the existing encrypted provider record. They are never returned to
clients. Redirects and automatic retries are disabled for OpenAI calls. Raw
vendor errors are not persisted because they may echo prompts or credentials.
Image and audio responses are bounded to 64 MiB; decoded image data is bounded
to 50 MiB. HTTP image references are fetched by OpenAI, not downloaded by a new
Gateway HTTP fetcher. Signed library references can use the existing owner-scoped
inline resolver. The supplied square icon is embedded and served unchanged as SVG.

No separate billing AccessKey fields are shown for OpenAI. Account balance and
invoice reconciliation are not implemented; the billing link opens OpenAI's
console. Do not use an administrative organization key as an inference key.

Example (authenticate with a **Gateway** key, not the OpenAI key):

```json
{
  "model": "openai/gpt-image-2.5-flare",
  "prompt": "A watercolor illustration of a lake",
  "size": "1024x1024",
  "quality": "medium",
  "background": "transparent",
  "output_format": "png"
}
```

Send this body to `POST /v1/generations`. To edit, add `image`. The same
polling, signed result download and lifecycle webhooks apply as for other providers.

## Cost semantics

Tariffs use [OpenAI standard pricing](https://developers.openai.com/api/docs/pricing)
reviewed on 2026-09-11. Each accepted task retains its pricing snapshot. Discounts,
cached-input discounts, taxes and contract pricing are not applied. Metered list
cost is an estimate, never an invoice or confirmed account debit.

Image quotes combine the official image guide's output-token calculator with a
local planning estimate for inputs: UTF-8 byte count plus 32 text tokens and
4,096 image tokens per reference. Those input estimates are heuristics; they are
not exact tokenization or a guaranteed upper bound. Dimensions, image encoding,
model behavior and prompt content affect the actual usage. In particular, 2.5
uses its own calculator settings, not GPT Image 2's quality-to-token mapping.

GPT Audio reserves the full 4,096 output-token limit at its higher audio-output
rate, plus estimated text input; short responses can cost much less. Actual
reported text-output and audio-output tokens are metered at separate rates.

TTS-1/HD estimates use Unicode character counts and their published character
rates. Mini TTS uses a disclosed planning heuristic of 900 characters per minute
and 1,250 audio tokens per minute, plus estimated text input. Speed, language and
instructions can change actual duration. The binary Speech response does not
include usage counters, so these tasks retain estimates and are not falsely
marked as metered or confirmed.

Image and audio Chat responses preserve raw usage. Their normalized component
metrics drive generic multi-component accounting. Missing required counters do
not turn into zero usage and do not produce a partial cost. If the provider does
not return usable usage, the accepted estimate remains. No automatic reconciliation
with OpenAI invoices is claimed.

## Validation and operational limits

Automated checks use HTTP fixtures; no paid OpenAI generation was performed.
Coverage includes each exposed model family, image edits, formats, usage,
credential validation, owner isolation, quoting, malformed responses, cancellation,
redacted errors, immediate-result archival/download, SVG serving and UI behavior.

The existing worker supports immediate data URLs. A worker crash after the
provider accepts a synchronous request but before the result is persisted cannot
be recovered from these OpenAI endpoints. The worker does not resubmit that job,
avoiding a possible duplicate charge. This limitation also exists for other
synchronous providers; adding a durable provider operation would require a
separate design. Large inline results also temporarily increase memory and
PostgreSQL payload size. Neither limitation is hidden behind fake polling.
