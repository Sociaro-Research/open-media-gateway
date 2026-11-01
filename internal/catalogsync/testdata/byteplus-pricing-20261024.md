Token prices are **USD per million tokens**.
Image generation prices are **per image**.

## Video generation models

### Pricing

> **Tip**

> Video pricing is determined by both the **token rate** and **token consumption**. Under otherwise identical conditions:

* > A video generated at a higher resolution costs more than one generated at a lower resolution.

* > A video generated with video input costs more than one generated without video input.

> You can use the [Seedance 2.0 Series Pricing Calculator](https://bytedance.larkoffice.com/share/base/form/shrcnIHSU1yg0GtzCX4pyW3g3yb) and [Seedance 2.5 Series Pricing Calculator](https://bytedance.larkoffice.com/share/base/form/shrcnIYBa28VajWRTpAPEwR4icf) to estimate the cost of each video.

**Input without video**

|Model |Online inference |||Offline inference |
|---|---|---|---|---|
| |480p and 720p |1080p |4K | |
|dreamina-seedance-2-5-260628 |$10.70 |$11.7 |Not supported |Not supported |
|dreamina-seedance-2-0-260128 |$7.0 |$7.7 |$4.0 |Not supported |
|dreamina-seedance-2-0-fast-260128 |$5.6 |Not supported |Not supported |Not supported |
|dreamina-seedance-2-0-mini-260615 |$3.5 |Not supported |Not supported |Not supported |
|seedance-1-0-pro-250528 |$2.5 |||$1.25 |
|seedance-1-0-pro-fast-251015 |$1 |||$0.5 |

**Input with video**

|Model |Online inference |||Offline inference |
|---|---|---|---|---|
| |480p and 720p |1080p |4K | |
|dreamina-seedance-2-5-260628 |$6.40 |$7.0 |Not supported |Not supported |
|dreamina-seedance-2-0-260128 |$4.3 |$4.7 |$2.4 |Not supported |
|dreamina-seedance-2-0-fast-260128 |$3.3 |Not supported |Not supported |Not supported |
|dreamina-seedance-2-0-mini-260615 |$2.1 |Not supported |Not supported |Not supported |

Retiring models

This model is retiring and will be retired on November 11, 2026.

|Model |Online inference ||Offline inference ||
|---|---|---|---|---|
| |Video with audio |Video without audio |Video with audio |Video without audio |
|seedance-1-5-pro-251215 |$2.4 |$1.2 |$1.2 |$0.6 |

## Image generation models

### Pricing

|Model ID |Input image |Output image |
|---|---|---|
|`dola-seedream-5-0-pro-260628` |* First image: Free<br><br>* From the second image: $0.003<br><br>> Pricing varies by image generation scenario. |* Single-image generation:<br><br>   * Up to 2.61 million pixels (1.5K or lower): $0.045<br><br>   * More than 2.61 million pixels (higher than 1.5K): $0.09<br><br>* Layer decomposition:<br><br>   * Up to 2.61 million pixels (1.5K or lower): $0.0225<br><br>   * More than 2.61 million pixels (higher than 1.5K): $0.045 |
|`dola-seedream-5-0-flash-260915` |Free |$0.018 |
|`seedream-5-0-lite-260128` |Free |$0.035 |
|`seedream-4-5-251128` |Free |$0.04 |
|`seedream-4-0-250828` |Free |$0.03 |
|`seededit-3-0-i2i-250628` |Free |$0.03 |
