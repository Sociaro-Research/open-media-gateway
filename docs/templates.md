# Templates

Templates is an instance-wide gallery beside Playground. Authorized users share
published prompts and cover media; their uploaded inputs and generated results
retain the normal personal/organization ownership, project, billing, retention
and history rules. Templates uses the existing Playground session key control.
Forgetting, switching or revoking a key invalidates pending reads and inputs.

Select a template title in the gallery. Its details, missing typed references and
generation action appear in the left panel beneath the Playground key control.
Expand **View prompt** to read its plain text and review the existing generation
estimate after supplying the missing references. The gallery stays visible while
selecting or changing templates. Covers show their complete natural aspect ratio;
video previews play on mouse hover and pause on leave. Focus the cover video and press Space/Enter, or tap it on touch devices, to play or pause. There is no separate Play/Pause preview button. Reduced-motion preferences disable hover play. The prompt
and fixed references cannot be edited in this form. Confirming submits an ordinary
asynchronous generation and opens its Playground journal entry. An unavailable
default requires an explicit compatible alternative; no compatible model or a
missing fixed asset blocks generation. Choose an existing private library file or
upload through the same completed-upload flow used by Playground.

Templates supports only the explicit `references` workflow for image/video
models that advertise it. Every published recipe needs at least one reviewed
reference slot, fixed or supplied by the user. Text-only, first/last-frame,
keyframe, hybrid first-frame/reference and continuation workflows remain available
in ordinary generation and are not template authoring choices.

## Authoring and publishing

Only a system **Administrator** can manage templates. Organization administrators
do not acquire this permission. Bearer-based management also requires
`system:manage`; a generation-only key cannot publish, even for an Administrator.
The authoring model catalog uses the instance's shared provider connections.

Everyone uses the same gallery and selection panel. Administrators additionally
see **Create template** in the gallery and **Edit** on their template cards; there
is no separate management mode. Returning from authoring restores the selected
template. Permission changes remove authoring controls without granting new
generation or library access.

1. Select **Create template** and choose its default model and
   References workflow. Only eligible models are offered. Enter a title, optional
   short description and prompt.
   Changing the authoring model or workflow resets draft generation defaults;
   the published revision is preserved until explicit publication.
2. Use **Infer and review slots**. Each stable typed slot has its own index and
   role. Repeated prompt mentions bind once. Numbering must be contiguous within
   each media type. New authoring recognizes the [common UI reference forms](prompt-reference-syntax.md), with `@Image1` recommended; provider hints are informational and prompts are never translated. A prompt without markers needs explicit ordered reference
   counts (or the selected workflow minimum). A new background is never inferred
   implicitly.
3. Upload directly or deliberately select a file from **Template references** to make a slot fixed, or
   leave it for the user. The dance-video/two-face example has two missing image
   slots and one fixed video slot. The shared file keeps its identity and bytes.
4. Choose an existing fixed image/video as the cover, or upload a separate
   image/video cover. A separate cover never becomes a generation input.
5. **Save draft** permits incomplete work within a selected References workflow.
   Known fixed-media incompatibilities are rejected before saving.
   Creation and draft saves require an available reference-capable default model.
   **Publish** requires at least one slot and validates the grammar,
   binding order, default model, complete reference contract, available fixed
   media and cover, then freezes the existing admission defaults.

Draft saves do not alter the current published revision. Every write requires the
current version; concurrent edits return a conflict and retain the browser draft.
Unpublish prevents new template submissions. Removal is a soft operation through
the API. Neither operation deletes original library files, durable copies or
historical results. Physical cleanup requires a separate authorized operation.

## Shared template assets

**Template references** is one reusable collection for the entire instance.
Every Administrator can upload, rename, preview, select and delete unused files,
including files uploaded by another Administrator. Library, fixed-slot and cover
uploads enter this same collection. Uploading alone does not assign a slot.
Selecting a file preserves its asset ID; the same supported file can be a cover
and a reference in several templates. Rename changes display metadata only.
Replacing content requires uploading a new asset and explicitly rebinding uses.

