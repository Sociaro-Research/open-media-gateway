package kling

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

func generatedAudioRequestFixture(seconds float64) core.MediaRequest {
	task := uuid.NewString()
	created := time.Now().UTC().Add(-time.Hour)
	hash := strings.Repeat("a", 64)
	g := &core.GeneratedResourceSource{TaskID: task, ArtifactID: "1", OutputID: "9007199254740993999", Model: TextToSpeech, Operation: TextToSpeech, MediaType: "audio", SHA256: hash, Duration: &seconds, CreatedAt: created, ExpiresAt: created.Add(generatedAudioLifetime), Inspection: &core.InputMediaEvidence{ReferenceID: task, Type: "audio", SHA256: hash, ContentType: "audio/wav", SizeBytes: 20 << 20, Duration: seconds}}
	return core.MediaRequest{Model: BindGeneratedAudio, TaskID: uuid.NewString(), Options: core.MediaOptions{ResourceIntent: &core.ResourceIntent{Kind: "file_resource", Subtype: "audio", Name: "Owned TTS output", GeneratedTaskID: task, GeneratedArtifactID: "1"}, ResourceCreation: &core.ResourceCreateRequest{Kind: "file_resource", Subtype: "audio", Name: "Owned TTS output", GeneratedMedia: g}}}
}
func generatedAudioSnapshotFixture(t *testing.T, a *Adapter, ctx context.Context, seconds float64) core.ResourceSnapshot {
	t.Helper()
	r := generatedAudioRequestFixture(seconds)
	out, e := a.Generate(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	return core.ResourceSnapshot{BindingID: uuid.NewString(), ReferenceID: "audio_1", Kind: "file_resource", Subtype: "audio", RemoteID: out.Resources[0].RemoteID, ConnectionIdentity: scope.Revision, Sources: []core.ResourceSourceEvidence{core.GeneratedResourceEvidence(r.Options.ResourceCreation.GeneratedMedia)}}
}
func generatedAudioConsumerFixture(model string, b core.ResourceSnapshot) core.MediaRequest {
	if model == LipSync {
		r := auxiliaryFixture(LipSync)
		r.Options.AudioURLs = nil
		r.Options.AudioURL = ""
		r.Options.ReferenceMode = "managed_audio"
		r.Options.ResourceReferences = append(r.Options.ResourceReferences, core.ResourceReference{BindingID: b.BindingID, ReferenceID: b.ReferenceID})
		r.Options.ResourceSnapshots = append(r.Options.ResourceSnapshots, b)
		r.Options.Parameters["sound_volume"] = 0.0
		r.Options.Parameters["original_audio_volume"] = 0.0
		return r
	}
	return core.MediaRequest{Model: AvatarImageToVideo, TaskID: uuid.NewString(), Options: core.MediaOptions{ImageURLs: []string{"https://cdn.example/avatar.png"}, ReferenceMode: "image_audio", ResourceReferences: []core.ResourceReference{{BindingID: b.BindingID, ReferenceID: b.ReferenceID}}, ResourceSnapshots: []core.ResourceSnapshot{b}}}
}
func TestGeneratedAudioBindingIsFreeLocalAndScoped(t *testing.T) {
	calls := 0
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("binding must not contact provider")
	})
	ctx := auxiliaryContext()
	r := generatedAudioRequestFixture(300)
	m, ok := modelFor(BindGeneratedAudio)
	if !ok || m.ResourceOperation == nil || m.ResourceOperation.Kind != "file_resource" || m.ResourceOperation.Variants[0].GeneratedSources[0].Model != TextToSpeech || !m.ResourceOperation.Variants[0].GeneratedSources[0].RequiresInspectedOutput {
		t.Fatal(m)
	}
	q, _, e := a.Quote(ctx, m, r)
	if e != nil || q.ExpectedUSD != 0 || q.ActualUSD == nil || *q.ActualUSD != 0 || q.ActualSource != "free_provider" {
		t.Fatal("local binding not confirmed free", q, e)
	}
	result, e := a.Generate(ctx, r)
	if e != nil || result.ID != "" || len(result.Artifacts) != 0 || result.ResultURL != "" || len(result.Resources) != 1 || result.Resources[0].RemoteID == r.Options.ResourceCreation.GeneratedMedia.OutputID || result.Resources[0].IsPreset || calls != 0 {
		t.Fatal(result, e, calls)
	}
	b := generatedAudioSnapshotFixture(t, a, ctx, 300)
	restarted := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("restart proof must stay local"); return nil, nil })
	id, d, e := restarted.generatedAudioID(ctx, b, 300, false)
	if e != nil || id != "9007199254740993999" || d != 300 {
		t.Fatal(id, d, e)
	}
	for _, bad := range []context.Context{legacyScopeFixture("other-owner", "fixture-provider-owner", "kling--fixture", "fixture-revision"), legacyScopeFixture("fixture-owner", "other-provider-owner", "kling--fixture", "fixture-revision"), legacyScopeFixture("fixture-owner", "fixture-provider-owner", "other-connection", "fixture-revision"), legacyScopeFixture("fixture-owner", "fixture-provider-owner", "kling--fixture", "other-revision"), context.Background()} {
		if _, _, e := a.generatedAudioID(bad, b, 300, false); e == nil {
			t.Fatal("foreign scope decoded audio ID")
		}
	}
	if e := a.ValidateResourceDeletion(ctx, b); e != nil {
		t.Fatal(e)
	}
	deleted, e := a.DeleteResource(ctx, b)
	if e != nil || deleted.Status != core.ProviderSucceeded || deleted.Handle != "" || calls != 0 {
		t.Fatal("local revoke reached native API", deleted, e, calls)
	}
}
func TestGeneratedAudioAvatarAndLipSerializeOnlyAuthenticatedNativeID(t *testing.T) {
	for _, model := range []string{AvatarImageToVideo, LipSync} {
		t.Run(model, func(t *testing.T) {
			calls := 0
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				calls++
				var p map[string]any
				if e := json.NewDecoder(req.Body).Decode(&p); e != nil {
					t.Fatal(e)
				}
				if model == AvatarImageToVideo {
					if req.URL.Path != "/v1/videos/avatar/image2video" || p["audio_id"] != "9007199254740993999" || p["sound_file"] != nil || p["image"] != "https://cdn.example/avatar.png" {
						t.Fatal(p)
					}
				} else {
					f := p["face_choose"].([]any)[0].(map[string]any)
					if req.URL.Path != "/v1/videos/advanced-lip-sync" || f["audio_id"] != "9007199254740993999" || f["sound_file"] != nil || f["sound_volume"] != float64(0) || f["original_audio_volume"] != float64(0) || f["sound_start_time"] != float64(0) || f["sound_end_time"] != float64(3000) {
						t.Fatal(p)
					}
				}
				return response(200, `{"code":0,"data":{"task_id":"native-video-task","task_status":"submitted"}}`), nil
			})
			b := generatedAudioSnapshotFixture(t, a, auxiliaryContext(), 4)
			r := generatedAudioConsumerFixture(model, b)
			if e := a.ValidateRequest(auxiliaryContext(), r); e != nil {
				t.Fatal(e)
			}
			if _, e := a.Generate(auxiliaryContext(), r); e != nil || calls != 1 {
				t.Fatal(e, calls)
			}
			// The native-ID path deliberately has no uploaded-file 5MB restriction.
			if b.Sources[0].Inspection.SizeBytes <= 5<<20 {
				t.Fatal("fixture lacks ID-path size regression")
			}
			r.Options.AudioURLs = []string{"https://cdn.example/also-uploaded.wav"}
			if e := a.ValidateRequest(auxiliaryContext(), r); !errors.Is(e, core.ErrOptionsInvalid) {
				t.Fatal("URL plus native ID admitted", e)
			}
			if calls != 1 {
				t.Fatal("ambiguous audio dispatched")
			}
		})
	}
}
func TestGeneratedAudioRejectsUntrustedCreationBeforeHTTP(t *testing.T) {
	edits := map[string]func(*core.MediaRequest){"raw native ID": func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"audio_id": "88"} }, "arbitrarymodel": func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedMedia.Model = TextToAudio }, "no inspection": func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedMedia.Inspection = nil }, "mismatched hash": func(r *core.MediaRequest) {
		r.Options.ResourceCreation.GeneratedMedia.Inspection.SHA256 = strings.Repeat("b", 64)
	}, "untrusted provider ID": func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedMedia.RemoteID = "88" }, "wrong output": func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedMedia.ArtifactID = "2" }, "expired": func(r *core.MediaRequest) {
		g := r.Options.ResourceCreation.GeneratedMedia
		g.CreatedAt = time.Now().Add(-31 * 24 * time.Hour)
		g.ExpiresAt = g.CreatedAt.Add(generatedAudioLifetime)
	}, "future": func(r *core.MediaRequest) {
		g := r.Options.ResourceCreation.GeneratedMedia
		g.CreatedAt = time.Now().Add(time.Hour)
		g.ExpiresAt = g.CreatedAt.Add(generatedAudioLifetime)
	}, "extended expiry": func(r *core.MediaRequest) {
		r.Options.ResourceCreation.GeneratedMedia.ExpiresAt = r.Options.ResourceCreation.GeneratedMedia.CreatedAt.Add(31 * 24 * time.Hour)
	}, "wrong duration proof": func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedMedia.Inspection.Duration = 5 }, "short": func(r *core.MediaRequest) {
		g := r.Options.ResourceCreation.GeneratedMedia
		*g.Duration = 1.99
		g.Inspection.Duration = 1.99
	}, "long": func(r *core.MediaRequest) {
		g := r.Options.ResourceCreation.GeneratedMedia
		*g.Duration = 300.01
		g.Inspection.Duration = 300.01
	}, "nonfinite": func(r *core.MediaRequest) { *r.Options.ResourceCreation.GeneratedMedia.Duration = math.NaN() }, "upload mixed": func(r *core.MediaRequest) {
		r.Options.ResourceCreation.FileSources = []core.ResourceSourceInput{{ReferenceID: uuid.NewString()}}
	}, "prompt": func(r *core.MediaRequest) { r.Prompt = "ignored" }}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			calls := 0
			a := adapter(t, func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") })
			r := generatedAudioRequestFixture(4)
			edit(&r)
			if _, e := a.Generate(auxiliaryContext(), r); !errors.Is(e, core.ErrOptionsInvalid) || calls != 0 {
				t.Fatal("untrusted binding admitted", e, calls)
			}
		})
	}
}
func TestGeneratedAudioRejectsSnapshotTamperingAndUnsupportedDeletion(t *testing.T) {
	edits := map[string]func(*core.ResourceSnapshot){"raw ID": func(b *core.ResourceSnapshot) { b.RemoteID = "88" }, "bad HMAC": func(b *core.ResourceSnapshot) { b.RemoteID += "x" }, "old Kind audio": func(b *core.ResourceSnapshot) { b.Kind = "audio" }, "voice": func(b *core.ResourceSnapshot) { b.Kind = "voice" }, "other file": func(b *core.ResourceSnapshot) { b.Subtype = "video" }, "preset": func(b *core.ResourceSnapshot) { b.IsPreset = true }, "missing provenance": func(b *core.ResourceSnapshot) { b.Sources[0].Provenance = nil }, "changed task": func(b *core.ResourceSnapshot) { b.Sources[0].Provenance.TaskID = uuid.NewString() }, "changed artifact": func(b *core.ResourceSnapshot) { b.Sources[0].Provenance.ArtifactID = "2" }, "hash": func(b *core.ResourceSnapshot) { b.Sources[0].SHA256 = strings.Repeat("b", 64) }, "inspection": func(b *core.ResourceSnapshot) { b.Sources[0].Inspection.SizeBytes++ }, "duration": func(b *core.ResourceSnapshot) { *b.Sources[0].Duration = 5; b.Sources[0].Inspection.Duration = 5 }, "expired": func(b *core.ResourceSnapshot) {
		b.Sources[0].Provenance.CreatedAt = time.Now().Add(-31 * 24 * time.Hour)
		b.Sources[0].Provenance.ExpiresAt = time.Now().Add(-time.Hour)
	}, "extra source": func(b *core.ResourceSnapshot) { b.Sources = append(b.Sources, b.Sources[0]) }}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			calls := 0
			a := adapter(t, func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") })
			b := generatedAudioSnapshotFixture(t, a, auxiliaryContext(), 4)
			edit(&b)
			r := generatedAudioConsumerFixture(AvatarImageToVideo, b)
			if _, e := a.Generate(auxiliaryContext(), r); e == nil || calls != 0 {
				t.Fatal("tampered source dispatched", e, calls)
			}
			if _, e := a.DeleteResource(auxiliaryContext(), b); e == nil || calls != 0 {
				t.Fatal("untrusted local deletion confirmed", e, calls)
			}
		})
	}
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("duration reject must be local"); return nil, nil })
	b := generatedAudioSnapshotFixture(t, a, auxiliaryContext(), 300)
	r := generatedAudioConsumerFixture(LipSync, b)
	if e := a.ValidateRequest(auxiliaryContext(), r); !errors.Is(e, core.ErrOptionsInvalid) {
		t.Fatal("lip-sync accepted300second audio ID", e)
	}
}
