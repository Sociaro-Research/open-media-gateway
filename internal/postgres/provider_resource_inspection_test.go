package postgres

import (
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
)

func TestProviderResourceCapturePersistsOnlyHostInspection(t *testing.T) {
	f := newProviderResourceFixture(t)
	duration := 4.0
	audio, video := 0, 1
	source := core.ResourceSourceInput{ReferenceID: uuid.NewString(), MediaType: "video", ContentType: "video/mp4", SizeBytes: 8192, Width: 1280, Height: 720, FPS: 30, FrameCount: 120, AudioStreams: &audio, VideoStreams: &video, SHA256: strings.Repeat("e", 64), Duration: &duration}
	intent := &core.ResourceIntent{Kind: "file_resource", Subtype: "video", Name: "Inspected file", ReferenceIDs: []string{source.ReferenceID}}
	options := core.MediaOptions{ResourceIntent: intent, ResourceCreation: &core.ResourceCreateRequest{Kind: intent.Kind, Subtype: intent.Subtype, Name: intent.Name, FileSources: []core.ResourceSourceInput{source}}}
	id := insertPaidResourceTask(t, f, options)
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	refs, err := captureTaskResourceBindings(f.ctx, tx, id, []core.ProviderResource{{Kind: "file_resource", Subtype: "video", RemoteID: "private-file-proof", Status: core.ProviderSucceeded, Sources: []core.ResourceSourceInput{{Width: 999, Height: 999, FPS: 1, FrameCount: 999}}}})
	if err != nil || len(refs) != 1 {
		t.Fatal(refs, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	b, err := f.repo.GetBinding(f.ctx, refs[0].BindingID)
	if err != nil || len(b.Sources) != 1 || b.Sources[0].Inspection == nil {
		t.Fatal(b, err)
	}
	e := b.Sources[0].Inspection
	if e.Width != 1280 || e.Height != 720 || e.FPS != 30 || e.FrameCount != 120 || e.SizeBytes != 8192 || e.ContentType != "video/mp4" || e.AudioStreams == nil || *e.AudioStreams != 0 {
		t.Fatal("trusted metadata replaced by native report", e)
	}
	changed := providerresources.CloneSources(b.Sources)
	changed[0].Inspection.Width++
	if providerresources.EqualSources(changed, b.Sources) {
		t.Fatal("inspected width omitted from immutable comparison")
	}
	if *changed[0].Inspection.AudioStreams != 0 {
		t.Fatal("known zero streams lost")
	}
	*changed[0].Inspection.AudioStreams = 2
	if *b.Sources[0].Inspection.AudioStreams != 0 {
		t.Fatal("inspection stream pointer aliased")
	}
}
