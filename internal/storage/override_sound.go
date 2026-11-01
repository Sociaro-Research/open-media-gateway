package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/soundoverride"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type readerArchive interface {
	archiveReader(context.Context, string, io.Reader, string, int64, string) (core.StoredMedia, error)
}

func (s *S3) archiveReader(ctx context.Context, id string, r io.Reader, kind string, size int64, source string) (core.StoredMedia, error) {
	if !validObjectKey(id) {
		return core.StoredMedia{}, ErrInvalidConfig
	}
	dir, cleanup, err := s.stagingDirectory()
	if err != nil {
		return core.StoredMedia{}, err
	}
	defer cleanup()
	local, err := NewLocal(dir, s.sourceClient, s.maxBytes)
	if err != nil {
		return core.StoredMedia{}, err
	}
	media, err := local.archiveReader(ctx, id, r, kind, size, source)
	if err != nil {
		return media, err
	}
	object, err := local.Open(ctx, media.StorageKey)
	if err != nil {
		return media, err
	}
	defer object.Reader.Close()
	media.StorageKey = path.Join(s.config.Prefix, media.StorageKey)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(media.StorageKey), Body: object.Reader, ContentLength: aws.Int64(media.SizeBytes), ContentType: aws.String(media.ContentType), Metadata: map[string]string{"sha256": media.SHA256}})
	if err != nil {
		return core.StoredMedia{}, safeS3Error("upload", err)
	}
	return media, nil
}
func (s *versionedStore) archiveReader(ctx context.Context, id string, r io.Reader, kind string, size int64, source string) (core.StoredMedia, error) {
	target, ok := s.Store.(readerArchive)
	if !ok {
		return core.StoredMedia{}, ErrInvalidConfig
	}
	ctx = identity.WithResourceOwners(ctx, identity.ProviderOwnerID(ctx), s.storageOwnerID)
	media, err := target.archiveReader(ctx, s.ownerID+"/results/"+id, r, kind, size, source)
	if err == nil && s.connectionID != "" {
		media.StorageKey = "s3:" + s.connectionID + "/" + media.StorageKey
	}
	if err == nil && s.manager != nil {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = s.manager.record(c, media, strings.Split(id, "/")[0])
	}
	return media, err
}

// ArchiveOverride never commits the unmodified provider result. Its temporary
// files use the existing locked staging lifetime and crash-recovery sweep.
func (m *Manager) ArchiveOverride(ctx context.Context, destination Store, id, source string, sound core.SoundOverride) (core.StoredMedia, error) {
	return archiveOverride(ctx, m.local, m, destination, id, source, sound)
}
func (s *Local) ArchiveOverride(ctx context.Context, destination Store, id, source string, sound core.SoundOverride) (core.StoredMedia, error) {
	return archiveOverride(ctx, s, s, destination, id, source, sound)
}
func archiveOverride(ctx context.Context, local *Local, reader Store, destination Store, id, source string, sound core.SoundOverride) (core.StoredMedia, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	target, ok := destination.(readerArchive)
	if !ok {
		return core.StoredMedia{}, ErrInvalidConfig
	}
	staging := &S3{}
	dir, cleanup, err := staging.stagingDirectory()
	if err != nil {
		return core.StoredMedia{}, soundoverride.ErrProcessing
	}
	defer cleanup()
	video, audio, output := filepath.Join(dir, "input"), filepath.Join(dir, "sound"), filepath.Join(dir, "output")
	r, _, size, err := local.openSource(ctx, source)
	if err != nil {
		return core.StoredMedia{}, soundoverride.ErrProcessing
	}
	err = copyBounded(video, r, size, local.maxBytes, "")
	r.Close()
	if err != nil {
		return core.StoredMedia{}, err
	}
	sourceCtx := identity.WithOrganization(ctx, "", false)
	sourceCtx = identity.WithDataOwnerID(sourceCtx, sound.DataOwnerID)
	sourceCtx = identity.WithResourceOwners(sourceCtx, identity.ProviderOwnerID(ctx), sound.StorageOwnerID)
	object, err := reader.Open(sourceCtx, sound.StorageKey)
	if err != nil {
		return core.StoredMedia{}, soundoverride.ErrProcessing
	}
	if object.SizeBytes != sound.SizeBytes {
		object.Reader.Close()
		return core.StoredMedia{}, soundoverride.ErrProcessing
	}
	err = copyBounded(audio, object.Reader, sound.SizeBytes, local.maxBytes, sound.SHA256)
	object.Reader.Close()
	if err != nil {
		return core.StoredMedia{}, err
	}
	kind, err := soundoverride.Replace(ctx, video, audio, output, local.maxBytes)
	if err != nil {
		return core.StoredMedia{}, err
	}
	f, err := os.Open(output)
	if err != nil {
		return core.StoredMedia{}, soundoverride.ErrProcessing
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return core.StoredMedia{}, soundoverride.ErrProcessing
	}
	return target.archiveReader(ctx, id, f, kind, info.Size(), source)
}
func copyBounded(name string, r io.Reader, size, max int64, digest string) error {
	if size > max {
		return soundoverride.ErrProcessing
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return soundoverride.ErrProcessing
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(r, max+1))
	if err != nil || n == 0 || n > max || size >= 0 && n != size || digest != "" && hex.EncodeToString(hash.Sum(nil)) != digest {
		return soundoverride.ErrProcessing
	}
	return errors.Join(f.Sync())
}
