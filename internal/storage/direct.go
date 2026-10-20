package storage

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
)

var ErrDirectUploadRequired = errors.New("S3 files must be uploaded directly; use POST /v1/references/uploads")

// DirectReader returns an empty URL only for storage that is genuinely local.
// Errors must never cause callers to fall back to relaying S3 object bytes.
type DirectReader interface {
	DirectURL(context.Context, string, string, string, string, time.Duration) (string, error)
}

type PendingUpload struct {
	Key          string `json:"key"`
	UploadID     string `json:"upload_id"`
	StorageOwner string `json:"storage_owner"`
	Size         int64  `json:"size"`
	ContentType  string `json:"content_type"`
	SHA256       string `json:"sha256"`
}
type UploadTarget struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func (m *Manager) objectS3(ctx context.Context, key string) (*S3, string, error) {
	id, objectKey, found := strings.Cut(strings.TrimPrefix(key, "s3:"), "/")
	if !strings.HasPrefix(key, "s3:") || !found || !validObjectKey(objectKey) {
		return nil, "", ErrObjectNotFound
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, "", ErrObjectNotFound
	}
	r, err := m.repo.Connection(ctx, id)
	if errors.Is(err, ErrSettingsNotFound) {
		return nil, "", ErrObjectNotFound
	}
	if err != nil {
		return nil, "", err
	}
	c, err := m.decode(r)
	if err != nil {
		return nil, "", err
	}
	prefix := strings.Trim(c.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	if !strings.HasPrefix(objectKey, prefix+identity.OwnerID(ctx)+"/") && !strings.HasPrefix(objectKey, prefix+"references/"+identity.OwnerID(ctx)+"/") {
		return nil, "", ErrObjectNotFound
	}
	store, err := NewS3(c, m.client, m.maxBytes)
	return store, objectKey, err
}
func (m *Manager) DirectURL(ctx context.Context, key, method, contentType, disposition string, ttl time.Duration) (string, error) {
	if !strings.HasPrefix(key, "s3:") {
		return "", nil
	}
	store, objectKey, err := m.objectS3(ctx, key)
	if err != nil {
		return "", err
	}
	signer := s3.NewPresignClient(store.client)
	if method == http.MethodHead {
		req, err := signer.PresignHeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(objectKey)}, s3.WithPresignExpires(ttl))
		if err != nil {
			return "", err
		}
		return req.URL, nil
	}
	input := &s3.GetObjectInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(objectKey), ResponseCacheControl: aws.String("private, no-store")}
	if contentType != "" {
		input.ResponseContentType = aws.String(contentType)
	}
	if disposition != "" {
		input.ResponseContentDisposition = aws.String(disposition)
	}
	req, err := signer.PresignGetObject(ctx, input, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// PlanUpload pins the connection and storage owner before any network operation.
func (m *Manager) PlanUpload(ctx context.Context, id, kind, digest string, size int64) (*PendingUpload, error) {
	r, c, err := m.current(ctx)
	if err != nil {
		return nil, err
	}
	if r.Driver == "gateway" {
		return m.PlanUpload(identity.WithResourceOwners(ctx, identity.ProviderOwnerID(ctx), identity.SharedOwnerID), id, kind, digest, size)
	}
	if r.Driver != "s3" {
		return nil, nil
	}
	if size > m.maxBytes {
		return nil, ErrInvalidConfig
	}
	if r.ConnectionID == "" {
		return nil, ErrInvalidConfig
	}
	dir, err := referenceDirectory(ctx, id)
	if err != nil {
		return nil, err
	}
	return &PendingUpload{Key: "s3:" + r.ConnectionID + "/" + path.Join(c.Prefix, dir, "reference"+extensionForType(kind, "")), StorageOwner: identity.StorageOwnerID(ctx), Size: size, ContentType: kind, SHA256: digest}, nil
}
func uploadContext(ctx context.Context, p PendingUpload) context.Context {
	return identity.WithResourceOwners(ctx, identity.ProviderOwnerID(ctx), p.StorageOwner)
}
func (m *Manager) StartUpload(ctx context.Context, p *PendingUpload) (UploadTarget, error) {
	ctx = uploadContext(ctx, *p)
	store, key, err := m.objectS3(ctx, p.Key)
	if err != nil {
		return UploadTarget{}, err
	}
	sum, err := hex.DecodeString(p.SHA256)
	if err != nil || len(sum) != 32 {
		return UploadTarget{}, ErrInvalidConfig
	}
	if p.UploadID == "" {
		created, err := store.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(key), ContentType: aws.String(p.ContentType), Metadata: map[string]string{"sha256": p.SHA256}})
		if err != nil {
			return UploadTarget{}, err
		}
		p.UploadID = aws.ToString(created.UploadId)
	}
	if p.UploadID == "" {
		return UploadTarget{}, errors.New("storage did not return upload ID")
	}
	signer := s3.NewPresignClient(store.client, func(o *s3.PresignOptions) {
		o.Presigner = uploadPayloadSigner{digest: p.SHA256}
	})
	req, err := signer.PresignUploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(key), UploadId: aws.String(p.UploadID), PartNumber: aws.Int32(1), ContentLength: aws.Int64(p.Size)}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return UploadTarget{}, err
	}
	headers := map[string]string{}
	for name, values := range req.SignedHeader {
		if !strings.EqualFold(name, "host") && !strings.EqualFold(name, "content-length") {
			headers[name] = strings.Join(values, ",")
		}
	}
	return UploadTarget{URL: req.URL, Headers: headers}, nil
}

