package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
)

// The byte inspector requires a complete bounded buffer. This technical ceiling
// is not an upstream native-audio-ID file-size restriction.
const generatedMediaInspectionMaxBytes int64 = 300000000
const generatedMediaMaxAge = 30 * 24 * time.Hour

func generatedMediaRule(model core.MediaModel, subtype, sourceModel, mediaType string) (core.GeneratedResourceRule, bool) {
	if model.ResourceOperation != nil {
		for _, variant := range model.ResourceOperation.Variants {
			if variant.Subtype != subtype {
				continue
			}
			for _, rule := range variant.GeneratedSources {
				kind := rule.MediaType
				if kind == "" {
					kind = "video"
				}
				if rule.Model == sourceModel && kind == mediaType {
					return rule, true
				}
			}
		}
	}
	return core.GeneratedResourceRule{}, false
}

func (s *Server) resolveGeneratedMediaSource(r *http.Request, intent *core.ResourceIntent, provider string, model core.MediaModel, task core.Task) (*core.GeneratedResourceSource, error) {
	now := time.Now().UTC()
	scope, err := s.resourceScopes(r.Context(), provider)
	if err != nil || scope.ID != provider || scope.OwnerID != identity.ProviderOwnerID(r.Context()) || scope.Revision == "" || task.ID != intent.GeneratedTaskID || task.OwnerID != identity.OwnerID(r.Context()) || task.ProviderOwnerID != scope.OwnerID || task.Provider != provider || task.ProviderIdentity != scope.Revision || task.Status != core.TaskSucceeded || task.ResultDeletedAt != nil || core.GeneratedTaskMediaType(task.MediaType) != "audio" || task.CreatedAt.IsZero() || task.CreatedAt.After(now) {
		return nil, providerresources.ErrNotFound
	}
	rule, ok := generatedMediaRule(model, intent.Subtype, task.Model, core.GeneratedTaskMediaType(task.MediaType))
	if !ok || !rule.RequiresInspectedOutput {
		return nil, providerresources.ErrInvalid
	}
	maxAge := generatedMediaMaxAge
	if rule.MaxAgeSeconds > 0 && rule.MaxAgeSeconds < int64(maxAge/time.Second) {
		maxAge = time.Duration(rule.MaxAgeSeconds) * time.Second
	}
	expires := task.CreatedAt.Add(maxAge)
	if !now.Before(expires) {
		return nil, providerresources.ErrNotFound
	}
	var selected *core.StoredArtifact
	eligible := 0
	for i := range task.StoredArtifacts {
		artifact := &task.StoredArtifacts[i]
		if artifact.MediaType != core.GeneratedTaskMediaType(task.MediaType) || !core.ValidGeneratedArtifactID(artifact.ID) || !providerresources.ValidRemoteID(artifact.ProviderID) {
			continue
		}
		if intent.GeneratedArtifactID != "" && artifact.ID != intent.GeneratedArtifactID {
			continue
		}
		eligible++
		selected = artifact
	}
	if eligible != 1 || selected == nil {
		return nil, providerresources.ErrInvalid
	}
	evidence, err := s.inspectGeneratedMedia(r.Context(), task, *selected)
	if err != nil {
		return nil, err
	}
	duration := evidence.Duration
	source := &core.GeneratedResourceSource{TaskID: task.ID, ArtifactID: selected.ID, OutputID: selected.ProviderID, SHA256: selected.SHA256, Operation: task.Model, Model: task.Model, MediaType: core.GeneratedTaskMediaType(task.MediaType), CreatedAt: task.CreatedAt, ExpiresAt: expires, Inspection: &evidence, Duration: &duration, NativeAudio: task.Options.GenerateAudio != nil && *task.Options.GenerateAudio}
	if task.ProviderTaskID != nil {
		source.TaskHandle = *task.ProviderTaskID
	}
	if err = core.ValidateGeneratedMediaSource(model, intent.Subtype, source); err != nil {
		return nil, providerresources.ErrInvalid
	}
	return source, nil
}

func (s *Server) inspectGeneratedMedia(ctx context.Context, task core.Task, artifact core.StoredArtifact) (core.InputMediaEvidence, error) {
	empty := core.InputMediaEvidence{}
	expected, err := hex.DecodeString(artifact.SHA256)
	kind := mediafile.CanonicalContentType(artifact.ContentType)
	if s.storage == nil || artifact.StorageKey == "" || artifact.SizeBytes <= 0 || artifact.SizeBytes > generatedMediaInspectionMaxBytes || err != nil || len(expected) != sha256.Size || !strings.HasPrefix(kind, "audio/") {
		return empty, providerresources.ErrInvalid
	}
	storageOwner := task.StorageOwnerID
	if storageOwner == "" {
		storageOwner = task.OwnerID
	}
	readContext := identity.WithResourceOwners(identity.WithOwnerID(ctx, task.OwnerID), task.ProviderOwnerID, storageOwner)
	object, err := s.storage.Open(readContext, artifact.StorageKey)
	if err != nil {
		return empty, providerresources.ErrNotFound
	}
	if object.Reader == nil {
		return empty, providerresources.ErrInvalid
	}
	defer object.Reader.Close()
	stop := context.AfterFunc(ctx, func() { _ = object.Reader.Close() })
	defer stop()
	if object.SizeBytes != artifact.SizeBytes || mediafile.CanonicalContentType(object.ContentType) != kind {
		return empty, providerresources.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(generatedMediaContextReader{ctx: ctx, reader: object.Reader}, artifact.SizeBytes+1))
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	if err != nil || int64(len(data)) != artifact.SizeBytes {
		return empty, providerresources.ErrInvalid
	}
	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
		return empty, providerresources.ErrInvalid
	}
	info, err := mediafile.Inspect(data, kind)
	if err != nil || mediafile.CanonicalContentType(info.ContentType) != kind || info.Size != artifact.SizeBytes || info.Duration <= 0 || math.IsNaN(info.Duration) || math.IsInf(info.Duration, 0) {
		return empty, providerresources.ErrInvalid
	}
	return core.InputMediaEvidence{ReferenceID: task.ID, Type: "audio", ContentType: kind, SizeBytes: info.Size, SHA256: artifact.SHA256, Duration: info.Duration, FrameCount: info.FrameCount, FPS: info.FPS, Width: info.Width, Height: info.Height, AudioStreams: info.AudioStreams, VideoStreams: info.VideoStreams}, nil
}

type generatedMediaContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r generatedMediaContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
