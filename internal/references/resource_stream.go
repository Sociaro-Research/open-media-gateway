package references

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"os"
	"strings"
	"sync"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/google/uuid"
)

// ErrResourceSource is intentionally fixed: storage/provider errors can contain
// private keys, paths or signed URLs and must not escape through an upload error.
var ErrResourceSource = errors.New("verified resource source is unavailable")

var _ core.ResourceSourceOpener = (*Resolver)(nil)

// OpenResourceSource opens only the accepted local reference ID. URL is ignored:
// neither it nor a caller-selected host can choose bytes or receive credentials.
// The original is fully staged and verified before any reader reaches a provider.
func (r *Resolver) OpenResourceSource(ctx context.Context, source core.ResourceSourceInput) (core.ArtifactSource, error) {
	empty := core.ArtifactSource{}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := CheckRead(ctx); err != nil {
		return empty, err
	}
	id, err := uuid.Parse(source.ReferenceID)
	if err != nil || r == nil || r.library == nil || source.SizeBytes <= 0 || source.SizeBytes > MaxBytes {
		return empty, ErrResourceSource
	}
	expected, err := hex.DecodeString(source.SHA256)
	if err != nil || len(expected) != sha256.Size {
		return empty, ErrResourceSource
	}
	contentType, ok := resourceSourceMIME(source.ContentType)
	if !ok || strings.SplitN(contentType, "/", 2)[0] != source.MediaType {
		return empty, ErrResourceSource
	}
	item, object, openErr := r.library.Open(ctx, source.ReferenceID)
	var closeOriginal sync.Once
	closeSource := func() {
		closeOriginal.Do(func() {
			if object.Reader != nil {
				_ = object.Reader.Close()
			}
		})
	}
	defer closeSource()
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if openErr != nil || object.Reader == nil {
		return empty, ErrResourceSource
	}
	actualID, idErr := uuid.Parse(item.ID)
	recordHash, hashErr := hex.DecodeString(item.SHA256)
	recordMIME, recordOK := resourceSourceMIME(item.ContentType)
	objectMIME, objectOK := resourceSourceMIME(object.ContentType)
	if idErr != nil || actualID != id || hashErr != nil || len(recordHash) != sha256.Size || subtle.ConstantTimeCompare(recordHash, expected) != 1 || item.SizeBytes != source.SizeBytes || object.SizeBytes != source.SizeBytes || !recordOK || !objectOK || recordMIME != contentType || objectMIME != contentType {
		return empty, ErrResourceSource
	}
	stopSourceCancellation := context.AfterFunc(ctx, closeSource)
	defer stopSourceCancellation()
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	staged, err := os.CreateTemp("", "gateway-resource-source-*")
	if err != nil {
		return empty, ErrResourceSource
	}
	stream := &resourceStagedSource{ctx: ctx, file: staged, path: staged.Name()}
	// Cancellation owns the stage from creation, including while byte/MIME
	// verification is running; providers still receive it only after success.
	stream.mu.Lock()
	stream.stop = context.AfterFunc(ctx, func() { _ = stream.Close() })
	stream.mu.Unlock()
	keep := false
	defer func() {
		if !keep {
			_ = stream.Close()
		}
	}()
	hash := sha256.New()
	bounded := io.LimitReader(resourceContextReader{ctx: ctx, reader: object.Reader}, source.SizeBytes+1)
	copied, copyErr := io.CopyBuffer(io.MultiWriter(staged, hash), bounded, make([]byte, 32<<10))
	closeSource()
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if copyErr != nil || copied != source.SizeBytes || subtle.ConstantTimeCompare(hash.Sum(nil), expected) != 1 {
		return empty, ErrResourceSource
	}
	// Reuse the existing byte classifier, including AAC/M4A container handling,
	// rather than introducing a weaker parallel MIME-sniffing implementation.
	// Both disk staging and this inspection read remain under the existing cap.
	data, readErr := io.ReadAll(io.NewSectionReader(staged, 0, source.SizeBytes))
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if readErr != nil || int64(len(data)) != source.SizeBytes {
		return empty, ErrResourceSource
	}
	detected, inspectErr := InspectMedia(data)
	canonical, detectedOK := resourceSourceMIME(detected)
	if inspectErr != nil || !detectedOK || canonical != contentType {
		return empty, ErrResourceSource
	}
	if _, err = staged.Seek(0, io.SeekStart); err != nil {
		return empty, ErrResourceSource
	}
	if err = ctx.Err(); err != nil {
		_ = stream.Close()
		return empty, err
	}
	keep = true
	return core.ArtifactSource{Reader: stream, ContentType: canonical, SizeBytes: copied}, nil
}

func resourceSourceMIME(raw string) (string, bool) {
	kind, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return "", false
	}
	kind = mediafile.CanonicalContentType(kind)
	switch kind {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "video/mp4", "video/quicktime", "video/webm", "audio/mpeg", "audio/wave", "audio/ogg", "audio/flac", "audio/aac", "audio/mp4":
		return kind, true
	}
	return "", false
}

type resourceContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r resourceContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type resourceStagedSource struct {
	ctx      context.Context
	file     *os.File
	path     string
	mu       sync.Mutex
	stop     func() bool
	closed   bool
	closeErr error
}

func (s *resourceStagedSource) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := s.file.Read(p)
	if err != nil && err != io.EOF {
		return n, ErrResourceSource
	}
	return n, err
}
func (s *resourceStagedSource) Close() error {
	s.mu.Lock()
	if s.closed {
		err := s.closeErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	closeErr := s.file.Close()
	removeErr := os.Remove(s.path)
	if closeErr != nil || (removeErr != nil && !errors.Is(removeErr, os.ErrNotExist)) {
		s.closeErr = ErrResourceSource
	}
	stop, err := s.stop, s.closeErr
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
	return err
}
