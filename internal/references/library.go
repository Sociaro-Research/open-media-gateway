// Package references manages each owner's reusable media library.
package references

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

const MaxBytes int64 = 200 << 20

var ErrInvalidImage = errors.New("use PNG, JPEG, WebP or GIF (30 MB), MP4, MOV or WebM (200 MB), or MP3, WAV, OGG or FLAC (50 MB)")
var ErrDuplicate = errors.New("reference content already exists")
var ErrInUse = errors.New("reference is retained by a template or is being prepared by a provider; remove retained usage before deleting the original")
var ErrUploadBusy = errors.New("another reference upload is in progress; retry shortly")

type Image struct {
	StorageOwnerID string    `json:"-"`
	Deleting       bool      `json:"deleting,omitempty"`
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	ContentType    string    `json:"content_type"`
	SizeBytes      int64     `json:"size_bytes"`
	CreatedAt      time.Time `json:"created_at"`
	StorageKey     string    `json:"-"`
	SHA256         string    `json:"sha256,omitempty"`
	Duplicate      bool      `json:"duplicate,omitempty"`
}

// Repository implementations must scope every operation to identity.OwnerID(ctx).
type Repository interface {
	List(context.Context, int, int) ([]Image, error)
	Get(context.Context, string) (Image, error)
	Insert(context.Context, Image) error
	Delete(context.Context, string) error
	FindByHash(context.Context, string) (Image, error)
	UnhashedBySize(context.Context, int64) ([]Image, error)
	SetHash(context.Context, string, string) error
}

type Store interface {
	StoreReference(context.Context, string, []byte, string) (core.StoredMedia, error)
	DeleteReference(context.Context, string) error
	Open(context.Context, string) (storage.Object, error)
}

type Library struct {
	repo    Repository
	store   Store
	uploads chan struct{}
	probes  chan struct{}
	// Bound multipart parsing and disk spooling before the HTTP body is read.
	multipart chan struct{}
}

func New(repo Repository, store Store) *Library {
	return &Library{repo: repo, store: store, uploads: make(chan struct{}, 1), probes: make(chan struct{}, 1), multipart: make(chan struct{}, 1)}
}

// WithStore creates an independent upload admission queue over the same scoped repository.
func (l *Library) WithStore(store Store) *Library { return New(l.repo, store) }

func (l *Library) AdmitMultipart() (func(), error) {
	select {
	case l.multipart <- struct{}{}:
		return func() { <-l.multipart }, nil
	default:
		return nil, ErrUploadBusy
	}
}
func (l *Library) List(ctx context.Context, limit, offset int) ([]Image, error) {
	return l.repo.List(ctx, limit, offset)
}
func (l *Library) Get(ctx context.Context, id string) (Image, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Image{}, storage.ErrObjectNotFound
	}
	return l.repo.Get(ctx, id)
}
func (l *Library) Upload(ctx context.Context, name string, reader io.Reader) (Image, error) {
	return l.UploadWithValidation(ctx, name, reader, nil, nil)
}
func (l *Library) UploadWithValidation(ctx context.Context, name string, reader io.Reader, target *referencevalidation.Target, validate referencevalidation.Validator) (Image, error) {
	if direct, err := l.RequiresDirectUpload(ctx); err != nil {
		return Image{}, err
	} else if direct {
		return Image{}, ErrDirectUploadRequired
	}
	select {
	case l.uploads <- struct{}{}:
		defer func() { <-l.uploads }()
	case <-ctx.Done():
		return Image{}, ctx.Err()
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxBytes+1))
	if err != nil {
		return Image{}, err
	}
	file, err := InspectFile(name, data, "")
	if err != nil {
		return Image{}, err
	}
	if err = validateTarget(ctx, target, []referencevalidation.File{file}, validate); err != nil {
		return Image{}, err
	}
	kind := file.Info.ContentType
	name = cleanName(name)

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if existing, err := l.findDuplicate(ctx, digest, int64(len(data))); err == nil {
		existing.Duplicate = true
		return existing, nil
	} else if !errors.Is(err, storage.ErrObjectNotFound) {
		return Image{}, err
	}
	item := Image{ID: uuid.NewString(), Name: name, ContentType: kind, SHA256: digest, CreatedAt: time.Now().UTC()}
	media, err := l.store.StoreReference(ctx, item.ID, data, kind)
	if err != nil {
		return Image{}, err
	}
	item.StorageKey, item.SizeBytes = media.StorageKey, media.SizeBytes
	if err := l.repo.Insert(ctx, item); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		cleanupErr := l.store.DeleteReference(cleanup, item.StorageKey)
		if errors.Is(err, ErrDuplicate) && cleanupErr == nil {
			existing, lookupErr := l.repo.FindByHash(ctx, digest)
			if errors.Is(lookupErr, storage.ErrObjectNotFound) {
				return Image{}, ErrDuplicate
			}
			existing.Duplicate = true
			return existing, lookupErr
		}
		return Image{}, errors.Join(err, cleanupErr)
	}
	return item, nil
}

