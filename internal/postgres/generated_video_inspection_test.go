package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

func TestGeneratedVideoInspectionCaptureClonesAndReopensIndependentTimelines(t *testing.T) {
	f := newProviderResourceFixture(t)
	data := testmedia.MP4(2000, 1000, 60)
	info, err := mediafile.Inspect(data, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	for _, tc := range []struct {
		name   string
		native *float64
	}{{"native-unknown", nil}, {"native-matches-container", func() *float64 { v := 2.; return &v }()}, {"native-independent", func() *float64 { v := 4.; return &v }()}} {
		t.Run(tc.name, func(t *testing.T) {
			sourceID := uuid.NewString()
			proof := core.InputMediaEvidence{ReferenceID: sourceID, Type: "video", SHA256: hex.EncodeToString(sum[:]), ContentType: info.ContentType, SizeBytes: info.Size, Duration: info.Duration, Width: info.Width, Height: info.Height, FPS: info.FPS, FrameCount: info.FrameCount, AudioStreams: info.AudioStreams, VideoStreams: info.VideoStreams, AudioCodecs: core.CloneAudioCodecs(info.AudioCodecs)}
			source := &core.GeneratedResourceSource{TaskID: sourceID, TaskHandle: "private-existing-source-handle", OutputID: "private-primary-video", Operation: "reviewed-existing-model", Model: "reviewed-existing-model", SHA256: proof.SHA256, Duration: tc.native, Inspection: &proof}
			options := core.MediaOptions{ResourceIntent: &core.ResourceIntent{Kind: "file_resource", Subtype: "video", Name: "Owned generated video", GeneratedTaskID: sourceID}, ResourceCreation: &core.ResourceCreateRequest{Kind: "file_resource", Subtype: "video", Name: "Owned generated video", GeneratedVideo: source}}
			original := core.CloneResourceCreation(options.ResourceCreation)
			intent, err := resourceTaskCreationIntent(options)
			if err != nil || len(intent.Sources) != 1 || intent.Sources[0].Inspection == nil {
				t.Fatal(intent, err)
			}
			*intent.Sources[0].Inspection.VideoStreams = 9
			if !reflect.DeepEqual(source.Inspection, original.GeneratedVideo.Inspection) {
				t.Fatal("typed PG intent shares original inspection")
			}
			if source.Duration != nil {
				*intent.Sources[0].Duration = 19
				if *source.Duration != *original.GeneratedVideo.Duration {
					t.Fatal("typed PG intent shares native duration")
				}
			}
			id := insertPaidResourceTask(t, f, options)
			tx, err := f.db.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			refs, err := captureTaskResourceBindings(f.ctx, tx, id, []core.ProviderResource{{Kind: "file_resource", Subtype: "video", RemoteID: "private-generated-file-" + id, Status: core.ProviderSucceeded, Sources: []core.ResourceSourceInput{{MediaType: "video", AudioCodecs: []string{"provider-claimed-opus"}}}}})
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
			binding, err := NewProviderResourceRepository(reopened).GetBinding(f.ctx, refs[0].BindingID)
			if err != nil || len(binding.Sources) != 1 || binding.Sources[0].Inspection == nil {
				t.Fatal(binding, err)
			}
			got := binding.Sources[0]
			if got.ReferenceID != sourceID || got.SHA256 != proof.SHA256 || got.Inspection.ReferenceID != sourceID || got.Inspection.Duration != 2 || !reflect.DeepEqual(got.Duration, tc.native) || got.Inspection.AudioStreams == nil || *got.Inspection.AudioStreams != 0 || len(got.Inspection.AudioCodecs) != 0 {
				t.Fatal("host/generated timeline metadata changed or provider claims replaced inspection", got)
			}
			clone := providerresources.CloneSources(binding.Sources)
			*clone[0].Inspection.VideoStreams = 8
			if *binding.Sources[0].Inspection.VideoStreams != 1 {
				t.Fatal("reopened binding inspection aliases clone")
			}
			var raw []byte
			if err = reopened.QueryRowContext(f.ctx, `SELECT sources FROM provider_resources WHERE id=$1`, binding.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var persisted []core.ResourceSourceEvidence
			if err = json.Unmarshal(raw, &persisted); err != nil || len(persisted) != 1 || !reflect.DeepEqual(persisted[0].Duration, tc.native) || !core.EqualInputMediaEvidence(*persisted[0].Inspection, *got.Inspection) {
				t.Fatal("JSON persistence changed independent timelines", err)
			}
			if !reflect.DeepEqual(options.ResourceCreation, original) {
				t.Fatal("capture mutated original private generated source")
			}
		})
	}
}
