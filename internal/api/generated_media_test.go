package api

import (
	"bytes"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
	"io"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeneratedTTSSpeechTaskUsesExplicitArchivedAudioArtifact(t *testing.T) {
	f := newResourceAPIFixture(t)
	ctx := f.context("alice")
	id := uuid.NewString()
	data := resourceAPIWAV(6)
	archive := f.s.storage.(storage.SourceArchiver)
	stored, err := archive.ArchiveSource(ctx, id, core.ArtifactSource{Reader: io.NopCloser(bytes.NewReader(data)), ContentType: "audio/wav", SizeBytes: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	handle := "private-tts-task"
	task := core.Task{ID: id, OwnerID: identity.OwnerID(ctx), ProviderOwnerID: identity.ProviderOwnerID(ctx), StorageOwnerID: identity.StorageOwnerID(ctx), Provider: "kling", ProviderIdentity: resourceAPIRevision, Model: "kling-tts", MediaType: "speech", Status: core.TaskSucceeded, ProviderTaskID: &handle, CreatedAt: time.Now().UTC(), HasStoredResult: true}
	task.StoredArtifacts = []core.StoredArtifact{{ID: "audio_1", ProviderID: "private-audio-one", MediaType: "audio", StorageKey: stored.StorageKey, ContentType: stored.ContentType, SizeBytes: stored.SizeBytes, SHA256: stored.SHA256}, {ID: "audio_2", ProviderID: "private-audio-two", MediaType: "audio", StorageKey: stored.StorageKey, ContentType: stored.ContentType, SizeBytes: stored.SizeBytes, SHA256: stored.SHA256}}
	f.tasks.tasks[id] = task
	model := core.MediaModel{ID: "kling-bind-generated-audio", ResourceOperation: &core.ResourceOperationSchema{Kind: "file_resource", Subtypes: []string{"audio"}, Variants: []core.ResourceVariantSchema{{Subtype: "audio", GeneratedSources: []core.GeneratedResourceRule{{Model: "kling-tts", MediaType: "audio", RequiresInspectedOutput: true, MinDuration: 2, MaxDuration: 300, MaxAgeSeconds: 2592000}}}}}}
	intent := &core.ResourceIntent{Kind: "file_resource", Subtype: "audio", Name: "Owned TTS", GeneratedTaskID: id, GeneratedArtifactID: "audio_2"}
	r := httptest.NewRequest("POST", "/v1/provider-resources", nil).WithContext(ctx)
	result, err := f.s.resolveResourceCreation(r, intent, "kling", model)
	if err != nil {
		t.Fatal("registered speech tasks must expose their actual audio artifact to the trusted host", err)
	}
	if result.GeneratedMedia == nil || result.GeneratedMedia.ArtifactID != "audio_2" || result.GeneratedMedia.OutputID != "private-audio-two" || result.GeneratedMedia.MediaType != "audio" || result.GeneratedMedia.Inspection.Duration != 6 {
		t.Fatal("selected artifact or inspected provenance lost", result)
	}
	for _, artifact := range []string{"", "missing", "private-audio-two"} {
		intent.GeneratedArtifactID = artifact
		if _, err = f.s.resolveResourceCreation(r, intent, "kling", model); err == nil {
			t.Fatal("ambiguous/missing/native artifact selector accepted", artifact)
		}
	}
	intent.GeneratedArtifactID = "audio_2"
	for _, change := range []func(*core.Task){func(x *core.Task) { x.OwnerID = uuid.NewString() }, func(x *core.Task) { x.ProviderIdentity = "rotated" }, func(x *core.Task) { x.Model = "unreviewed-tts" }, func(x *core.Task) { x.CreatedAt = time.Now().Add(-31 * 24 * time.Hour) }, func(x *core.Task) { x.Status = core.TaskRunning }} {
		copy := task
		change(&copy)
		f.tasks.tasks[id] = copy
		if _, err = f.s.resolveResourceCreation(r, intent, "kling", model); err == nil {
			t.Fatal("changed source accepted")
		}
	}
}