func (l *Library) findDuplicate(ctx context.Context, digest string, size int64) (Image, error) {
	item, err := l.repo.FindByHash(ctx, digest)
	if !errors.Is(err, storage.ErrObjectNotFound) {
		return item, err
	}
	// Existing libraries are indexed lazily; size only narrows the candidates.
	legacy, err := l.repo.UnhashedBySize(ctx, size)
	if err != nil {
		return Image{}, err
	}
	for _, candidate := range legacy {
		// Do not download legacy S3 files just to populate a deduplication index.
		if strings.HasPrefix(candidate.StorageKey, "s3:") {
			continue
		}
		object, err := l.store.Open(StorageContext(ctx, candidate), candidate.StorageKey)
		if errors.Is(err, storage.ErrObjectNotFound) {
			continue
		}
		if err != nil {
			return Image{}, err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, io.LimitReader(object.Reader, MaxBytes+1))
		object.Reader.Close()
		if err != nil {
			return Image{}, err
		}
		candidate.SHA256 = hex.EncodeToString(hash.Sum(nil))
		if err = l.repo.SetHash(ctx, candidate.ID, candidate.SHA256); err != nil && !errors.Is(err, ErrDuplicate) {
			return Image{}, err
		}
		if candidate.SHA256 == digest {
			return candidate, nil
		}
	}
	return Image{}, storage.ErrObjectNotFound
}
func (l *Library) Open(ctx context.Context, id string) (Image, storage.Object, error) {
	item, err := l.Get(ctx, id)
	if err != nil {
		return item, storage.Object{}, err
	}
	object, err := l.store.Open(StorageContext(ctx, item), item.StorageKey)
	return item, object, err
}
func (l *Library) Delete(ctx context.Context, id string) error {
	item, err := l.Get(ctx, id)
	if repo, ok := l.repo.(interface {
		GetForDelete(context.Context, string) (Image, error)
	}); ok {
		item, err = repo.GetForDelete(ctx, id)
	}
	if err != nil {
		return err
	}
	// Keep the record retryable if physical deletion fails. Missing objects are
	// successful deletions, including after a previous database failure.
	remove := func() error {
		deleteContext := StorageContext(ctx, item)
		if err := l.store.DeleteReference(deleteContext, item.StorageKey); err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
			return err
		}
		return nil
	}
	if guarded, ok := l.repo.(interface {
		DeleteWithGuard(context.Context, string, func() error) error
	}); ok {
		return guarded.DeleteWithGuard(ctx, id, remove)
	}
	if err := remove(); err != nil {
		return err
	}
	return l.repo.Delete(ctx, id)
}

// InspectMedia validates the bytes, independently of filename or supplied MIME.
func InspectMedia(data []byte) (string, error) {
	if len(data) == 0 || int64(len(data)) > MaxBytes {
		return "", ErrInvalidImage
	}
	kind := mediafile.CanonicalContentType(http.DetectContentType(data))
	// ISO-base-media files commonly advertise isom/iso2 rather than an mp4
	// major brand. Bound the ftyp box before accepting these standard brands.
	if len(data) >= 16 && string(data[4:8]) == "ftyp" {
		size := int64(binary.BigEndian.Uint32(data[:4]))
		if size >= 16 && size <= int64(len(data)) && size%4 == 0 {
			switch string(data[8:12]) {
			case "isom", "iso2", "iso4", "iso5", "iso6", "avc1", "hvc1", "hev1", "M4V ":
				kind = "video/mp4"
			}
		}
	}

	if len(data) >= 12 && string(data[4:8]) == "ftyp" && string(data[8:12]) == "qt  " {
		kind = "video/quicktime"
	}
	if len(data) >= 4 && string(data[:4]) == "fLaC" {
		kind = "audio/flac"
	}
	if kind == "application/ogg" {
		kind = "audio/ogg"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1]&0xe0 == 0xe0 && data[1]&0x06 != 0 && data[2]&0xf0 != 0xf0 {
		kind = "audio/mpeg"
	}
	switch kind {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		if len(data) > 30<<20 {
			return kind, ErrInvalidImage
		}
	case "video/mp4", "video/quicktime", "video/webm":
	case "audio/mpeg", "audio/wave", "audio/wav", "audio/x-wav", "audio/ogg", "audio/flac":
		if len(data) > 50<<20 {
			return kind, ErrInvalidImage
		}
	default:
		return kind, ErrInvalidImage
	}

	return kind, nil
}

// TemplateUse describes a retained binding without exposing storage metadata.
type TemplateUse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}
type InUseError struct {
	Templates []TemplateUse `json:"templates"`
}

func (e *InUseError) Error() string {
	return "This file is used by templates. Remove every draft and published use before deleting it."
}
func (e *InUseError) Unwrap() error { return ErrInUse }

// Rename edits display metadata only; replacing bytes always creates a new ID.
func (l *Library) Rename(ctx context.Context, id, name string) error {
	if _, err := l.Get(ctx, id); err != nil {
		return err
	}
	if name == "" || name != strings.TrimSpace(name) || len([]rune(name)) > 255 || strings.ContainsAny(name, "/\\\n\r") {
		return referencevalidation.Invalid("Filename", "Use 1–255 characters without path separators or newlines.")
	}
	editor, ok := l.repo.(interface {
		Rename(context.Context, string, string) error
	})
	if !ok {
		return storage.ErrInvalidConfig
	}
	return editor.Rename(ctx, id, name)
}

// StorageContext uses trusted repository metadata only after namespace/role
// authorization. Canonical metadata and retained physical bytes may have
// different owners after a non-destructive template-library upgrade.
func StorageContext(ctx context.Context, item Image) context.Context {
	if item.StorageOwnerID == "" {
		return ctx
	}
	if identity.OwnerID(ctx) != item.StorageOwnerID {
		ctx = identity.WithOrganization(ctx, "", false)
	}
	return identity.WithDataOwnerID(ctx, item.StorageOwnerID)
}
