# Reference library

Image, video and audio reference fields share the organization's reusable library.
Choose from the library or upload files by dropping them or using the file picker.
The library shows the filename and size; selection is indicated by a border.

Supported uploads: PNG/JPEG/WebP/GIF (30 MiB), MP4/MOV/WebM (200 MiB), and
MP3/WAV/OGG/FLAC (50 MiB). The storage limit also applies. Local uploads detect media type from file bytes. Direct S3 uploads require an
allowed declared media type and verify SHA-256 and size using S3 metadata. SHA-256 detects identical content within an organization and reuses the
existing file, regardless of filename. Provider-specific formats and duration,
dimension and file-size limits still apply.

WAV MIME labels `audio/wave`, `audio/wav` and `audio/x-wav` identify the same
format and normalize to `audio/wave` for new uploads and template validation.
Existing uploads using these labels remain usable. Template validation still
inspects the stored bytes and enforces the selected model's supported formats
and limits; WAV support depends on the model.

Drag selected thumbnails to reorder them. The × control removes a reference from
the current generation; Delete in My storage permanently removes its file.
Source deletion is blocked until a provider registration has finished ingesting it.
Manual URLs remain available under each field's **Use … URLs** disclosure.

Models expose `reference_modes`: select a mode before adding files. Modes specify
counts and roles, including first frame, first/last frames, ordinary multimodal
references, video continuation and character animation. Frame labels follow the
image order. The API validates the same modes through `options.reference_mode`.
Generation requests use top-level `image` for images and `input_reference` for
video source images; both accept URLs, `{ "image_url": "..." }`,
`{ "file_id": "..." }`, or ordered arrays. Video and audio references use
`options.video_urls` and `options.audio_urls`. Legacy `options.image_urls`,
`options.video_url` and `options.audio_url` are rejected. Wan 2.7 reference-to-video also accepts a
voice per ordinary image/video through `reference_voices`; use **Add voice
reference** on a selected asset to choose its audio from the library.

## Registered BytePlus assets

Seedance 2.0 and 2.5 models advertise `supports_asset_references: true`. In the
Playground picker, choose **BytePlus** to upload/register, import and select ready
virtual portraits. The picker inserts `asset://<asset ID>` automatically. See the
[provider library workflow and configuration](provider-asset-library.md). Manual
IDs remain available under **Use … URLs or registered assets**, one per line. These can be mixed with ordinary URLs and Gateway library references.
Reordering keeps the same image numbering (`@Image1`, `@Image2`, etc.) and the
selected mode's first/last-frame roles. Video and audio assets use their respective
fields and counts; mode restrictions still apply.

For example, a video request can include:

```json
{
  "model": "bytedance/dreamina-seedance-2-5-260628",
  "prompt": "The character from @Image2 walks through the scene in @Image1.",
  "input_reference": [
    "https://example.com/scene.png",
    {"image_url": "asset://asset-example-character"}
  ],
  "options": {
    "reference_mode": "references",
    "video_urls": ["asset://asset-example-video"],
    "audio_urls": ["asset://asset-example-audio"]
  }
}
```

Register and wait for the asset to become ready in BytePlus before using its ID.
The configured personal or shared BytePlus connection must have access to the same
account and project. Gateway forwards the URI unchanged. Workspace bindings created through the
provider picker are refreshed for readiness; manually pasted external IDs have
no such binding and BytePlus reports unavailable assets or permission errors. A Gateway `file_id` is a separate
library identifier and cannot contain a BytePlus asset ID. Other models reject
`asset://` unless they advertise support. The **Allow inline references** setting
does not block registered assets, since their content is not embedded in requests.

See the [BytePlus virtual portrait asset guide](https://docs.byteplus.com/en/docs/ModelArk/2333565).

## Gateway library storage

Uploads use the organization's selected storage configuration. Local files live under
`STORAGE_LOCAL_PATH/<organization UUID>/references/<reference UUID>/`; S3 objects use
`<configured prefix>/<organization UUID>/references/<reference UUID>/`. Metadata is stored in
PostgreSQL by migration `013_reference_images.sql`. Each record retains its storage
key and S3 connection version, so switching storage does not move or strand old
references. Keep historical connections and their credentials available to read
and delete their files. Back up the library along with the existing storage and
PostgreSQL database.

All library operations require authentication and current organization content
permissions. Developers and Members can list, reuse and delete their own files;
Supermanagers can manage all organization files. Content Officers have read-only
access to all retained files; Managers have no content access. API keys need
`generation` scope. Eligible browser sessions can use the organization-scoped
list and delete routes. List responses contain up to 48 files and support offset pagination.

- `POST /v1/references/uploads`: prepare a direct S3 upload with file metadata.
  PUT the bytes to the returned signed URL, without gateway credentials.
- `POST /v1/references/uploads/{id}/complete`: verify and register the upload;
  safe to retry after a lost response. See [the protocol and CORS setup](direct-storage.md).
- `POST /v1/references`: local storage only, multipart form with one `file` field.
  S3 rejects this endpoint before parsing the body.
- `GET /v1/references?offset=0`: metadata, preview URLs and pagination state.
- `DELETE /v1/references/{id}`: delete the file from its original destination and
  then remove metadata; storage failures leave the record available for a retry.

The returned `url` is a relative, signed capability URL. Resolve it against the
Gateway's public origin and use the resulting absolute URL in the existing
top-level `image` or `input_reference`, or an `options.video_urls` or
`options.audio_urls` array. The console does this automatically. S3 file reads redirect to short-lived signed storage URLs. Before submitting a
queued generation, the worker resolves its stable library URLs into fresh direct
S3 URLs, so providers fetch the files without gateway byte relay or inline base64.
Local references still require a reachable gateway. Private S3 buckets need no
public access; signed URLs grant limited access without exposing secret credentials. The URL grants anyone
holding it file access until the file is deleted or `MEDIA_URL_SIGNING_SECRET`
is rotated. Unlike short-lived output previews, these input URLs do not expire
while generations wait in a queue. Do not put them in public logs or documents.

Reference API and storage tests cover upload validation, signed access, organization
isolation, physical deletion, S3 destination changes, pagination, persistence, direct transfer, retries and
concurrent completion. Migration `032_direct_reference_uploads.sql` adds durable
upload sessions; the worker cleans up expired sessions and unfinished parts.
Run `node scripts/ui/references.cjs` with Playwright installed for browser tests;
`NODE_PATH`, `UI_BROWSER_PATH` and `UI_SCREENSHOT_DIR` work as in the existing UI
smoke suite. It uses mocked API responses and never calls a paid provider.

MP4 average frame rate is calculated from the weighted `stts` sample timing
table in the media timescale, rather than the nominal label or a padded track
header duration. Duration checks use the longer of header and sample duration,
so shorter headers cannot bypass model duration/combined-duration limits.
This does not round 23.976 FPS to 24 or relax provider-specific limits.
