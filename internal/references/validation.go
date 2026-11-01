package references

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

type invalidMediaError struct{ error }

func (e invalidMediaError) Unwrap() []error { return []error{ErrInvalidImage, e.error} }

func InspectFile(name string, data []byte, declared string) (referencevalidation.File, error) {
	bad := func(message string) (referencevalidation.File, error) {
		return referencevalidation.File{}, invalidMediaError{referencevalidation.Invalid(cleanName(name), message)}
	}
	if len(data) == 0 || int64(len(data)) > HardMaxBytes {
		return bad(fmt.Sprintf("size %d bytes; upload a nonempty file no larger than %d bytes.", len(data), HardMaxBytes))
	}
	kind, err := InspectMedia(data)
	if err != nil {
		if limit := commonLimit(kind); limit > 0 && int64(len(data)) > limit {
			return bad(fmt.Sprintf("size %d bytes; detected %s permits at most %d bytes. Reduce the file size.", len(data), kind, limit))
		}
		return bad("Detected format " + kind + " is unsupported. Use PNG/JPEG/WebP/GIF, HEIC/HEIF with direct HEVC still images or one-level HEVC grids, MP4/MOV/WebM or MP3/WAV/AAC/M4A/OGG/FLAC.")
	}
	if declared != "" && mediafile.CanonicalType(declared) != mediafile.CanonicalType(kind) {
		return bad(fmt.Sprintf("declared format %s does not match detected %s. Export a supported file and select it again.", declared, kind))
	}
	info, err := mediafile.Inspect(data, kind)
	if err != nil {
		if kind == "image/heic" || kind == "image/heif" {
			return bad("HEIC/HEIF metadata is invalid or uses an unsupported construction. Use a direct HEVC still image or one-level HEVC grid without auxiliary/alpha items, sequences, encrypted/external data or other derived images; alternatively export PNG/JPEG.")
		}
		return bad("Media metadata could not be read. Re-export a valid file with dimensions and, for video/audio, a positive duration; video also needs readable frame timing.")
	}
	return referencevalidation.File{Type: strings.SplitN(kind, "/", 2)[0], Name: cleanName(name), Info: info}, nil
}

func (l *Library) inspectItem(ctx context.Context, item Image) (referencevalidation.File, error) {
	select {
	case l.probes <- struct{}{}:
		defer func() { <-l.probes }()
	case <-ctx.Done():
		return referencevalidation.File{}, ctx.Err()
	}
	if item.SizeBytes <= 0 || item.SizeBytes > HardMaxBytes {
		return referencevalidation.File{}, referencevalidation.Invalid(item.Name, "Stored file size is invalid. Select a valid file again.")
	}
	object, err := l.store.Open(StorageContext(ctx, item), item.StorageKey)
	if err != nil {
		return referencevalidation.File{}, err
	}
	defer object.Reader.Close()
	if object.SizeBytes != item.SizeBytes {
		return referencevalidation.File{}, storage.ErrObjectNotFound
	}
	data, err := io.ReadAll(io.LimitReader(object.Reader, item.SizeBytes+1))
	if err != nil {
		return referencevalidation.File{}, err
	}
	if int64(len(data)) != item.SizeBytes {
		return referencevalidation.File{}, storage.ErrObjectNotFound
	}
	sum := sha256.Sum256(data)
	if item.SHA256 != "" && hex.EncodeToString(sum[:]) != item.SHA256 {
		return referencevalidation.File{}, referencevalidation.Invalid(item.Name, "Stored file checksum changed. Select a valid file again.")
	}
	file, err := InspectFile(item.Name, data, item.ContentType)
	if err != nil {
		return referencevalidation.File{}, err
	}
	file.SHA256 = hex.EncodeToString(sum[:])
	return file, nil
}

// Inspect uses the repository's existing owner and content-author restrictions.
// It never follows client URLs or changes saved reference metadata.
func (l *Library) Inspect(ctx context.Context, id string) (referencevalidation.File, error) {
	if err := CheckRead(ctx); err != nil {
		return referencevalidation.File{}, err
	}
	item, err := l.Get(ctx, id)
	if err != nil {
		return referencevalidation.File{}, err
	}
	return l.inspectItem(ctx, item)
}

var ErrReadForbidden = errors.New("reference content is unavailable to this role")

func CheckRead(ctx context.Context) error {
	role := identity.OrganizationFromContext(ctx).Role
	if role != "" && !organization.OwnContent(role) {
		return ErrReadForbidden
	}
	return nil
}
func validateTarget(ctx context.Context, target *referencevalidation.Target, files []referencevalidation.File, validate referencevalidation.Validator) error {
	if target == nil {
		return nil
	}
	if validate == nil {
		return referencevalidation.Invalid("References", "Model validation is unavailable. Retry the upload.")
	}
	return validate(ctx, target, files, false)
}
