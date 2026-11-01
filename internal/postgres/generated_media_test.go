package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

func TestGeneratedTTSPersistenceAdmissionAndReopen(t *testing.T) {
	f := newProviderResourceFixture(t)
	sourceID := uuid.NewString()
	created := time.Now().UTC().Truncate(time.Microsecond)
	duration := 6.
	a, v := 1, 0
	proof := core.InputMediaEvidence{ReferenceID: sourceID, Type: "audio", SHA256: strings.Repeat("a", 64), ContentType: "audio/wave", SizeBytes: 96044, Duration: duration, AudioStreams: &a, VideoStreams: &v}
	source := &core.GeneratedResourceSource{TaskID: sourceID, ArtifactID: "2", OutputID: "private-existing-audio", Operation: "kling-tts", Model: "kling-tts", MediaType: "audio", SHA256: proof.SHA256, CreatedAt: created, ExpiresAt: created.Add(30 * 24 * time.Hour), Duration: &duration, Inspection: &proof}
	artifacts, _ := json.Marshal([]core.StoredArtifact{{ID: "1", ProviderID: "other-private-output", MediaType: "audio", StorageKey: "synthetic/other.wav", SHA256: strings.Repeat("b", 64), ContentType: "audio/wav", SizeBytes: 96044}, {ID: "2", ProviderID: source.OutputID, MediaType: "audio", StorageKey: "synthetic/selected.wav", SHA256: source.SHA256, ContentType: "audio/wav", SizeBytes: 96044}})
	_, err := f.db.Exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,media_type,status,prompt,provider_identity,created_at,result_artifacts) VALUES($1,$2,$3,$2,$4,'kling-tts','speech','succeeded','',$5,$6,$7)`, sourceID, identity.OwnerID(f.ctx), identity.ProviderOwnerID(f.ctx), f.job.ProviderID, f.job.Revision, created, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	options := core.MediaOptions{ResourceIntent: &core.ResourceIntent{Kind: "file_resource", Subtype: "audio", Name: "Selected TTS", GeneratedTaskID: sourceID, GeneratedArtifactID: "2"}, ResourceCreation: &core.ResourceCreateRequest{Kind: "file_resource", Subtype: "audio", Name: "Selected TTS", GeneratedMedia: source}}
	task := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(f.ctx), ProviderOwnerID: identity.ProviderOwnerID(f.ctx), Provider: f.job.ProviderID, ProviderIdentity: f.job.Revision, Options: options}
	validate := func(wantErr bool) {
		t.Helper()
		tx, e := f.db.BeginTx(f.ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		e = validateTaskResourceSnapshots(f.ctx, tx, task)
		tx.Rollback()
		if wantErr {
			if !errors.Is(e, core.ErrOptionsInvalid) {
				t.Fatal("changed source accepted", e)
			}
		} else if e != nil {
			t.Fatal(e)
		}
	}
	validate(false)
	for _, change := range []func(){func() { task.ProviderIdentity = "other" }, func() { task.OwnerID = uuid.NewString() }, func() { task.Options.ResourceIntent.GeneratedArtifactID = "1" }, func() { task.Options.ResourceCreation.GeneratedMedia.SHA256 = strings.Repeat("c", 64) }} {
		task.Options.ResourceIntent = core.CloneResourceIntent(options.ResourceIntent)
		task.Options.ResourceCreation = core.CloneResourceCreation(options.ResourceCreation)
		task.OwnerID = identity.OwnerID(f.ctx)
		task.ProviderIdentity = f.job.Revision
		change()
		validate(true)
	}
	task.Options = options
	task.OwnerID = identity.OwnerID(f.ctx)
	task.ProviderIdentity = f.job.Revision
	accepted := insertPaidResourceTask(t, f, options)
	if _, err = f.db.Exec(`UPDATE tasks SET result_deleted_at=now() WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	validate(true) // New admission cannot bind deleted output.
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := captureTaskResourceBindings(f.ctx, tx, accepted, []core.ProviderResource{{Kind: "file_resource", Subtype: "audio", RemoteID: "signed-private-audio-binding", Status: core.ProviderSucceeded}})
	if err != nil || len(refs) != 1 {
		tx.Rollback()
		t.Fatal(refs, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), os.Getenv("GATEWAY_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	binding, err := NewProviderResourceRepository(reopened).GetBinding(f.ctx, refs[0].BindingID)
	if err != nil || len(binding.Sources) != 1 {
		t.Fatal(binding, err)
	}
	got := binding.Sources[0]
	if got.Provenance == nil || got.Provenance.ArtifactID != "2" || got.Provenance.Model != "kling-tts" || got.ReferenceID != sourceID || got.Inspection == nil || got.Inspection.Duration != 6 || !got.Provenance.ExpiresAt.Equal(source.ExpiresAt) {
		t.Fatal("selected source proof lost on reopen", got)
	}
	raw, _ := json.Marshal(binding)
	if strings.Contains(string(raw), source.OutputID) || strings.Contains(string(raw), "signed-private") {
		t.Fatal("private native identity exposed")
	}
}
