# Direct S3 file transfers

The gateway authorizes access and exchanges metadata. It does not relay S3 file
bytes for library uploads, previews, downloads or provider reference inputs.
Local storage still necessarily uses the gateway. Workers retain their bounded
provider-result download/archive path where providers cannot write to our bucket.

## Upload protocol

1. Hash the file with SHA-256 and POST `{name, content_type, size_bytes, sha256}` to
   `/v1/references/uploads`. The console hashes in a worker with 1 MiB chunks.
2. For `mode=s3`, PUT the original file directly to `target.url` with
   `target.headers`. Do not attach gateway credentials. The browser sets the signed
   Content-Length automatically from the File. Do not wrap the file in FormData.
3. POST `/v1/references/uploads/{id}/complete`, without a file body. Retrying this
   endpoint recovers a lost completion response without another upload.
4. Use the returned library `id` / `url` in a generation request.

`mode=existing` returns an existing owner-scoped reference. `mode=local` explicitly
selects the legacy multipart endpoint. S3 never falls back to gateway uploads.
This also applies to multipart image-edit/video generation endpoints: S3 clients
must prepare a reference and submit the generation as JSON.

The S3 upload is a single multipart part, even for a small file. Its signed request
binds the upload ID, part number, exact size and mandatory `x-amz-content-sha256`
header. Backblaze B2 uses payload-signed SigV4 `Authorization` and `x-amz-date`
headers; its presigned-URL mode does not validate that digest against the bytes.
Other services retain the presigned URL. Always send every returned target header.
Storage validates the received bytes against that signed digest. Completion checks S3's
part metadata, completes that exact part and checks the completed object's metadata.
Completing consumes the upload ID, so a still-valid URL cannot overwrite a saved
file. No staging copy or server-side file download is necessary. Declared MIME
must be an allowed media type; direct uploads do not inspect content bytes on the
server. The object is served with that fixed media type, never as HTML or SVG.

Preparation records the unique destination before contacting S3. PostgreSQL row
and per-owner/digest locks serialize competing completions and cleanup, while
unrelated uploads proceed concurrently. The original storage connection and billing
owner remain pinned across settings changes. The worker reclaims unfinished parts
and unregistered objects after the 30-minute session plus a one-hour grace period.
Completed receipts are retained until this cleanup for safe retries. Cleanup errors
retain the record for the next attempt. Configure an incomplete-multipart lifecycle
rule as an additional safeguard during prolonged worker downtime.

## Bucket configuration

Use a publicly reachable HTTPS S3 endpoint and keep the bucket private. The S3
implementation must validate signed `x-amz-content-sha256` upload headers and
support multipart operations (CreateMultipartUpload, UploadPart, ListParts,
CompleteMultipartUpload), signed GET/HEAD response headers,
and listing/aborting multipart uploads. Unsupported checksum behavior fails closed;
it must not silently trust a client-supplied hash or proxy the upload. The optional
`x-amz-checksum-sha256` multipart extension is not required: the deployed R2 service
rejects that extension. Release verification confirmed that the signed payload
header accepts matching bytes and rejects modified bytes on R2. Other compatible
storage implementations must pass this integrity check before use.

When saving an S3 connection, Gateway checks browser access for the configured
`UI_PUBLIC_URL` (falling back to `API_PUBLIC_URL`). If missing, it reads the current
S3 CORS document, appends a rule limited to that frontend origin, writes it, and
confirms the persisted rules. Existing rules, bucket privacy and lifecycle settings
are preserved. A ready bucket does not require CORS management permissions.

The deterministic per-origin rule ID makes repeated saves idempotent. An existing
Gateway rule for the same origin is extended only with missing methods/headers;
foreign rules are retained. Read failures, network failures and redirects do not
trigger policy replacement. Buckets at the 100-rule limit fail with an actionable
message. S3 CORS writes replace a whole document and have no conditional-update
API: avoid concurrent external CORS edits while saving a connection. A database
save failure cannot roll back a successful external bucket update.

