# Kling public pricing fixture

`pricing.md` captures the reviewed video pricing table and billing formulas
from the official English export on 2026-11-20, with trailing whitespace
removed. The raw response SHA-256 was
`91ea30efb8d99d629cfa7779e751f87e5619dc2455d512de1cd69a17e678a406`.
The USD Balance prices and prepaid Units have separate denominations.
[Video pricing](https://kling.ai/document-api/pricing/base/video.md).

On 2026-11-20 the following English endpoint exports each specified the 4k
resolution, SDR default, HDR at 1080p/4k and billing by `usage.output_duration`.
Omni also specified `usage.input_duration` for video and video-character input.

| Official endpoint document | Raw SHA-256 |
| --- | --- |
| [Text-to-video](https://kling.ai/document-api/api/video/4-0/text-to-video.md) | `c982061e9d4da00a70b3d7512f57c92c8e7f72c2a4620cd61b1d79cb06f9f425` |
| [Image-to-video](https://kling.ai/document-api/api/video/4-0/image-to-video.md) | `2b533caa9f87911a8596c45fd85d1ebca48b58f735ffaadafe47a4b77f95e51f` |
| [Omni video](https://kling.ai/document-api/api/video/4-0/video-omni.md) | `7cce1bae11e69d7a5febbe834846fb9062659a97e833888994245146164a2be3` |
