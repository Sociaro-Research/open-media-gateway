package core

import (
	"errors"
	"github.com/google/uuid"
	"math"
	"strings"
	"testing"
	"time"
)

func generatedMediaFixture() (MediaModel, MediaOptions) {
	model := MediaModel{ID: "bind-generated-audio", ResourceOperation: &ResourceOperationSchema{Kind: "file_resource", Subtypes: []string{"audio"}, Variants: []ResourceVariantSchema{{Subtype: "audio", GeneratedSources: []GeneratedResourceRule{{Model: "reviewed-tts", MediaType: "audio", RequiresInspectedOutput: true, MinDuration: 2, MaxDuration: 300, MaxAgeSeconds: 30 * 86400}}}}}}
	task := uuid.NewString()
	duration := 6.0
	created := time.Now().UTC()
	audio, video := 1, 0
	hash := strings.Repeat("a", 64)
	source := &GeneratedResourceSource{TaskID: task, ArtifactID: "1", OutputID: "900719925474099312345", Model: "reviewed-tts", Operation: "reviewed-tts", MediaType: "audio", SHA256: hash, CreatedAt: created, ExpiresAt: created.Add(30 * 24 * time.Hour), Duration: &duration, Inspection: &InputMediaEvidence{ReferenceID: task, Type: "audio", ContentType: "audio/wave", SizeBytes: 96044, SHA256: hash, Duration: duration, AudioStreams: &audio, VideoStreams: &video}}
	return model, MediaOptions{ResourceIntent: &ResourceIntent{Kind: "file_resource", Subtype: "audio", Name: "owned audio", GeneratedTaskID: task}, ResourceCreation: &ResourceCreateRequest{Kind: "file_resource", Subtype: "audio", Name: "owned audio", GeneratedMedia: source}}
}
func TestGeneratedMediaTypedIntentAndPrivateProof(t *testing.T) {
	model, original := generatedMediaFixture()
	if err := ValidateResourceCreationIntent(model, original, false); err != nil {
		t.Fatal("single artifact selector may be omitted", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*MediaOptions)
	}{
		{"wrong task", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.TaskID = uuid.NewString() }},
		{"wrong selected artifact", func(o *MediaOptions) { o.ResourceIntent.GeneratedArtifactID = "2" }},
		{"native ID in client selector", func(o *MediaOptions) { o.ResourceIntent.GeneratedArtifactID = "https://native.invalid/audio" }},
		{"no inspected bytes", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.Inspection = nil }},
		{"wrong inspected source", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.Inspection.ReferenceID = uuid.NewString() }},
		{"wrong inspected hash", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.Inspection.SHA256 = strings.Repeat("b", 64) }},
		{"wrong inspected type", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.Inspection.Type = "video" }},
		{"wrong inspected MIME", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.Inspection.ContentType = "video/mp4" }},
		{"provider duration substituted", func(o *MediaOptions) { *o.ResourceCreation.GeneratedMedia.Duration = 20 }},
		{"unknown duration", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.Duration = nil }},
		{"nonfinite duration", func(o *MediaOptions) { *o.ResourceCreation.GeneratedMedia.Duration = math.NaN() }},
		{"wrong model", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.Model = "other-tts" }},
		{"raw native remote", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.RemoteID = "client-native" }},
		{"missing output identity", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.OutputID = "" }},
		{"missing creation time", func(o *MediaOptions) { o.ResourceCreation.GeneratedMedia.CreatedAt = time.Time{} }},
		{"unbounded expiry", func(o *MediaOptions) {
			s := o.ResourceCreation.GeneratedMedia
			s.ExpiresAt = s.CreatedAt.Add(31 * 24 * time.Hour)
		}},
		{"legacy and generic both", func(o *MediaOptions) {
			o.ResourceCreation.GeneratedVideo = CloneGeneratedResourceSource(o.ResourceCreation.GeneratedMedia)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := original
			o.ResourceIntent = CloneResourceIntent(original.ResourceIntent)
			o.ResourceCreation = CloneResourceCreation(original.ResourceCreation)
			tc.change(&o)
			if err := ValidateResourceCreationIntent(model, o, false); !errors.Is(err, ErrOptionsInvalid) {
				t.Fatal("invalid proof accepted", err)
			}
		})
	}
	model.ResourceOperation.Variants[0].GeneratedSources[0].MediaType = ""
	if err := ValidateResourceCreationIntent(model, original, false); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatal("legacy defaultvideo rule accepted audio", err)
	}
}
func TestGeneratedMediaClonesAndExpiryRemainImmutable(t *testing.T) {
	_, o := generatedMediaFixture()
	original := o.ResourceCreation.GeneratedMedia
	clone := CloneResourceCreation(o.ResourceCreation)
	*clone.GeneratedMedia.Duration = 20
	clone.GeneratedMedia.Inspection.Duration = 20
	*clone.GeneratedMedia.Inspection.AudioStreams = 9
	if *original.Duration != 6 || original.Inspection.Duration != 6 || *original.Inspection.AudioStreams != 1 {
		t.Fatal("private proof aliases original")
	}
	evidence := GeneratedResourceEvidence(original)
	cloned := CloneResourceSources([]ResourceSourceEvidence{evidence})
	cloned[0].Provenance.Model = "other"
	cloned[0].Provenance.ExpiresAt = time.Time{}
	*cloned[0].Duration = 30
	*cloned[0].Inspection.AudioStreams = 9
	if evidence.Provenance.Model != "reviewed-tts" || evidence.Provenance.ExpiresAt.IsZero() || *evidence.Duration != 6 || *evidence.Inspection.AudioStreams != 1 {
		t.Fatal("durable source proof aliases input")
	}
	if ResourceSourcesExpired([]ResourceSourceEvidence{evidence}, original.CreatedAt.Add(time.Hour)) {
		t.Fatal("fresh proof expired")
	}
	if !ResourceSourcesExpired([]ResourceSourceEvidence{evidence}, original.ExpiresAt) || !ResourceSourcesExpired([]ResourceSourceEvidence{evidence}, original.CreatedAt.Add(-time.Second)) {
		t.Fatal("expired/future proof accepted")
	}
	if ResourceSourcesExpired([]ResourceSourceEvidence{{ReferenceID: uuid.NewString()}}, time.Now()) {
		t.Fatal("legacy source expiry invented")
	}
}