Authenticated ordinary users can browse, read and select shared files for
permitted generation inputs and missing template slots. They cannot upload,
rename or delete shared files, or override fixed template inputs. Personal and
company libraries remain separate; existing private files are never promoted.
The ordinary upload controls still upload to the caller's private library.

Shared metadata uses reserved namespace
`00000000-0000-0000-0000-000000000002` in `reference_images`, with configured
instance storage and existing audit metadata. Migration 079 registers legacy
template-only files using their original IDs, metadata and storage keys, without
moving bytes or rewriting recipes. Existing distinct legacy files with identical
bytes retain their identities. Repeating the upgrade is safe; conflicting asset
identity metadata fails explicitly. No live migration is implied by source changes.

Deletion checks every active template's draft and published cover/reference
bindings under the same database lock used to save bindings. The error lists all
blocking template names, IDs and navigation links. Editing a draft does not
release an asset still used by its published revision. Unpublishing leaves draft
uses protected. Removing a template releases its unusable bindings: the current
lifecycle offers no restore operation. A future restore implementation must
revalidate assets. Removing one of several uses never permits deletion prematurely.

An unused file can be deleted by an Administrator. A committed pending-deletion
state prevents new bindings before physical removal. A storage failure retains
an administrator-visible retry action; pending files cannot be selected. Files
and templates are never deleted as an upload-validation repair. Storage/security
configuration and private-media permissions are unchanged.

Generic library uploads validate supported bytes and global limits. Selected-model
uploads, binding, draft saving, publication and generation retain common validation
and provider limits. The library has no invented universal FPS or duration minimum.

## Compatibility and media

The server resolves the stored revision, prompt and fixed inputs. The template
request accepts only revision, provider/model, missing slot-to-file-ID bindings,
optional project and confirmed cost. Prompt/options overrides, fixed-slot
replacement, extra references, foreign owner file IDs and provider `asset://`
substitutions are rejected. Publication stays locked through admission, so an
unpublish cannot race past its submission check. Admission uses the existing
idempotency, organization/key budget, queue, worker, storage and history pipeline.

PostgreSQL publication row locks and template writes use a separate pool bounded
to two connections, cloned from the existing connector configuration. Callbacks
use the ordinary pool for asset authorization and task admission. Waiting editors
use the lock pool too, so neither lock holders nor queued edits can consume all
downstream connections. Template repository shutdown closes the additional pool.
The shared pool limit is unchanged; row-lock serialization, version checks and
transactional asset membership/visibility are preserved.

For templates with references, model selection requires the same output, reviewed
grammar, workflow and slot roles, plus accepted per-type/combined counts, fixed
settings and actual fixed-media constraints. Publication and availability also
run the current connection's native admission validator with normalized settings
and one placeholder per declared reference. This preflight never quotes, reserves,
stores or queues a task. For example, Wan 2.7 Image Pro 4K remains valid for text
only, but its native image-input restriction blocks a 4K reference recipe before
publication and excludes a legacy invalid revision from availability.
The filter reads the returned
`MediaModel` contract and current key permissions. It contains no provider/model
name allowlist: any native model or operator alias with the complete matching
contract is eligible. Saved manual aliases inherit missing reviewed grammar,
typed namespaces and file limits from their explicitly selected native profile
at read time, without rewriting saved configuration or connection identity. No cross-grammar prose rewriting is performed. References are
inspected again at submission for size, checksum, content type, dimensions,
container duration, frame rate and declared codec constraints. The shared upload inspector
supports PNG/JPEG/GIF/WebP, MP4/MOV/WebM and MP3/WAV/OGG/FLAC. A selected
model may accept only a subset of these formats.
Container inspection is metadata validation, not a full codec decode or a paid
provider acceptance guarantee.

