package storage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

var ErrObjectNotFound = errors.New("stored media not found")

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type Object struct {
	Key         string
	ContentType string
	SizeBytes   int64
	ModTime     time.Time
	Reader      ReadSeekCloser
}

// Store is the storage boundary used by the worker and HTTP layer.
type Store interface {
	Archive(context.Context, string, string) (core.StoredMedia, error)
	Open(context.Context, string) (Object, error)
}

type Local struct {
	root        string
	client      *http.Client
	maxBytes    int64
	temporary   temporaryScanner
	stagingOnly bool
}

func NewLocal(root string, client *http.Client, maxBytes int64) (*Local, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("local storage path is required")
	}
	if client == nil {
		return nil, errors.New("storage HTTP client is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("storage max bytes must be positive")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve local storage path: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create local storage path: %w", err)
	}
	return &Local{root: abs, client: client, maxBytes: maxBytes}, nil
}

func (s *Local) Archive(ctx context.Context, taskID, sourceURL string) (core.StoredMedia, error) {
	reader, contentType, size, err := s.openSource(ctx, sourceURL)
	if err != nil {
		return core.StoredMedia{}, err
	}
	defer reader.Close()
	return s.archiveReader(ctx, taskID, reader, contentType, size, sourceURL)
}

func (s *Local) archiveReader(ctx context.Context, taskID string, reader io.Reader, contentType string, size int64, sourceURL string) (core.StoredMedia, error) {
	return s.archiveReaderChecked(ctx, taskID, reader, contentType, size, sourceURL, false)
}

