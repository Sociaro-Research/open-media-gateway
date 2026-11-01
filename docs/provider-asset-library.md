# Provider asset libraries

The Playground reference picker supports **My storage** and **BytePlus** for
Seedance 2.0/2.5. Registered virtual portraits are provider assets, distinct from
Gateway files. A provider adapter owns registration and remote identifiers; the
shared service owns workspace bindings and preparation state. Other providers
can implement the same adapter interface without changing the reference fields.

## Configuration and workflow

On the selected BytePlus provider connection, save **Asset library AccessKey ID**,
**Asset library secret**, project and region. These optional encrypted credentials
are separate from inference API keys and billing credentials. Changing them does
not change inference account identity. The current adapter supports the Johor
management API (`ap-southeast-1`) and matching inference region. Environment
bootstrap fields are `BYTEPLUS_ASSET_ACCESS_KEY_ID`,
`BYTEPLUS_ASSET_SECRET_ACCESS_KEY`, `BYTEPLUS_ASSET_PROJECT` (default `default`)
and `BYTEPLUS_ASSET_REGION` (default `ap-southeast-1`). The account must already
have library access and have completed any required console authorization.
The library project must match the inference endpoint's project.

1. Select the BytePlus model in Playground and open its reference library.
2. Choose **BytePlus** as the source. Create/select a virtual portrait group.
3. Use **Upload to BytePlus** or drop files. Alternatively, in **My storage**, use
   **Add to BytePlus** on an existing file, then select a group and register it.
4. Wait for **Ready**. Choosing the card inserts its `asset://` URI automatically.
   Preparation continues after closing the picker and after Gateway restarts.
5. **Import existing assets** lists assets uploaded through the BytePlus console
   and adds the chosen item to this workspace. No second upload is made.

BytePlus `CreateAsset` ingests a publicly reachable URL, not binary/base64.
Gateway first retains the original in My storage, then provides a signed source
URL to the management API. Private S3 files receive a 24-hour signed URL; local
storage requires a public HTTPS `AUTH_PUBLIC_URL`. The URL must remain reachable
until preparation finishes. Original bytes are retained; image processing and
video/audio duration limits are ultimately checked by BytePlus. The adapter does
not change content-filter or moderation settings.

The picker rejects WebM video and OGG/FLAC audio for BytePlus, although these
formats remain available in My storage. The BytePlus path accepts MP4/MOV video,
MP3/WAV audio up to 15 MiB and supported image uploads below 30 MiB. The existing
200 MiB video upload cap also applies. Resolution, aspect ratio, duration and
frame-rate restrictions may differ from the generation model's input limits.
Failed preparation is visible on the card and never treated as Ready.

## State, ownership and deletion

Migration 069 stores registration state and bindings scoped to data owner,
provider resource owner, connection and library scope. The scope includes the
management AccessKey ID, project and region: changing these starts a different
inventory namespace; existing records are retained. A secret rotation with the
same AccessKey ID preserves that scope. Expiring preview URLs are not persisted
as durable identities and are refreshed from BytePlus.

Registration reserves a source binding before making one non-idempotent provider
call. Duplicate requests reuse it. If a response is lost, the state is
**Checking registration**; Gateway looks up the unique registration name instead
of repeating `CreateAsset`. A definitive rejection stays Failed and can be removed
before retrying. An unresolved registration retains its source. An unavailable
connection is shown as Disconnected; restore its configuration to refresh it.

Developers/Members see and manage their own bindings. Supermanagers see all
workspace bindings. Content Officers have read-only access; Managers have no
content access. A shared provider's complete remote inventory requires a matching
system-administrator browser session, or a system-authorized API key. A generation
key issued to an administrator does not acquire that privilege by itself. An
owner using their personal provider can import their own account's inventory.

The selected-reference × removes an item only from the current generation.
**Remove from workspace** detaches its binding and retains the BytePlus file.
**Delete** asks for confirmation before permanently deleting the remote asset;
the My storage original is retained. Deletion is blocked while queued/running
generations use that URI, or another workspace retains a binding. Detach those
bindings first. Task admission and deletion share a database lock, so new work
cannot race past the deletion marker. Source deletion likewise waits for provider
ingestion to finish. These checks cover Gateway jobs/bindings; independently
submitted BytePlus jobs or other applications are outside Gateway's database.

## HTTP API

