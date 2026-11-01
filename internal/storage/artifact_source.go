package storage

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// SourceArchiver is additive: stores without trusted stream support continue to
// satisfy Store. ArchiveSource owns Reader, including rejection and cancellation.
// SizeBytes is exact when nonnegative; -1 denotes an unknown stream length.
type SourceArchiver interface {
	ArchiveSource(context.Context, string, core.ArtifactSource) (core.StoredMedia, error)
}

type artifactContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r artifactContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	if err != nil && err != io.EOF {
		return n, errors.New("provider artifact stream could not be read")
	}
	return n, err
}

func (s *Local) ArchiveSource(ctx context.Context, id string, source core.ArtifactSource) (core.StoredMedia, error) {
	if source.Reader == nil {
		return core.StoredMedia{}, errors.New("provider artifact stream is missing")
	}
	var once sync.Once
	closeSource := func() { once.Do(func() { _ = source.Reader.Close() }) }
	stop := context.AfterFunc(ctx, closeSource)
	defer func() { stop(); closeSource() }()
	kind, params, err := mime.ParseMediaType(source.ContentType)
	if err != nil || len(params) > 0 && !(kind == "text/plain" && len(params) == 1 && strings.EqualFold(params["charset"], "utf-8")) || !(supportedMediaType(kind) || kind == "text/plain" || kind == "application/json" || kind == "application/octet-stream") {
		return core.StoredMedia{}, errors.New("provider artifact stream has an unsupported content type")
	}
	if source.SizeBytes < -1 {
		return core.StoredMedia{}, errors.New("provider artifact stream size is invalid")
	}
	return s.archiveReaderChecked(ctx, id, artifactContextReader{ctx, source.Reader}, kind, source.SizeBytes, "", true)
}

func validateArtifactFile(ctx context.Context, f *os.File, kind string) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return errors.New("provider artifact validation failed")
	}
	reader := bufio.NewReader(artifactContextReader{ctx, f})
	if kind == "application/json" {
		if err := validateArtifactJSON(reader); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("provider artifact stream contains invalid JSON")
		}
		return nil
	}
	// Opaque trusted streams are download-only data, including unknown tracking formats.
	// Never reinterpret their payload or change ordinary URL media validation.
	if kind == "application/octet-stream" {
		return nil
	}
	if kind == "text/plain" {
		return nil
	}
	sample, _ := reader.Peek(512)
	detected := strings.SplitN(http.DetectContentType(sample), ";", 2)[0]
	// Unknown binary signatures retain the existing media policy, while recognized
	// HTML/text or a different media family cannot masquerade as a typed artifact.
	if detected != "application/octet-stream" && detected != kind && !(kind == "audio/wav" && detected == "audio/wave") && !(kind == "audio/x-wav" && detected == "audio/wave") {
		return errors.New("provider artifact stream does not match its declared content type")
	}
	return nil
}

func (s *S3) ArchiveSource(ctx context.Context, id string, source core.ArtifactSource) (core.StoredMedia, error) {
	// Local owns and closes the stream after staging. Close on earlier failures too.
	if !validObjectKey(id) {
		if source.Reader != nil {
			_ = source.Reader.Close()
		}
		return core.StoredMedia{}, errors.New("invalid task storage key")
	}
	dir, cleanup, err := s.stagingDirectory()
	if err != nil {
		if source.Reader != nil {
			_ = source.Reader.Close()
		}
		return core.StoredMedia{}, errors.New("create S3 upload staging directory")
	}
	defer cleanup()
	local, err := NewLocal(dir, s.sourceClient, s.maxBytes)
	if err != nil {
		if source.Reader != nil {
			_ = source.Reader.Close()
		}
		return core.StoredMedia{}, err
	}
	media, err := local.ArchiveSource(ctx, id, source)
	if err != nil {
		return core.StoredMedia{}, err
	}
	object, err := local.Open(ctx, media.StorageKey)
	if err != nil {
		return core.StoredMedia{}, err
	}
	defer object.Reader.Close()
	media.StorageKey = path.Join(s.config.Prefix, media.StorageKey)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(media.StorageKey), Body: object.Reader, ContentLength: aws.Int64(media.SizeBytes), ContentType: aws.String(media.ContentType), Metadata: map[string]string{"sha256": media.SHA256}})
	if err != nil {
		if ctx.Err() != nil {
			return core.StoredMedia{}, ctx.Err()
		}
		return core.StoredMedia{}, safeS3Error("upload", err)
	}
	return media, nil
}

func (m *Manager) ArchiveSource(ctx context.Context, id string, source core.ArtifactSource) (core.StoredMedia, error) {
	selected, err := m.ForArchive(ctx)
	if err != nil {
		if source.Reader != nil {
			_ = source.Reader.Close()
		}
		return core.StoredMedia{}, err
	}
	target, ok := selected.(SourceArchiver)
	if !ok {
		if source.Reader != nil {
			_ = source.Reader.Close()
		}
		return core.StoredMedia{}, errors.New("trusted artifact stream storage is unavailable")
	}
	return target.ArchiveSource(ctx, id, source)
}

func (s *versionedStore) ArchiveSource(ctx context.Context, id string, source core.ArtifactSource) (core.StoredMedia, error) {
	target, ok := s.Store.(SourceArchiver)
	if !ok {
		if source.Reader != nil {
			_ = source.Reader.Close()
		}
		return core.StoredMedia{}, errors.New("trusted artifact stream storage is unavailable")
	}
	ctx = identity.WithResourceOwners(ctx, identity.ProviderOwnerID(ctx), s.storageOwnerID)
	media, err := target.ArchiveSource(ctx, s.ownerID+"/results/"+id, source)
	if err == nil && s.connectionID != "" {
		media.StorageKey = "s3:" + s.connectionID + "/" + media.StorageKey
	}
	if err == nil && s.manager != nil {
		accounting, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = s.manager.record(accounting, media, strings.Split(id, "/")[0])
	}
	return media, err
}
