package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"math"
	"sync"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
)

// Inspection consumes the owner's already archived primary video, not a native
// URL or native metadata claim. The existing buffer inspector has this technical
// read ceiling; it does not extend any model's reference or upload limit.
const generatedVideoInspectionMaxBytes int64 = 300000000

func (s *Server) inspectGeneratedVideo(ctx context.Context, task core.Task, artifact core.StoredArtifact) (core.InputMediaEvidence, error) {
	invalid := providerresources.ErrInvalid
	var empty core.InputMediaEvidence
	if task.OwnerID != identity.OwnerID(ctx) || task.MediaType != "video" || task.Status != core.TaskSucceeded || task.ResultDeletedAt != nil || artifact.MediaType != "video" || artifact.StorageKey == "" || artifact.SizeBytes <= 0 || artifact.SizeBytes > generatedVideoInspectionMaxBytes || s.storage == nil {
		return empty, invalid
	}
	expected, err := hex.DecodeString(artifact.SHA256)
	if err != nil || len(expected) != sha256.Size {
		return empty, invalid
	}
	kind := mediafile.CanonicalContentType(artifact.ContentType)
	if kind != "video/mp4" && kind != "video/quicktime" && kind != "video/webm" {
		return empty, invalid
	}
	owner := task.StorageOwnerID
	if owner == "" {
		owner = task.OwnerID
	}
	readCtx := identity.WithResourceOwners(ctx, task.ProviderOwnerID, owner)
	object, err := s.storage.Open(readCtx, artifact.StorageKey)
	if err != nil {
		return empty, providerresources.ErrNotFound
	}
	if object.Reader == nil {
		return empty, invalid
	}
	var once sync.Once
	closeReader := func() { once.Do(func() { _ = object.Reader.Close() }) }
	stop := context.AfterFunc(readCtx, closeReader)
	defer func() { stop(); closeReader() }()
	if object.SizeBytes != artifact.SizeBytes || mediafile.CanonicalContentType(object.ContentType) != kind {
		return empty, invalid
	}
	data, err := io.ReadAll(io.LimitReader(archivedVideoContextReader{readCtx, object.Reader}, artifact.SizeBytes+1))
	if readCtx.Err() != nil {
		return empty, readCtx.Err()
	}
	if err != nil || int64(len(data)) != artifact.SizeBytes {
		return empty, invalid
	}
	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
		return empty, invalid
	}
	info, err := mediafile.Inspect(data, kind)
	if err != nil || mediafile.CanonicalContentType(info.ContentType) != kind || info.Size != artifact.SizeBytes || info.Duration <= 0 || math.IsNaN(info.Duration) || math.IsInf(info.Duration, 0) {
		return empty, invalid
	}
	evidence := core.InputMediaEvidence{ReferenceID: task.ID, Type: "video", SHA256: artifact.SHA256, ContentType: kind, SizeBytes: info.Size, Width: info.Width, Height: info.Height, FPS: info.FPS, FrameCount: info.FrameCount, Duration: info.Duration, AudioStreams: info.AudioStreams, VideoStreams: info.VideoStreams, AudioCodecs: core.CloneAudioCodecs(info.AudioCodecs)}
	return core.CloneInputMediaEvidence(map[string]core.InputMediaEvidence{"video": evidence})["video"], nil
}

type archivedVideoContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r archivedVideoContextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}