All routes require existing generation authentication, workspace/resource-owner
binding and content permissions. `{provider}` is the selected connection ID;
`{id}` below is the Gateway binding UUID, not the remote asset ID.

| Route | Operation |
| --- | --- |
| `GET /v1/references/providers/{provider}/groups` | Workspace groups and `can_import` |
| `POST /v1/references/providers/{provider}/groups` | Create AIGC group using `{ "name": "Characters" }` |
| `GET /v1/references/providers/{provider}/groups?inventory=1` | Authorized remote group inventory |
| `GET /v1/references/providers/{provider}/inventory?group=...&next=...` | Authorized remote assets and continuation token |
| `POST /v1/references/providers/{provider}/import` | Bind existing `{ "asset_id": "asset-..." }` |
| `GET /v1/references/providers/{provider}/assets?offset=0` | Up to 48 workspace bindings and pagination |
| `POST /v1/references/providers/{provider}/assets` | Register `{ "reference_id": "...", "group_id": "group-..." }`; returns 202 with state |
| `GET /v1/references/providers/{provider}/assets/{id}` | Refresh one binding |
| `DELETE /v1/references/providers/{provider}/assets/{id}` | Guarded permanent remote deletion |
| `DELETE /v1/references/providers/{provider}/assets/{id}?detach=1` | Remove only the workspace binding |

A binding contains `id`, `asset_id`, `provider`, `group_id`, `name`, `type`,
`status`, optional `source_id`, `preview_url`, `uri` and `error`. Use `uri` in
ordinary ordered generation reference inputs after status becomes `Active`.
Raw externally supplied `asset://` URIs remain supported, but Gateway cannot
verify their ownership/readiness from a workspace binding that does not exist.

## Prompt reference previews

Playground and Templates share the [common UI reference forms](prompt-reference-syntax.md).
Use `@Image1` for concise image references; `Picture` is an image alias. Balanced
angle/square/brace wrappers accept a contiguous, space-separated or underscore
index. The at-sign form permits no space. Bare `Image 1` is ordinary prose.
This recognition is independent of remote asset support and legacy saved recipe
metadata. The prompt is transmitted unchanged; an informational provider API
hint does not select the parser or promise every UI form has native meaning.

Hover a colored marker, tap it on touch screens, or put the caret in it and press
Alt+Down. Escape/outside tap closes a pinned preview. Missing files and unsupported
media produce a field message. The generation API does not impose this UI
mention grammar. Numbering follows each media type's selected order, including
after reordering. Wan's first-frame-plus-references mode excludes the first frame
from reference-image numbering. BytePlus library operations remain distinct
from reference previews and are not enabled for other providers.

## Other provider mechanisms

The October 19 documentation comparison found different storage contracts:
Alibaba [temporary uploads](https://docs.modelstudio.console.alibabacloud.com/en/model-studio/get-temporary-file-url)
produce model/account-bound `oss://` URLs with a 48-hour lifetime and a special
resolve header; the documented feature is Beijing-only and intended for testing.
It is not enabled as a persistent portrait library. OpenAI documents uploaded
Files IDs for [image inputs](https://developers.openai.com/api/docs/guides/images-vision#analyze-images),
a separate contract from trusted `asset://` portraits; this change does not add
an OpenAI Files adapter. The reviewed [Agnes video contract](https://www.agnes-ai.com/en/docs/agnes-video-25)
uses publicly reachable reference URLs and `<Picture N>` markers. The latter is
supported by the preview layer without pretending it is the BytePlus library.

## Official contracts and local verification

- [BytePlus virtual portrait library](https://docs.byteplus.com/en/docs/modelark/private-virtual-avatar-library-guide-preview)
- [CreateAsset](https://docs.byteplus.com/en/docs/modelark/create-asset-api)
- [Portrait inputs and ordered prompt references](https://docs.byteplus.com/en/docs/modelark/seedance-portrait-asset-guide)
- [Wan video API reference](https://www.alibabacloud.com/help/en/model-studio/wan3-video-generation-api-reference)

`provider_assets_test.go` covers durable recovery, source retention, shared
bindings, isolation, task admission and deletion with isolated PostgreSQL.
Adapter tests verify the fixed signed management protocol without a live account.
`scripts/ui/provider-assets.cjs` covers both browser engines with synthetic API
fixtures. These checks do not prove live AK/SK rights, upstream preprocessing,
production migration or a paid generation; verify those separately after release.
