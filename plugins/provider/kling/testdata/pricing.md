> ## Documentation Index
>
> Fetch the complete documentation index at: https://kling.ai/document-api/llms.txt
> Use this file to discover all available pages before exploring further.

# Video

> Source: https://kling.ai/document-api/pricing/base/video
> Locale: en
> This content is optimized for LLMs. UI-only controls are omitted.

Resource package deduction rules for video generation and related video capabilities.

Kling 4.0 is billed based on total video duration in seconds. The total duration depends on whether input videos are provided, their duration, and the output video duration.

- When input video duration > output video duration: Unit price × (Input video duration + Output video duration)
- When input video duration ≤ output video duration: Unit price × Output video duration × 2

* Input video duration includes reference video duration and video character elements duration.

## Pricing Table

| Model | Features | Billing Type | Price (720P) | Price (1080P) | Price (4K) |
| --- | --- | --- | --- | --- | --- |
| Kling 4.0 | No Input Video (Input videos include reference videos (feature video and base video), and video character element.) | Per second (Total duration = Output duration) | 1.0 Unit ($0.157) /s | 1.5 Units ($0.235) /s | - |
| Kling 4.0 | With Input Video (Input Duration ≤ Output Duration) (Input videos include reference videos (feature video and base video), and video character element.) | Per second (Total duration = Output duration × 2) | 0.6 Units ($0.094) /s | 0.9 Units ($0.141) /s | - |
| Kling 4.0 | With Input Video (Input Duration > Output Duration) (Input videos include reference videos (feature video and base video), and video character element.) | Per second (Total duration = Input duration + Output duration) | 0.6 Units ($0.094) /s | 0.9 Units ($0.141) /s | - |
| Kling 3.0 Turbo | With Native Audio | Per second (Total duration = Output duration) | 0.8 Units ($0.112) /s | 1.0 Unit ($0.14) /s | - |
| Kling 3.0 | No Native Audio | Per second (Total duration = Output duration) | 0.6 Units ($0.084) /s | 0.8 Units ($0.112) /s | 3.0 Units ($0.42) /s |
| Kling 3.0 | With Native Audio | Per second (Total duration = Output duration) | 0.9 Units ($0.126) /s | 1.2 Units ($0.168) /s | 3.0 Units ($0.42) /s |
| Kling 3.0 | Motion Control | Per second (Total duration = Output duration) | 0.9 Units ($0.126) /s | 1.2 Units ($0.168) /s | - |
| Kling 3.0 Omni | No Reference Video Input x No Native Audio (Reference videos include feature video and base video.) | Per second (Total duration = Output duration) | 0.6 Units ($0.084) /s | 0.8 Units ($0.112) /s | 3.0 Units ($0.42) /s |
| Kling 3.0 Omni | No Reference Video Input x With Native Audio (Reference videos include feature video and base video.) | Per second (Total duration = Output duration) | 0.8 Units ($0.112) /s | 1.0 Unit ($0.14) /s | 3.0 Units ($0.42) /s |
| Kling 3.0 Omni | With Reference Video Input x No Native Audio (Reference videos include feature video and base video.) | Per second (Total duration = Output duration) | 0.9 Units ($0.126) /s | 1.2 Units ($0.168) /s | 3.0 Units ($0.42) /s |
| Kling O1 | No Reference Video Input (Reference videos include feature video and base video.) | Per second (Total duration = Output duration) | 0.6 Units ($0.084) /s | 0.8 Units ($0.112) /s | - |
| Kling O1 | With Reference Video Input (Reference videos include feature video and base video.) | Per second (Total duration = Output duration) | 0.9 Units ($0.126) /s | 1.2 Units ($0.168) /s | - |
| Kling 2.6 | No Native Audio | Per second (Total duration = Output duration) | 0.3 Units ($0.042) /s | 0.5 Units ($0.07) /s | - |
| Kling 2.6 | With Native Audio x No Voice Reference | Per second (Total duration = Output duration) | - | 1.0 Unit ($0.14) /s | - |
| Kling 2.6 | With Native Audio x With Voice Reference | Per second (Total duration = Output duration) | - | 1.2 Units ($0.168) /s | - |
| Kling 2.6 | Motion Control | Per second (Total duration = Output duration) | 0.5 Units ($0.07) /s | 0.8 Units ($0.112) /s | - |
| Kling 2.5 Turbo | No Native Audio | Per second (Total duration = Output duration) | 0.3 Units ($0.042) /s | 0.5 Units ($0.07) /s | - |
| Multi-element Video Editing | Multi-element Video Editing | Per 5 seconds (Total duration = Output duration) | 3 Units ($0.42) /5s | 5 Units ($0.7) /5s | - |
| Avatar | Avatar | Per second (Total duration = Output duration) | 0.4 Units ($0.056) /s | 0.8 Units ($0.112) /s | - |
| Avatar | TTS | Per request | 0.05 Units ($0.007) /call | - | - |
| Lip Sync | Lip Sync | Per 5 seconds | 0.5 Units ($0.07) /5s | - | - |
| Lip Sync | Face Recognition | Per request | 0.05 Units ($0.007) /call | - | - |
| Audio Generation | Text to Audio | Per request | 0.25 Units ($0.035) /call | - | - |
| Audio Generation | Video to Audio | Per request | 0.25 Units ($0.035) /call | - | - |
| Audio Generation | Custom Voice | Per request | 0.05 Units ($0.007) /call | - | - |
| Image Recognition | Image Recognition | Per request | 0.1 Units ($0.014) /call | - | - |
