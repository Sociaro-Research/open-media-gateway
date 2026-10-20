package references

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/google/uuid"
)

var ErrUploadExpired = errors.New("upload expired; select the file again")
var ErrDirectUploadRequired = storage.ErrDirectUploadRequired

type UploadInput struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}
type UploadSession struct {
	Item      Image                 `json:"item"`
	Storage   storage.PendingUpload `json:"storage"`
	ExpiresAt time.Time             `json:"expires_at"`
	ResultID  string                `json:"result_id,omitempty"`
}
type ExpiredUpload struct{ Owner, ID string }
type UploadRepository interface {
	CreateUpload(context.Context, UploadSession) error
	// Serialize completion/cleanup and identical hashes across gateway processes.
	// Persist session changes and reference metadata in the same transaction.
	UseUpload(context.Context, string, bool, func(context.Context, *UploadSession, Repository) error) error
	ExpiredUploads(context.Context) ([]ExpiredUpload, error)
}
type DirectStore interface {
	PlanUpload(context.Context, string, string, string, int64) (*storage.PendingUpload, error)
	StartUpload(context.Context, *storage.PendingUpload) (storage.UploadTarget, error)
	CompleteUpload(context.Context, storage.PendingUpload) (core.StoredMedia, error)
	AbortUpload(context.Context, storage.PendingUpload, bool) error
}
type UploadPlan struct {
	ID       string                `json:"id,omitempty"`
	Mode     string                `json:"mode"`
	Target   *storage.UploadTarget `json:"target,omitempty"`
	Existing *Image                `json:"-"`
}

