# Provider lifecycle source coverage

Catalogue defaults monitor the official sources below in addition to protocol
and pricing documents. Generic `document` parsing retains normalized evidence
and warns on subsequent changes. Typed lifecycle extraction requires a separate
provider-owned interpreter for the exact source identity. Source registration
alone does not admit models or prove account availability. Following links from
an index is not automatic. Unknown IDs still need a reviewed media adapter contract.

| Provider | Model and availability evidence | Retirement evidence and limits |
| --- | --- | --- |
| BytePlus | [Model list](https://docs.byteplus.com/en/docs/modelark/1330310), [releases](https://docs.byteplus.com/en/docs/modelark/1159178) and the release archive | The [general notice](https://docs.byteplus.com/en/docs/ModelArk/model-deprecation-notice) remains a separate document source from the [regional notice](https://ai.byteplus.com/ark/region:ap-southeast-1/docs/model-deprecation-notice), whose Markdown alias has a reviewed lifecycle interpreter. Matching content does not merge their source IDs or fingerprints. Notification, deprecation, deactivation, exact time and region remain distinct. |
| Alibaba | [Model category index](https://www.alibabacloud.com/help/en/model-studio/model-studio-model-list/), separate [image](https://www.alibabacloud.com/help/en/model-studio/model-list-image-generation), [video](https://www.alibabacloud.com/help/en/model-studio/model-list-video-generation), [speech](https://www.alibabacloud.com/help/en/model-studio/model-list-speech-synthesis) and [music](https://www.alibabacloud.com/help/en/model-studio/fun-music) lists, existing releases and media capability documents | [Maintained lifecycle policy](https://docs.modelstudio.console.alibabacloud.com/en/model-studio/model-depreciation.md) supplements the existing fixed legacy notice. No-new-users, EoM, EoS, region restrictions, postponements and cancellations must remain distinct. The policy is document-only; the fixed-notice parser is not applied to it. |
| MiniMax | [Models overview](https://platform.minimax.io/docs/guides/models-intro.md), [model releases](https://platform.minimax.io/docs/release-notes/models.md), [API releases](https://platform.minimax.io/docs/release-notes/apis.md), existing models FAQ and model-list API documentation | These sources include text models alongside media. A text retirement must not retire a media model. Refusing new users does not establish shutdown for existing paid users. All these feeds are document-only. The API reference is not an account discovery response. |
| Kling | [Video capabilities](https://kling.ai/document-api/guides/capability-map/video.md), [image capabilities](https://kling.ai/document-api/guides/capability-map/image.md) and [API updates](https://kling.ai/document-api/updates/api.md) | API updates already belong to defaults, but may be absent from saved schedules. Markdown content is required evidence; an HTML application shell is insufficient. No dedicated retirement feed has been established. |
| Sonilo | Existing API document plus the separate short and complete OpenAPI documents | [Changelog](https://sonilo.com/docs/changelog), linked by the official [documentation index](https://sonilo.com/llms.txt). These describe API operations, defaults and SDK changes; model release or shutdown dates must not be invented from operation changes. |
| OpenAI | [Complete models index](https://developers.openai.com/api/docs/models/all.md), existing individual reviewed media model documents and [changelog](https://developers.openai.com/api/docs/changelog) | [Deprecations](https://developers.openai.com/api/docs/deprecations) is separate from the changelog. Its registered Markdown alias has a reviewed media-shutdown interpreter. The new index and existing changelog remain unstructured documents. Announcement/deprecation dates are distinct from shutdown dates. |
| Agnes | [Documentation index](https://wiki.agnes-ai.com/llms.txt) and existing seven media/price documents | [Historical video 2.0 retirement document](https://wiki.agnes-ai.com/en/docs/agnes-video-v20.md) is retained independently: it is absent from the current index. A maintained dedicated lifecycle feed remains unresolved; that is not evidence that none exists. No runtime exception is derived from this source declaration. |

The list is a reviewed starting point, not a claim that every future official
notice is discoverable. New source layouts, linked notices and contradictory
dates require further review. Preserve unknown dates and last confirmed prices.
Structured parsing and runtime admission/retirement enforcement need independent
semantic tests; source registration alone cannot establish those guarantees.

Keep reporting outcomes distinct: a successfully applied addition or retirement
belongs in ordinary changes. Warnings describe unresolved work and its reason,
such as an unknown contract, conflicting dates or an unsuccessful disable.
Document-change evidence alone does not establish that either action succeeded.

## Adopting defaults into existing schedules

Optional default changes do not migrate saved schedules. Separately registered
module lifecycle sources are checked alongside saved sources without rewriting
them; see [catalogue synchronization](catalog-sync.md). Review production and staging
independently against fresh schedule versions and installed adapter support.
Missing defaults may represent deliberate administrator removals. Adopt only
explicitly selected sources, preserving custom sources, source order, parser
aliases, enabled state and daily time. Do not silently acknowledge document
fingerprints or warnings during adoption.

An exact existing ID/URL/parser tuple is a no-op without a PUT. An ID collision,
incompatible URL/parser or uncertain removal intent requires review. Before an
authorized write, reread the current version and full source list. A version
conflict returns 409 and requires a new review; never overwrite with a stale
body. A PUT can increment the version and recalculate the next check even when
its content appears unchanged.

Record exact additions in an application receipt. Rollback removes only those
unchanged additions after rereading the latest version and checking subsequent
administrator edits. Never restore an old full schedule body. Adding sources
does not authorize enabling schedules, accepting warnings, synchronizing models,
changing live configuration or making provider calls.
