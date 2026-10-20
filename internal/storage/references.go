package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
)

func referenceDirectory(ctx context.Context, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", ErrObjectNotFound
	}
	owner := identity.OwnerID(ctx)
	if !validObjectKey(owner) || strings.Contains(owner, "/") {
		return "", ErrObjectNotFound
	}
	return owner + "/references/" + id, nil
}

func (s *Local) StoreReference(ctx context.Context, id string, data []byte, kind string) (core.StoredMedia, error) {
	dir, err := referenceDirectory(ctx, id)
	if err != nil {
		return core.StoredMedia{}, err
	}
	if int64(len(data)) > s.maxBytes {
		return core.StoredMedia{}, fmt.Errorf("reference exceeds storage size limit")
	}
	name, extension := mediaFileName(kind)
	key := filepath.ToSlash(filepath.Join(dir, name+extension))
	target, err := s.resolve(key)
	if err != nil {
		return core.StoredMedia{}, err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return core.StoredMedia{}, err
	}
	temporary, err := s.createTemporary(filepath.Dir(target), "reference")
	if err != nil {
		return core.StoredMedia{}, err
	}
	defer func() {
		if !temporaryLockSupported {
			_ = temporary.Close()
		}
		_ = os.Remove(temporary.Name())
		_ = temporary.Close()
	}()
	if _, err = temporary.Write(data); err != nil {
		return core.StoredMedia{}, err
	}
	if err = temporary.Sync(); err != nil {
		return core.StoredMedia{}, err
	}
	if !temporaryLockSupported {
		if err = temporary.Close(); err != nil {
			return core.StoredMedia{}, err
		}
	}
	if err = os.Rename(temporary.Name(), target); err != nil {
		return core.StoredMedia{}, err
	}
	sum := sha256.Sum256(data)
	return core.StoredMedia{StorageKey: key, ContentType: kind, SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}
func (s *Local) DeleteReference(ctx context.Context, key string) error {
	if !validObjectKey(key) || !ownedReferenceKey(key, "", identity.OwnerID(ctx)) {
		return ErrObjectNotFound
	}
	file, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(path.Dir(file))
	return nil
}

// StoreReference accepts bytes only for local storage. This final guard also
// covers a storage-setting change while a legacy multipart request is in flight.
func (m *Manager) StoreReference(ctx context.Context, id string, data []byte, kind string) (core.StoredMedia, error) {
	r, _, err := m.current(ctx)
	if err != nil {
		return core.StoredMedia{}, err
	}
	if r.Driver == "gateway" {
		return m.StoreReference(identity.WithResourceOwners(ctx, identity.ProviderOwnerID(ctx), identity.SharedOwnerID), id, data, kind)
	}
	if r.Driver == "s3" {
		return core.StoredMedia{}, ErrDirectUploadRequired
	}
	if r.Driver != "local" {
		return core.StoredMedia{}, errors.New("invalid saved storage configuration")
	}
	media, err := m.local.StoreReference(ctx, id, data, kind)
	if err == nil {
		err = m.record(ctx, media, "")
	}
	return media, err
}

func (m *Manager) DeleteReference(ctx context.Context, key string) error {
	if !strings.HasPrefix(key, "s3:") {
		if err := m.local.DeleteReference(ctx, key); err != nil {
			return err
		}
		return m.removed(ctx, key)
	}
	id, objectKey, ok := strings.Cut(strings.TrimPrefix(key, "s3:"), "/")
	if !ok || !validObjectKey(objectKey) {
		return ErrObjectNotFound
	}
	if _, err := uuid.Parse(id); err != nil {
		return ErrObjectNotFound
	}
	r, err := m.repo.Connection(ctx, id)
	if err != nil {
		return err
	}
	c, err := m.decode(r)
	if err != nil {
		return err
	}
	if !ownedReferenceKey(objectKey, c.Prefix, identity.OwnerID(ctx)) {
		return ErrObjectNotFound
	}
	s, err := NewS3(c, m.client, m.maxBytes)
	if err != nil {
		return err
	}
	if err := s.deleteReferenceObject(ctx, objectKey); err != nil {
		return err
	}
	return m.removed(ctx, key)
}

// A name-only DELETE hides a versioned object. Remove its data versions before
// removing hide markers, so partial failure never exposes an older image again.
func (s *S3) deleteReferenceObject(ctx context.Context, key string) error {
	deleted, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.config.Bucket, Key: &key})
	if err != nil {
		return safeS3Error("delete reference", err)
	}
	if !aws.ToBool(deleted.DeleteMarker) {
		return nil
	}
	pages := s3.NewListObjectVersionsPaginator(s.client, &s3.ListObjectVersionsInput{Bucket: &s.config.Bucket, Prefix: &key})
	var versions, markers []string
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return safeS3Error("list reference versions (ListBucketVersions permission required)", err)
		}
		for _, version := range page.Versions {
			if aws.ToString(version.Key) == key {
				versions = append(versions, aws.ToString(version.VersionId))
			}
		}
		for _, marker := range page.DeleteMarkers {
			if aws.ToString(marker.Key) == key {
				markers = append(markers, aws.ToString(marker.VersionId))
			}
		}
	}
	for _, version := range append(versions, markers...) {
		if version == "" {
			return errors.New("storage returned a reference version without an ID")
		}
		_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.config.Bucket, Key: &key, VersionId: &version})
		if err != nil {
			var missing smithy.APIError
			if errors.As(err, &missing) && (missing.ErrorCode() == "NoSuchVersion" || missing.ErrorCode() == "NoSuchKey" || missing.ErrorCode() == "NotFound") {
				continue
			}
			return safeS3Error("delete reference version (DeleteObjectVersion permission required)", err)
		}
	}
	return nil
}

// Keep legacy reference paths readable and deletable after the layout change.
func ownedReferenceKey(key, prefix, owner string) bool {
	return strings.HasPrefix(key, path.Join(prefix, owner, "references")+"/") || strings.HasPrefix(key, path.Join(prefix, "references", owner)+"/")
}