func (s *Local) archiveReaderChecked(ctx context.Context, taskID string, reader io.Reader, contentType string, size int64, sourceURL string, trustedStream bool) (core.StoredMedia, error) {
	if err := ctx.Err(); err != nil {
		return core.StoredMedia{}, err
	}
	if size > s.maxBytes {
		return core.StoredMedia{}, fmt.Errorf("provider result exceeds STORAGE_MAX_BYTES (%d)", s.maxBytes)
	}
	buffered := bufio.NewReader(reader)
	if contentType == "" || contentType == "application/octet-stream" {
		sample, _ := buffered.Peek(512)
		detected := strings.TrimSpace(strings.Split(http.DetectContentType(sample), ";")[0])
		if supportedMediaType(detected) {
			contentType = detected
		}
	}
	if !supportedMediaType(contentType) && contentType != "application/octet-stream" && !(trustedStream && (contentType == "text/plain" || contentType == "application/json")) {
		return core.StoredMedia{}, fmt.Errorf("provider returned unsupported content type %q", contentType)
	}

	baseName, extension := mediaFileName(contentType)
	key := filepath.Join(taskID, baseName+extension)
	path, err := s.resolve(key)
	if err != nil {
		return core.StoredMedia{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return core.StoredMedia{}, fmt.Errorf("create task storage directory: %w", err)
	}
	temporary, err := s.createTemporary(filepath.Dir(path), "media")
	if err != nil {
		return core.StoredMedia{}, fmt.Errorf("create temporary media file: %w", err)
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		if !temporaryLockSupported {
			_ = temporary.Close()
		}
		if !committed {
			_ = os.Remove(temporaryName)
		}
		_ = temporary.Close()
	}()

	hash := sha256.New()
	limited := &io.LimitedReader{R: buffered, N: s.maxBytes + 1}
	written, err := io.Copy(io.MultiWriter(temporary, hash), limited)
	if err != nil {
		return core.StoredMedia{}, fmt.Errorf("copy provider result: %w", err)
	}
	if written > s.maxBytes {
		return core.StoredMedia{}, fmt.Errorf("provider result exceeds STORAGE_MAX_BYTES (%d)", s.maxBytes)
	}
	if written == 0 {
		return core.StoredMedia{}, errors.New("provider returned an empty media file")
	}
	if trustedStream {
		if size >= 0 && written != size {
			return core.StoredMedia{}, errors.New("provider artifact stream size does not match its declared size")
		}
		if err := validateArtifactFile(ctx, temporary, contentType); err != nil {
			return core.StoredMedia{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return core.StoredMedia{}, err
	}
	if err := temporary.Sync(); err != nil {
		return core.StoredMedia{}, fmt.Errorf("flush media file: %w", err)
	}
	if err := temporary.Chmod(0o640); err != nil {
		return core.StoredMedia{}, fmt.Errorf("set media file permissions: %w", err)
	}
	if !temporaryLockSupported {
		if err := temporary.Close(); err != nil {
			return core.StoredMedia{}, fmt.Errorf("close media file: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return core.StoredMedia{}, err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return core.StoredMedia{}, fmt.Errorf("commit media file: %w", err)
	}
	committed = true
	return core.StoredMedia{
		SourceURL: sourceForDiagnostics(sourceURL), StorageKey: filepath.ToSlash(key),
		ContentType: contentType, SizeBytes: written, SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func supportedMediaType(contentType string) bool {
	return strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "audio/")
}

func mediaFileName(contentType string) (string, string) {
	switch contentType {
	case "text/plain":
		return "text", ".txt"
	case "application/json":
		return "data", ".json"
	case "video/webm":
		return "video", ".webm"
	case "image/png":
		return "image", ".png"
	case "image/jpeg":
		return "image", ".jpg"
	case "image/webp":
		return "image", ".webp"
	case "audio/mpeg":
		return "audio", ".mp3"
	case "audio/wav", "audio/x-wav":
		return "audio", ".wav"
	case "audio/ogg":
		return "audio", ".ogg"
	default:
		if strings.HasPrefix(contentType, "image/") {
			return "image", extensionForType(contentType, ".img")
		}
		if strings.HasPrefix(contentType, "audio/") {
			return "audio", extensionForType(contentType, ".audio")
		}
		return "video", ".mp4"
	}
}

func extensionForType(contentType, fallback string) string {
	extensions, err := mime.ExtensionsByType(contentType)
	if err == nil && len(extensions) > 0 {
		return extensions[0]
	}
	return fallback
}

func (s *Local) Open(_ context.Context, key string) (Object, error) {
	path, err := s.resolve(key)
	if err != nil {
		return Object{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Object{}, ErrObjectNotFound
	}
	if err != nil {
		return Object{}, fmt.Errorf("open stored media: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return Object{}, fmt.Errorf("stat stored media: %w", err)
	}
	return Object{Key: key, ContentType: mime.TypeByExtension(filepath.Ext(path)), SizeBytes: info.Size(), ModTime: info.ModTime(), Reader: file}, nil
}

func (s *Local) openSource(ctx context.Context, rawURL string) (io.ReadCloser, string, int64, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", 0, fmt.Errorf("parse provider result URL: %w", err)
	}
	if parsed.Scheme == "data" {
		return openDataURL(rawURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, "", 0, fmt.Errorf("unsupported provider result URL scheme %q", parsed.Scheme)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", 0, fmt.Errorf("create provider result request: %w", err)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, "", 0, fmt.Errorf("download provider result: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, "", 0, fmt.Errorf("download provider result: unexpected HTTP status %s", response.Status)
	}
	contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	return response.Body, contentType, response.ContentLength, nil
}

func openDataURL(rawURL string) (io.ReadCloser, string, int64, error) {
	header, payload, found := strings.Cut(rawURL, ",")
	if !found || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") {
		return nil, "", 0, errors.New("mock media data URL must be base64 encoded")
	}
	contentType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", 0, fmt.Errorf("decode mock media: %w", err)
	}
	return io.NopCloser(bytes.NewReader(decoded)), contentType, int64(len(decoded)), nil
}

func (s *Local) resolve(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid storage key")
	}
	path := filepath.Join(s.root, clean)
	relative, err := filepath.Rel(s.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("storage key escapes local root")
	}
	return path, nil
}

func sourceForDiagnostics(sourceURL string) string {
	if strings.HasPrefix(sourceURL, "data:") {
		return ""
	}
	return sourceURL
}
