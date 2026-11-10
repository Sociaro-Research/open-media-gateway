# Qwen Audio 3.1 files

The Alibaba adapter supports these exact non-streaming model IDs:

| Model | Native input | Standard Singapore / Beijing price |
| --- | --- | --- |
| `qwen-audio-3.1-tts-plus` | Text and a reviewed voice | USD 0.30 / 0.20 per 10,000 billable characters |
| `qwen-audio-3.1-tts-flash` | Text and a reviewed voice | Input USD 0.23 / 0.212; output USD 1.87 / 1.696 per million tokens |
| `qwen-audio-3.1-tts-next` | Audio description and optional ordered audio references | Input USD 0.93 / 0.848; output USD 1.87 / 1.696 per million tokens |

Use an existing region-matched API key and an explicit workspace endpoint:
`https://<workspace>.ap-southeast-1.maas.aliyuncs.com/api/v1` or
`https://<workspace>.cn-beijing.maas.aliyuncs.com/api/v1`. The ordinary DashScope
endpoint cannot identify a workspace for these APIs. Gateway does not discover a
workspace, activate a model or accept provider authorization terms. Documentation
and offline tests do not establish the connected account's model access.

Submit a queued file job through `POST /v1/audio/speech`, with a public model ID
such as `alibaba/qwen-audio-3.1-tts-plus`, `input`, `voice`, and optional
`response_format` (`wav` or `mp3`). Custom connections use their connection ID
as the public model prefix. Plus and Flash expose four reviewed shared voices:
`longanlingxin_v3.1`, `xunanchuan_v3.1`, `longanhuan_v3.1`, and
`longanfengyue_v3.1`. They support `instructions`, `options.language`, and seeds
from 0 through 65535. Their text and instruction limit is Gateway's ordinary
prompt envelope, not a claimed provider maximum.

Next accepts up to 3,000 Unicode code points through `input`. It sends native
`text_prompt` and never inherits voice, instruction, language or seed settings.
Use `options.audio_urls` in reference order; `@voice1`, `@voice2`, and `@voice3`
bind to that order. References require authorized, inspected media evidence:
WAV, MP3 or OGG Opus; at most 30 seconds and 10,000,000 bytes each. Raw PCM,
inline `audio_data`, nested reference overrides and conflicting singular/plural
audio sources are rejected. Use the existing reference upload/validation flow;
external URLs without inspected evidence cannot satisfy this contract.

Gateway requests WAV/MP3 files at 24 kHz for Plus/Flash and 48 kHz stereo for
Next. Other native output controls and streaming remain outside this subset.
The worker saves a private accepted-result receipt before downloading. Download
handles bind the owner and provider connection revision, expire with the returned
URL and never expose the signed source URL in public task responses. Downloads
validate MIME, a known container and length within a 128 MiB Gateway envelope.
Interrupted archival resumes the accepted result without another synthesis.
An expired source requires operator investigation, not automatic resubmission.

Plus meters only reported billable characters, even if token fields also appear.
Flash requires both token counters to meter a cost. Next's documented
`usage.duration` is not a token charge basis: its token quantities and monetary
total remain unknown. Missing usage never means free generation. Existing
catalogue source adoption and installation configuration remain separate from
the native adapter implementation.

Contracts and standard tariffs reviewed 2026-11-10:
[TTS HTTP API](https://www.alibabacloud.com/help/en/model-studio/qwen-audio-tts-http-api),
[Next file API](https://www.alibabacloud.com/help/en/model-studio/audio-generation-api),
[voices](https://www.alibabacloud.com/help/en/model-studio/qwen-audio-tts-voice-list),
and [pricing](https://www.alibabacloud.com/help/en/model-studio/model-pricing).

For offline validation and required disposable-service checks, see
[testing](testing.md). Adapter wire/usage fixtures, authenticated HTTP admission
and durable worker receipt recovery are tested separately from paid provider
availability.
