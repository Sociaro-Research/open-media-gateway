package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
)

// S3Config works with AWS S3 and compatible services, including R2 and B2.
// Only encrypted copies of this structure may be persisted.
type S3Config struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	PathStyle       bool   `json:"path_style"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

var ErrInvalidConfig = errors.New("invalid storage configuration")

// Safe domain causes; never wrap an SDK/transport error that may contain secrets.
var ErrPrivateEndpoint = errors.New("storage endpoint blocked by outbound private-address policy")
var ErrEndpointDNS = errors.New("storage endpoint hostname could not be resolved")

func (c *S3Config) normalize() error {
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if strings.HasPrefix(c.Endpoint, "//") {
		c.Endpoint = "https:" + c.Endpoint
	} else if c.Endpoint != "" && !strings.Contains(c.Endpoint, "://") && !strings.HasPrefix(strings.ToLower(c.Endpoint), "https:") && !strings.HasPrefix(strings.ToLower(c.Endpoint), "http:") {
		c.Endpoint = "https://" + c.Endpoint
	}
	c.Region = strings.TrimSpace(c.Region)
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.Prefix = strings.Trim(strings.TrimSpace(c.Prefix), "/")
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("%w: endpoint must be an HTTP(S) origin without credentials, path or query", ErrInvalidConfig)
		}
	}
	if c.Region == "" || len(c.Region) > 63 || strings.ContainsAny(c.Region, "/\\ \t\n\r") {
		return fmt.Errorf("%w: region is required (use auto for Cloudflare R2)", ErrInvalidConfig)
	}
	if len(c.Bucket) < 3 || len(c.Bucket) > 63 {
		return fmt.Errorf("%w: bucket must contain 3 to 63 characters", ErrInvalidConfig)
	}
	for _, ch := range c.Bucket {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '-') {
			return fmt.Errorf("%w: invalid bucket name", ErrInvalidConfig)
		}
	}
	if len(c.Prefix) > 512 || (c.Prefix != "" && !validObjectKey(c.Prefix)) {
		return fmt.Errorf("%w: invalid object prefix", ErrInvalidConfig)
	}
	if strings.TrimSpace(c.AccessKeyID) == "" || strings.TrimSpace(c.SecretAccessKey) == "" {
		return fmt.Errorf("%w: access key ID and secret access key are required", ErrInvalidConfig)
	}
	return nil
}

func validObjectKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
}

type S3 struct {
	client       *s3.Client
	sourceClient *http.Client
	config       S3Config
	maxBytes     int64
	stagingPath  string
}

func NewS3(c S3Config, client *http.Client, maxBytes int64) (*S3, error) {
	if err := c.normalize(); err != nil {
		return nil, err
	}
	if client == nil || maxBytes <= 0 {
		return nil, errors.New("storage client and positive max bytes are required")
	}
	// Never forward signed storage requests to a redirect destination.
	s3HTTP := *client
	s3HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	options := s3.Options{
		Region: c.Region, Credentials: credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
		HTTPClient: &s3HTTP, UsePathStyle: c.PathStyle, RetryMaxAttempts: 3,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if c.Endpoint != "" {
		options.BaseEndpoint = aws.String(c.Endpoint)
	}
	return &S3{client: s3.New(options), sourceClient: client, config: c, maxBytes: maxBytes}, nil
}

func (s *S3) Archive(ctx context.Context, taskID, sourceURL string) (core.StoredMedia, error) {
	if !validObjectKey(taskID) {
		return core.StoredMedia{}, errors.New("invalid task storage key")
	}
	// Stage one bounded file for hashing and retryable uploads; never keep a local
	// permanent copy. Reuse exactly the same source validation as local storage.
	dir, cleanup, err := s.stagingDirectory()
	if err != nil {
		return core.StoredMedia{}, errors.New("create S3 upload staging directory")
	}
	defer cleanup()
	local, err := NewLocal(dir, s.sourceClient, s.maxBytes)
	if err != nil {
		return core.StoredMedia{}, err
	}
	media, err := local.Archive(ctx, taskID, sourceURL)
	if err != nil {
		return core.StoredMedia{}, err
	}
	object, err := local.Open(ctx, media.StorageKey)
	if err != nil {
		return core.StoredMedia{}, err
	}
	defer object.Reader.Close()
	media.StorageKey = path.Join(s.config.Prefix, media.StorageKey)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.config.Bucket), Key: aws.String(media.StorageKey), Body: object.Reader,
		ContentLength: aws.Int64(media.SizeBytes), ContentType: aws.String(media.ContentType),
		Metadata: map[string]string{"sha256": media.SHA256},
	})
	if err != nil {
		return core.StoredMedia{}, safeS3Error("upload", err)
	}
	return media, nil
}

// Check exercises the actual object permissions without listing or creating buckets.
// The uniquely named probe is removed even when the read check fails.
func (s *S3) Check(ctx context.Context) (err error) {
	key := path.Join(s.config.Prefix, ".gateway-check", uuid.NewString())
	payload := []byte("media-gateway-storage-check")
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.config.Bucket), Key: &key, Body: bytes.NewReader(payload), ContentLength: aws.Int64(int64(len(payload))), ContentType: aws.String("application/octet-stream")})
	if err != nil {
		return safeS3Error("write check", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, cleanupErr := s.client.DeleteObject(cleanupCtx, &s3.DeleteObjectInput{Bucket: aws.String(s.config.Bucket), Key: &key})
		if cleanupErr != nil {
			err = safeS3Error("probe cleanup (DeleteObject permission required)", cleanupErr)
		}
	}()
	response, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.config.Bucket), Key: &key})
	if err != nil {
		return safeS3Error("read check", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(len(payload)+1)))
	if err != nil || !bytes.Equal(data, payload) {
		return errors.New("S3 read check returned different content")
	}
	return nil
}

func (s *S3) Open(ctx context.Context, key string) (Object, error) {
	if !validObjectKey(key) {
		return Object{}, errors.New("invalid S3 object key")
	}
	meta, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.config.Bucket), Key: &key})
	if err != nil {
		return Object{}, safeS3Error("open", err)
	}
	size := aws.ToInt64(meta.ContentLength)
	if size < 0 {
		return Object{}, errors.New("invalid S3 content length")
	}
	return Object{Key: key, ContentType: aws.ToString(meta.ContentType), SizeBytes: size, ModTime: aws.ToTime(meta.LastModified),
		Reader: &s3Reader{ctx: ctx, store: s, key: key, size: size, etag: meta.ETag}}, nil
}

// s3Reader maps ServeContent seeks to lazy ranged GETs. HEAD and conditional
// requests do not download the object; video seeking streams only requested bytes.
type s3Reader struct {
	ctx          context.Context
	store        *S3
	key          string
	size, offset int64
	etag         *string
	body         io.ReadCloser
	closed       bool
}

func (r *s3Reader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.offset >= r.size {
		return 0, io.EOF
	}
	if r.body == nil {
		out, err := r.store.client.GetObject(r.ctx, &s3.GetObjectInput{Bucket: aws.String(r.store.config.Bucket), Key: &r.key, Range: aws.String(fmt.Sprintf("bytes=%d-", r.offset)), IfMatch: r.etag})
		if err != nil {
			return 0, safeS3Error("read", err)
		}
		r.body = out.Body
	}
	n, err := r.body.Read(p)
	r.offset += int64(n)
	return n, err
}
func (r *s3Reader) Seek(offset int64, whence int) (int64, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.offset
	case io.SeekEnd:
		offset += r.size
	default:
		return 0, errors.New("invalid seek origin")
	}
	if offset < 0 {
		return 0, errors.New("negative seek offset")
	}
	if offset != r.offset && r.body != nil {
		_ = r.body.Close()
		r.body = nil
	}
	r.offset = offset
	return offset, nil
}
func (r *s3Reader) Close() error {
	r.closed = true
	if r.body != nil {
		return r.body.Close()
	}
	return nil
}

func safeS3Error(operation string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return ErrObjectNotFound
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return fmt.Errorf("S3 %s denied; check credentials and bucket permissions", operation)
		}
	}
	if errors.Is(err, httpclient.ErrOutboundPrivateDestination) {
		return fmt.Errorf("S3 %s failed: %w", operation, ErrPrivateEndpoint)
	}
	if errors.Is(err, httpclient.ErrOutboundDNS) {
		return fmt.Errorf("S3 %s failed: %w", operation, ErrEndpointDNS)
	}
	// SDK errors can contain endpoint URLs, access key IDs and remote response text.
	return fmt.Errorf("S3 %s failed; check endpoint, region, bucket and network connectivity", operation)
}
