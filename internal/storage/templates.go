package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// StoreTemplateMedia is restricted to already validated administrator media. It
// creates a new private object; it never adopts or deletes a library pointer.
func (m *Manager) StoreTemplateMedia(ctx context.Context, id string, data []byte, kind string) (core.StoredMedia, error) {
	record, config, err := m.current(ctx)
	if err != nil {
		return core.StoredMedia{}, err
	}
	if record.Driver != "s3" {
		return m.StoreReference(ctx, id, data, kind)
	}
	dir, err := referenceDirectory(ctx, id)
	if err != nil {
		return core.StoredMedia{}, err
	}
	if len(data) == 0 || int64(len(data)) > m.maxBytes {
		return core.StoredMedia{}, ErrInvalidConfig
	}
	target, err := NewS3(config, m.client, m.maxBytes)
	if err != nil {
		return core.StoredMedia{}, err
	}
	name, ext := mediaFileName(kind)
	key := path.Join(config.Prefix, dir, name+ext)
	_, err = target.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(config.Bucket), Key: &key, Body: bytes.NewReader(data), ContentType: &kind})
	if err != nil {
		return core.StoredMedia{}, safeS3Error("store template media", err)
	}
	sum := sha256.Sum256(data)
	result := core.StoredMedia{StorageKey: "s3:" + record.ConnectionID + "/" + key, ContentType: kind, SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	return result, m.record(ctx, result, "")
}
func (s *Local) StoreTemplateMedia(ctx context.Context, id string, data []byte, kind string) (core.StoredMedia, error) {
	return s.StoreReference(ctx, id, data, kind)
}