Existing incompatible drafts and published revisions are retained without data
migration, deletion or automatic mode conversion. Administrators can still read
and unpublish them. They are omitted from the consumer gallery; a direct detail
read explains their unavailability and offers no compatible models. Estimate and
submission reject them. To use one again, an Administrator explicitly selects an
eligible model and References mode, infers/reviews the slots and republishes.
Saving a replacement draft leaves the old published revision unchanged until
publication; title-only saves cannot bypass the workflow requirement.

Reference-file metadata is optional and additive. Missing `ReferenceMediaLimits`
or an omitted per-type rule does not erase an adapter's existing workflow,
counts, ordered inputs or prompt grammar. Every actual reference still passes
completed-library ownership, the existing shared upload limits, byte/MIME and
checksum inspection before submission. Supplied provider rules additionally
validate formats, size, dimensions, alpha channels, durations, FPS and codecs.
Unknown optional provider limits are not a claim of unlimited provider inputs or
of paid-provider acceptance. Native admission remains authoritative for its
existing request constraints.

For native workflows without prompt markers, the publisher declares ordered
image/video/audio input counts. Omitted counts use the selected workflow's
minimums. **Infer and review slots** derives stable slots and roles from those
counts; publication rejects extra, reordered or invented bindings. This enables
image-output reference templates without introducing marker tokens or rewriting
the prompt. In this mode the prompt is
opaque native plain text; input order is explicitly reviewed by the publisher.
When common UI markers are present, new authoring infers counts exclusively
from those markers and rejects count overrides. Older stored recipes retain
their original marker parser.

Legacy recipe compatibility checks retain these stored contracts; new authoring uses the common UI reference grammar:

| Model/profile | Preserved semantics and additional validation |
| --- | --- |
| Seedance 2.0 and 2.5 | Separate image/video/audio numbering. 2.5 uses its own 30-second reference limits; `references` explicitly sends `omni_reference_task_type=reference`. Editing, extension and keyframes remain ordinary-generation workflows. |
| Kling 4 image-to-video | Not offered for Templates: these profiles advertise frame/keyframe workflows rather than the explicit `references` mode. Their ordinary generation routes and native validation are unchanged. |
| HappyHorse 1.1 R2V | Bracketed image numbering, 1–9 images, JPEG/PNG/WebP, minimum 400-pixel side and maximum 20 MB. |
| Wan 2.7 R2V | Separate Image N / Video N numbering, combined visual counts and 15-second output (10 seconds with a video input). `reference_voice` belongs to a visual reference and is not a free Audio N input. The provider also requires single-character subject references; this does not imply general multi-person dance equivalence. |
| Wan 3 / Prime | Separate image/video/audio numbering, 10/5/5 reference counts, file constraints and 15-second aggregate video/audio limits. First/last workflows remain outside Templates. |
| Image-output models | Explicit ordered native image inputs, with exact native count/settings validation. Reviewed OpenAI, Seedream and Wan 2.7 Image file constraints supplement the shared upload/inspection checks. |

Provider-managed asset registration is not part of this authoring flow; private
project-scoped asset IDs cannot be substituted into template slots. Different
native marker grammars remain distinct. No cross-grammar rendering or prose
rewriting is performed.

Catalogue synchronization and operational compatibility are separate contracts.
`ReferenceMediaLimits` was added by Templates and is not a prerequisite that older
catalogue snapshots promised to populate. `TestNativeTemplateContracts` checks
actual supported descriptors, inference, typed namespaces and representative
file rules independently of a successful catalogue synchronization report.
`TestTemplateOperationalContractsSurviveCatalogueCapture` also checks the
configured connection/capture path for the reviewed operational fields.

Constraint sources:

- [ModelArk image inputs](https://ai.byteplus.com/ark/region:ap-southeast-1/docs/image-generation-api)
- [OpenAI image editing](https://developers.openai.com/api/docs/guides/image-generation)
- [HappyHorse R2V API](https://help.aliyun.com/en/model-studio/happyhorse-reference-to-video-api-reference)
- [Wan 2.7 R2V API](https://www.alibabacloud.com/help/en/model-studio/wan-video-to-video-api-reference)
- [Wan 3 API](https://www.alibabacloud.com/help/en/model-studio/wan3-video-generation-api-reference)
- [Wan 2.7 Image API](https://www.alibabacloud.com/help/en/model-studio/wan-image-generation-and-editing-api-reference)
- [ModelArk generation task contract](https://ai.byteplus.com/ark/region:ap-southeast-1/docs/create-video-generation-task-api)
- [Agnes Video 2.5 reference limits](https://wiki.agnes-ai.com/en/docs/agnes-video-25)
- [Agnes Video 2.5 Flash differences](https://wiki.agnes-ai.com/en/docs/agnes-video-25-flash)

Template media uses shared asset identities. Published media capabilities remain
valid for queued jobs after ordinary edits and unpublishing. Explicit authorized
asset deletion follows the dependency checks above. Customer uploads and results
retain their existing retention and request-logging minimization rules.

See [OpenAPI](openapi.yaml) for the API and [browser fixtures](../scripts/ui/README.md)
for local-only verification. These source changes do not publish or deploy a release.

## Early reference validation

Playground and Templates share byte inspection and provider-neutral file checks.
Uploads are received or completed privately, then inspected before a selectable
library entry or template copy is returned. Rejected direct uploads have durable
rejection receipts; cleanup failures remain retryable and worker cleanup still
reclaims abandoned temporary objects. Existing user media is never deleted as a
validation repair. Files are not transcoded or silently rewritten.

A model-context upload supplies `validation` with the provider/model, generation
options and optional existing owned `reference_ids` for aggregate checks. The
server resolves current authorized capabilities; supplied URLs or invented limits
are not trusted. Direct upload preparation pins this context through completion.
Generic library uploads apply common format, metadata and size requirements only,
without a universal duration or FPS limit. Binding a stored reference, changing
a model/workflow, and saving known fixed references recheck compatibility before
Save/Publish/Review. Final publication and new-generation admission still check
current constraints. Accepted idempotent retries retain their original task even
if an input later becomes unavailable; failed new admissions create no task.

Errors identify the file/slot, failed constraint, actual value, allowed bound and
corrective action. Video FPS is the **average** from inspected container timing;
a nominal 24 FPS label can coexist with an average below 24 FPS. Provider documents
may not distinguish average from nominal FPS, so these checks do not establish
upstream acceptance or justify weakening a model's reviewed limits. Re-exporting
at a supported constant frame rate is an explicit user action.

Per-visual voice files use a separate `reference_voice` role and reviewed native
voice-file limits where supplied. They never become undeclared free Audio N slots.
Existing-reference checks preserve owner, organization, content-role and creator
filters. Metadata-only Managers cannot use validation to inspect private files.

Stored-reference inspection routes allow 150 seconds for bounded reads and probing,
with a three-minute response budget. Request cancellation and earlier caller
deadlines also stop the work.

### Reference identity and previews

Authoring previews support image, video and audio files. Completed files
retain their filename and immutable asset ID. A failed or cancelled replacement
keeps the previous assignment and says so; it never binds the rejected attempt.
The gallery cover is separate from generation inputs and does not change when a
fixed slot is replaced. Saving a draft retains the published revision until an
explicit Publish. Published detail includes `fixed_references` previews for that revision's bound files. Consumers can select shared files for missing inputs, but cannot replace fixed slots.

Authenticated editor previews explicitly request `preview=1` on the existing
Administrator asset route. This returns same-origin, range-capable media for the
editor blob preview in both session and API-key modes. Normal media URLs and
provider input reads retain direct signed storage delivery. This is an explicit
preview operation, never an automatic relay fallback after a storage error.
Canonical asset authorization precedes applying the trusted physical storage
owner retained by migration 079; private namespaces and storage key guards are
unchanged.