// uploadPayloadSigner binds the file digest as a signed HTTP header. B2 needs
// header-based SigV4 with the actual payload hash: its presigned URL path accepts
// changed bytes even when x-amz-content-sha256 is signed. R2 uses the presigned
// UNSIGNED-PAYLOAD marker and validates the mandatory signed digest header.
type uploadPayloadSigner struct{ digest string }

func (s uploadPayloadSigner) PresignHTTP(ctx context.Context, credentials aws.Credentials, r *http.Request, _ string, service, region string, signingTime time.Time, opts ...func(*v4.SignerOptions)) (string, http.Header, error) {
	// The SDK may prepopulate a query payload marker. Keep a single, unambiguous
	// digest header; R2 rejects conflicting query/header payload values.
	q := r.URL.Query()
	q.Del("X-Amz-Content-Sha256")
	r.URL.RawQuery = q.Encode()
	r.Header.Set("X-Amz-Content-Sha256", s.digest)
	opts = append(opts, func(o *v4.SignerOptions) {
		o.DisableHeaderHoisting = true
		o.DisableURIPathEscaping = true
	})
	signer := v4.NewSigner()
	if strings.HasSuffix(strings.ToLower(r.URL.Hostname()), ".backblazeb2.com") {
		// The SDK adds this for larger parts; browsers cannot set Expect.
		r.Header.Del("Expect")
		if err := signer.SignHTTP(ctx, credentials, r, s.digest, service, region, signingTime, opts...); err != nil {
			return "", nil, err
		}
		return r.URL.String(), r.Header, nil
	}
	return signer.PresignHTTP(ctx, credentials, r, "UNSIGNED-PAYLOAD", service, region, signingTime, opts...)
}

func missingUpload(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && api.ErrorCode() == "NoSuchUpload"
}
func missingObject(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "404")
}

// CompleteUpload only exchanges S3 metadata. The single part is checksum-bound,
// and completing the multipart upload consumes its upload ID, preventing rewrites.
func (m *Manager) CompleteUpload(ctx context.Context, p PendingUpload) (core.StoredMedia, error) {
	ctx = uploadContext(ctx, p)
	store, key, err := m.objectS3(ctx, p.Key)
	if err != nil {
		return core.StoredMedia{}, err
	}
	sum, _ := hex.DecodeString(p.SHA256)
	checksum := base64.StdEncoding.EncodeToString(sum)
	parts, err := store.client.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(key), UploadId: aws.String(p.UploadID), MaxParts: aws.Int32(2)})
	if err == nil {
		if len(parts.Parts) != 1 || aws.ToBool(parts.IsTruncated) || aws.ToInt32(parts.Parts[0].PartNumber) != 1 || aws.ToInt64(parts.Parts[0].Size) != p.Size || aws.ToString(parts.Parts[0].ETag) == "" || (aws.ToString(parts.Parts[0].ChecksumSHA256) != "" && aws.ToString(parts.Parts[0].ChecksumSHA256) != checksum) {
			return core.StoredMedia{}, fmt.Errorf("%w: uploaded part size or checksum does not match", ErrInvalidConfig)
		}
		part := parts.Parts[0]
		_, err = store.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(key), UploadId: aws.String(p.UploadID), MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{ETag: part.ETag, PartNumber: part.PartNumber, ChecksumSHA256: part.ChecksumSHA256}}}})
	}
	if err != nil && !missingUpload(err) {
		return core.StoredMedia{}, err
	}
	// Also recovers a lost CompleteMultipartUpload response without re-uploading.
	head, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(key)})
	if err != nil {
		return core.StoredMedia{}, err
	}
	if aws.ToInt64(head.ContentLength) != p.Size || aws.ToString(head.ContentType) != p.ContentType || head.Metadata["sha256"] != p.SHA256 {
		return core.StoredMedia{}, fmt.Errorf("%w: uploaded object does not match", ErrInvalidConfig)
	}
	media := core.StoredMedia{StorageKey: p.Key, SizeBytes: p.Size, SHA256: p.SHA256, ContentType: p.ContentType}
	return media, m.record(ctx, media, "")
}

// AbortUpload also discovers a create request whose response was lost before its
// upload ID could be saved. The unique object key is persisted before initiation.
func (m *Manager) AbortUpload(ctx context.Context, p PendingUpload, deleteObject bool) error {
	ctx = uploadContext(ctx, p)
	store, key, err := m.objectS3(ctx, p.Key)
	if err != nil {
		return err
	}
	pager := s3.NewListMultipartUploadsPaginator(store.client, &s3.ListMultipartUploadsInput{Bucket: aws.String(store.config.Bucket), Prefix: aws.String(key)})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, u := range page.Uploads {
			if aws.ToString(u.Key) != key {
				continue
			}
			_, err = store.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(key), UploadId: u.UploadId})
			if err != nil && !missingUpload(err) {
				return err
			}
		}
	}
	if deleteObject {
		_, err = store.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(store.config.Bucket), Key: aws.String(key)})
		if missingObject(err) {
			return m.removed(ctx, p.Key)
		}
		if err != nil {
			return err
		}
		return m.DeleteReference(ctx, p.Key)
	}
	return nil
}
