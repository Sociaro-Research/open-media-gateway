package postgres

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
)

func TestAudioCodecEvidencePrivateCaptureAndDatabaseReopen(t *testing.T) {
	f := newProviderResourceFixture(t)
	for _, tc := range []struct {
		name    string
		streams *int
		codecs  []string
	}{
		{name: "known audio", streams: func() *int { v := 2; return &v }(), codecs: []string{"aac", "mp3"}},
		{name: "proven silence", streams: func() *int { v := 0; return &v }()},
		{name: "unknown inventory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			duration := 4.0
			video := 1
			source := core.ResourceSourceInput{ReferenceID: uuid.NewString(), MediaType: "video", ContentType: "video/mp4", SizeBytes: 8192, Width: 1280, Height: 720, FPS: 30, FrameCount: 120, AudioStreams: tc.streams, VideoStreams: &video, AudioCodecs: core.CloneAudioCodecs(tc.codecs), SHA256: strings.Repeat("e", 64), Duration: &duration}
			intent := &core.ResourceIntent{Kind: "file_resource", Subtype: "video", Name: "Inspected codec file", ReferenceIDs: []string{source.ReferenceID}}
			options := core.MediaOptions{ResourceIntent: intent, ResourceCreation: &core.ResourceCreateRequest{Kind: intent.Kind, Subtype: intent.Subtype, Name: intent.Name, FileSources: []core.ResourceSourceInput{source}}}
			id := insertPaidResourceTask(t, f, options)
			tx, err := f.db.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fakeAudio := 3
			refs, err := captureTaskResourceBindings(f.ctx, tx, id, []core.ProviderResource{{Kind: "file_resource", Subtype: "video", RemoteID: "private-file-codec-proof-" + id, Status: core.ProviderSucceeded, Sources: []core.ResourceSourceInput{{AudioStreams: &fakeAudio, AudioCodecs: []string{"opus", "opus", "opus"}}}}})
			if err != nil || len(refs) != 1 {
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
			repo := NewProviderResourceRepository(reopened)
			binding, err := repo.GetBinding(f.ctx, refs[0].BindingID)
			if err != nil || len(binding.Sources) != 1 || binding.Sources[0].Inspection == nil {
				t.Fatal(binding, err)
			}
			proof := binding.Sources[0].Inspection
			if !reflect.DeepEqual(proof.AudioStreams, tc.streams) || !reflect.DeepEqual(proof.AudioCodecs, tc.codecs) {
				t.Fatal("native claims replaced actual trusted codec inventory", proof)
			}
			var raw []byte
			if err = reopened.QueryRowContext(f.ctx, `SELECT sources FROM provider_resources WHERE id=$1`, binding.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var stored []core.ResourceSourceEvidence
			if err = json.Unmarshal(raw, &stored); err != nil || len(stored) != 1 || !core.EqualInputMediaEvidence(*proof, *stored[0].Inspection) {
				t.Fatal("private JSON changed codec evidence", err)
			}
			if len(tc.codecs) > 0 {
				copy := providerresources.CloneSources(binding.Sources)
				copy[0].Inspection.AudioCodecs[0] = "opus"
				if providerresources.EqualSources(copy, binding.Sources) || binding.Sources[0].Inspection.AudioCodecs[0] != "aac" {
					t.Fatal("private clone aliases or ignores codec evidence")
				}
			}
			public, err := json.Marshal(binding)
			if err != nil || strings.Contains(string(public), "audio_codecs") || strings.Contains(string(public), source.SHA256) {
				t.Fatal("private codec/hash evidence leaked", string(public), err)
			}
		})
	}
}