func cleanName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || len(name) > 255 {
		return "Reference file"
	}
	return name
}
func validUpload(input UploadInput) bool {
	sum, err := hex.DecodeString(input.SHA256)
	if err != nil || len(sum) != 32 || input.SizeBytes <= 0 {
		return false
	}
	limit := int64(0)
	switch input.ContentType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		limit = 30 << 20
	case "video/mp4", "video/quicktime", "video/webm":
		limit = 200 << 20
	case "audio/mpeg", "audio/wave", "audio/wav", "audio/x-wav", "audio/ogg", "audio/flac":
		limit = 50 << 20
	}
	return input.SizeBytes <= limit
}
func (l *Library) RequiresDirectUpload(ctx context.Context) (bool, error) {
	store, ok := l.store.(DirectStore)
	if !ok {
		return false, nil
	}
	plan, err := store.PlanUpload(ctx, uuid.NewString(), "image/png", "", 1)
	return plan != nil, err
}
func (l *Library) BeginUpload(ctx context.Context, input UploadInput) (UploadPlan, error) {
	if !validUpload(input) {
		return UploadPlan{}, ErrInvalidImage
	}
	input.SHA256 = strings.ToLower(input.SHA256)
	store, ok := l.store.(DirectStore)
	if !ok {
		return UploadPlan{Mode: "local"}, nil
	}
	id := uuid.NewString()
	pending, err := store.PlanUpload(ctx, id, input.ContentType, input.SHA256, input.SizeBytes)
	if err != nil {
		return UploadPlan{}, err
	}
	if pending == nil {
		return UploadPlan{Mode: "local"}, nil
	}
	repo, ok := l.repo.(UploadRepository)
	if !ok {
		return UploadPlan{}, errors.New("direct upload repository is unavailable")
	}
	item := Image{ID: id, Name: cleanName(input.Name), ContentType: input.ContentType, SizeBytes: input.SizeBytes, SHA256: input.SHA256, StorageKey: pending.Key, CreatedAt: time.Now().UTC()}
	session := UploadSession{Item: item, Storage: *pending, ExpiresAt: time.Now().UTC().Add(30 * time.Minute)}
	// Persist the unique destination first, so even a lost S3 initiation response
	// leaves enough information for the cleanup worker to reclaim its parts.
	if err = repo.CreateUpload(ctx, session); err != nil {
		return UploadPlan{}, err
	}
	plan := UploadPlan{ID: id, Mode: "s3"}
	err = repo.UseUpload(ctx, id, false, func(ctx context.Context, s *UploadSession, refs Repository) error {
		existing, err := refs.FindByHash(ctx, input.SHA256)
		if err == nil {
			existing.Duplicate = true
			plan.Existing = &existing
			s.ResultID = existing.ID
			return nil
		}
		if !errors.Is(err, storage.ErrObjectNotFound) {
			return err
		}
		target, err := store.StartUpload(ctx, &s.Storage)
		if err != nil {
			return err
		}
		plan.Target = &target
		return nil
	})
	return plan, err
}
func (l *Library) FinishUpload(ctx context.Context, id string) (Image, error) {
	repo, ok := l.repo.(UploadRepository)
	if !ok {
		return Image{}, storage.ErrObjectNotFound
	}
	store, ok := l.store.(DirectStore)
	if !ok {
		return Image{}, storage.ErrObjectNotFound
	}
	var result Image
	var cleanupDuplicate bool
	err := repo.UseUpload(ctx, id, false, func(ctx context.Context, s *UploadSession, refs Repository) error {
		if s.ResultID != "" {
			var err error
			result, err = refs.Get(ctx, s.ResultID)
			result.Duplicate = s.ResultID != s.Item.ID
			cleanupDuplicate = result.Duplicate && s.Storage.UploadID != ""
			return err
		}
		if time.Now().After(s.ExpiresAt) {
			return ErrUploadExpired
		}
		if s.Storage.UploadID == "" {
			return ErrUploadExpired
		}
		// Only S3-validated checksums enter the library. An unverified client hash
		// must not be used to deduplicate files before a successful upload.
		media, err := store.CompleteUpload(ctx, s.Storage)
		if err != nil {
			return err
		}
		existing, err := refs.FindByHash(ctx, s.Item.SHA256)
		if err == nil {
			existing.Duplicate = true
			result = existing
			s.ResultID = existing.ID
			cleanupDuplicate = true
			return nil
		}
		if !errors.Is(err, storage.ErrObjectNotFound) {
			return err
		}
		s.Item.StorageKey = media.StorageKey
		if err = refs.Insert(ctx, s.Item); err != nil {
			return err
		}
		s.ResultID = s.Item.ID
		result = s.Item
		return nil
	})
	if err == nil && cleanupDuplicate {
		// Keep the completed object recoverable until ResultID is durable. A
		// rollback after deleting it would leave neither an upload nor an object
		// for CompleteUpload to validate on retry.
		err = repo.UseUpload(ctx, id, false, func(ctx context.Context, s *UploadSession, _ Repository) error {
			if s.Storage.UploadID == "" {
				return nil
			}
			if err := store.AbortUpload(ctx, s.Storage, true); err != nil {
				return err
			}
			s.Storage.UploadID = ""
			return nil
		})
	}
	return result, err
}
func (l *Library) CleanupUploads(ctx context.Context) error {
	repo, ok := l.repo.(UploadRepository)
	if !ok {
		return nil
	}
	store, ok := l.store.(DirectStore)
	if !ok {
		return nil
	}
	expired, err := repo.ExpiredUploads(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range expired {
		ownerCtx, cancel := context.WithTimeout(identity.WithOwnerID(ctx, item.Owner), time.Minute)
		err = repo.UseUpload(ownerCtx, item.ID, true, func(ctx context.Context, s *UploadSession, refs Repository) error {
			if time.Now().Before(s.ExpiresAt.Add(time.Hour)) {
				return errors.New("upload still active")
			}
			saved, err := refs.Get(ctx, s.Item.ID)
			if err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
				return err
			}
			return store.AbortUpload(ctx, s.Storage, err != nil || saved.StorageKey != s.Storage.Key)
		})
		cancel()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (l *Library) DirectURL(ctx context.Context, id, method string, ttl time.Duration) (string, error) {
	item, err := l.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if store, ok := l.store.(storage.DirectReader); ok {
		return store.DirectURL(ctx, item.StorageKey, method, item.ContentType, "inline", ttl)
	}
	return "", nil
}
func (l *Library) DirectReferenceURL(ctx context.Context, id string) (string, error) {
	return l.DirectURL(ctx, id, http.MethodGet, 24*time.Hour)
}
