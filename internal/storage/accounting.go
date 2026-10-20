package storage

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/google/uuid"
	"os"
	"path"
	"strings"
	"time"
)

type Usage struct {
	UsedBytes      int64  `json:"used_bytes"`
	UnknownObjects int64  `json:"unknown_objects"`
	LimitBytes     *int64 `json:"limit_bytes"`
	Applies        bool   `json:"applies"`
	Exceeded       bool   `json:"exceeded"`
}
type PendingObject struct{ Owner, Key string }
type ObjectAccounting interface {
	RecordObject(context.Context, string, string, int64) error
	RemoveObject(context.Context, string) error
	SharedUsage(context.Context) (Usage, error)
	PendingObjects(context.Context) ([]PendingObject, error)
	ResolveObject(context.Context, string, *int64) error
}

// ResultObjectAccounting indexes private result artifacts for owner-authorized
// deletion without including their bytes in the shared storage quota.
type ResultObjectAccounting interface {
	RecordResultObject(context.Context, string, string, int64) error
}

func (m *Manager) SharedUsage(ctx context.Context) (Usage, error) {
	if r, ok := m.repo.(ObjectAccounting); ok {
		u, e := r.SharedUsage(ctx)
		u.Applies = identity.StorageOwnerID(ctx) == identity.SharedOwnerID
		u.Exceeded = u.Applies && u.LimitBytes != nil && u.UsedBytes > *u.LimitBytes
		return u, e
	}
	return Usage{}, nil
}
func (m *Manager) record(ctx context.Context, media core.StoredMedia, taskID string) error {
	if identity.StorageOwnerID(ctx) != identity.SharedOwnerID {
		if r, ok := m.repo.(ResultObjectAccounting); ok && taskID != "" {
			return r.RecordResultObject(ctx, media.StorageKey, taskID, media.SizeBytes)
		}
		return nil
	}
	if r, ok := m.repo.(ObjectAccounting); ok {
		return r.RecordObject(ctx, media.StorageKey, taskID, media.SizeBytes)
	}
	return nil
}
func (m *Manager) removed(ctx context.Context, key string) error {
	if r, ok := m.repo.(ObjectAccounting); ok {
		return r.RemoveObject(ctx, key)
	}
	return nil
}

// DeleteResult accepts only a task's own stored paths. The caller must authorize the task.
func (m *Manager) DeleteResult(ctx context.Context, taskID, key string) error {
	if _, e := uuid.Parse(taskID); e != nil {
		return ErrObjectNotFound
	}
	if !strings.HasPrefix(key, "s3:") {
		if !validObjectKey(key) || !(strings.HasPrefix(key, taskID+"/") || strings.HasPrefix(key, path.Join(identity.OwnerID(ctx), "results", taskID)+"/")) {
			return ErrObjectNotFound
		}
		file, e := m.local.resolve(key)
		if e != nil {
			return e
		}
		if e = os.Remove(file); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		_ = os.Remove(path.Dir(file))
		return m.removed(ctx, key)
	}
	id, objectKey, ok := strings.Cut(strings.TrimPrefix(key, "s3:"), "/")
	if !ok || !validObjectKey(objectKey) {
		return ErrObjectNotFound
	}
	r, e := m.repo.Connection(ctx, id)
	if e != nil {
		return e
	}
	c, e := m.decode(r)
	if e != nil {
		return e
	}
	owner := identity.OwnerID(ctx)
	if !strings.HasPrefix(objectKey, path.Join(c.Prefix, owner, "results", taskID)+"/") && !strings.HasPrefix(objectKey, path.Join(c.Prefix, owner, taskID)+"/") {
		return ErrObjectNotFound
	}
	s, e := NewS3(c, m.client, m.maxBytes)
	if e != nil {
		return e
	}
	if e = s.deleteReferenceObject(ctx, objectKey); e != nil {
		return e
	}
	return m.removed(ctx, key)
}

// Only legacy objects with unknown size are inspected; normal writes record exact bytes.
func (m *Manager) ReconcileUsage(ctx context.Context) error {
	r, ok := m.repo.(ObjectAccounting)
	if !ok {
		return nil
	}
	items, e := r.PendingObjects(ctx)
	if e != nil {
		return e
	}
	var failures []error
	for _, item := range items {
		c, cancel := context.WithTimeout(identity.WithOwnerID(ctx, item.Owner), 30*time.Second)
		obj, err := m.Open(c, item.Key)
		if err == nil {
			obj.Reader.Close()
			err = r.ResolveObject(c, item.Key, &obj.SizeBytes)
		} else if errors.Is(err, ErrObjectNotFound) {
			err = r.ResolveObject(c, item.Key, nil)
		}
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("resolve stored object size: %w", err))
		}
	}
	return errors.Join(failures...)
}