**Test connection** checks object read/write/delete plus unsigned PUT, GET and
HEAD preflights without changing CORS or activating settings. Missing browser
access returns `storage_browser_setup_required`; the shared editor offers
**Set up browser uploads** even for an unchanged saved connection. After automatic
setup, Save can return `browser_uploads_pending: true`: the policy is confirmed,
but provider caches may take about a minute to apply it. The editor retries checks
for up to a minute, then leaves Test connection available if propagation continues.

Headless requests also use the configured frontend origin. Only installations
without public URLs and requests without an Origin retain the server-only check.
The origin cannot be supplied in the settings JSON. A real browser upload remains
the end-to-end acceptance check.

CORS management permissions, needed only when browser access is missing:

| Provider | Key permissions |
| --- | --- |
| AWS S3 | `s3:GetBucketCORS` and `s3:PutBucketCORS` on the bucket ARN |
| Backblaze B2 | `readBuckets` and `writeBuckets`, in addition to object permissions |
| Cloudflare R2 | Admin Read & Write token permission for bucket configuration |
| Custom S3 | Equivalent GetBucketCors/PutBucketCors support and permissions |

B2's bucket-management capability and R2's Admin token can grant broader access
than object-only credentials. Existing browser-ready buckets can retain narrower
object credentials. See [B2 capabilities](https://www.backblaze.com/docs/cloud-storage-application-key-capabilities)
and [R2 token permissions](https://developers.cloudflare.com/r2/api/tokens/).
The S3 API preserves B2 Native API-only rules separately; see
[B2 CORS behavior](https://www.backblaze.com/docs/cloud-storage-cross-origin-resource-sharing-rules).

For reference, this is the added rule (with a deployment-specific ID and origin):

```json
{
  "CORSRules": [{
    "AllowedOrigins": ["https://gateway.example.com"],
    "AllowedMethods": ["PUT", "GET", "HEAD"],
    "AllowedHeaders": ["Content-Type", "Authorization", "x-amz-*", "Range"],
    "ExposeHeaders": ["ETag"],
    "MaxAgeSeconds": 3600
  }]
}
```

For Backblaze B2, `Authorization` must be explicitly allowed; a wildcard
response header does not cover it. CORS setup uses Content-MD5 for the serialized
XML instead of SDK-default flexible checksums, which are not uniformly supported
by S3-compatible providers. See [R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/).

CORS grants browser access, not object authorization. Each upload/read still needs
its signed URL or temporary request signature. Bucket privacy is not changed.
Object permissions: `s3:PutObject`, `s3:GetObject`, `s3:DeleteObject`,
`s3:ListMultipartUploadParts`, `s3:AbortMultipartUpload`. Bucket permission:
`s3:ListBucketMultipartUploads`. Versioned deletion additionally needs the existing
`s3:ListBucketVersions` and `s3:DeleteObjectVersion` permissions. Equivalent rights
are required on compatible storage. Server-side connection checks alone cannot
prove browser CORS readiness; exercise a browser upload before rollout.

## Reads and provider references

Existing gateway URLs remain stable and authorize each access. S3 files return a
307 redirect to a 15-minute signed S3 URL; local files retain byte responses.
Follow redirects, including for HEAD and Range requests. Do not forward gateway
credentials across hosts. Use the signed media-access URLs without API headers
for downloads. If calling the authenticated content endpoint, use a client that
strips credentials on cross-host redirects; curl strips Authorization but may
forward a custom X-API-Key header. Do not use `--location-trusted`. An already
issued S3 URL remains usable until expiry or object deletion.

Stored tasks keep stable library URLs. Immediately before submission, the worker
replaces owned image, video, audio and voice references with fresh 24-hour signed
S3 URLs. External URLs are unchanged. Providers fetch S3 directly. S3 references
are never converted to inline base64, even when an adapter prefers inline images.

## Browser hashing dependency

`web/public/ui/sha256-vendor.js` is the unmodified js-sha256 0.11.1 source from
https://cdn.jsdelivr.net/npm/js-sha256@0.11.1/src/sha256.js (MIT; license adjacent).
It is served locally, uses no runtime CDN, and runs in a dedicated worker. The
worker is terminated on completion, error or workspace cancellation.

Protocol references: [AWS multipart uploads](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html),
[AWS UploadPart](https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPart.html),
[R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/).
