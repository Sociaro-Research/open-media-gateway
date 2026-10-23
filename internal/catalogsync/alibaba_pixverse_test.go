package catalogsync

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestAlibabaPixVerseReleaseEvidenceRequiresContractReview(t *testing.T) {
	// Excerpt of the saved 2026-10-22 Singapore release log (SHA-256
	// 887517bb96d0ada09e795e5d78b34af33c541a9d61e922cb24a20527a9251910).
	body, err := os.ReadFile("testdata/alibaba-pixverse-releases.md")
	if err != nil {
		t.Fatal(err)
	}
	source := Source{ID: "releases", Parser: "alibaba-releases", URL: "https://www.alibabacloud.com/help/en/model-studio/newly-released-models"}
	entries, text, err := Parse(source, body)
	if err != nil || len(entries) != 5 {
		t.Fatalf("release rows were discarded: %d entries, %v", len(entries), err)
	}
	ids := []string{"pixverse/pixverse-v6-t2v", "pixverse/pixverse-v6-it2v", "pixverse/pixverse-v6-kf2v", "pixverse/pixverse-v6-r2v"}
	byID := map[string]Entry{}
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for _, id := range ids {
		entry := byID[id]
		if entry.Metadata == nil || entry.Metadata.ReleaseDate != "2026-10-22" || !strings.Contains(entry.Metadata.Description, "PixVerse V6") || len(entry.Rates) != 0 || entry.Contract != nil {
			t.Fatalf("missing release evidence or invented execution/price: %s: %+v", id, entry)
		}
	}

	known := core.MediaModel{ID: "qwen-image-2.1-turbo", MediaType: "image"}
	capture := Capture{Models: []core.MediaModel{known}, IDs: append([]string{known.ID}, ids...)}
	observations := []Observation{{Source: source, Hash: "release-fixture", Text: text, Entries: entries}, {Source: Source{ID: "_api", Parser: "api"}}}
	now := time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)
	previous, _ := Reconcile(State{}, capture, observations, now, accepted)
	entry := previous.Models[known.ID]
	entry.Rates = []Rate{{Unit: "image", Per: 1, Price: 1234, Region: "Singapore"}}
	previous.Models[known.ID] = entry
	next, run := Reconcile(previous, capture, observations, now.Add(time.Hour), accepted)
	if len(run.Changes) != 0 || len(run.NewWarnings) != 0 || len(run.ResolvedWarnings) != 0 || len(run.OpenWarnings) != 8 || run.EmailStatus != "not_needed" || next.Version != previous.Version {
		t.Fatalf("repeat changed published state or silenced review: %+v", run)
	}
	if !reflect.DeepEqual(previous.Models[known.ID], next.Models[known.ID]) {
		t.Fatal("unknown releases changed an existing model or tariff")
	}
	for _, id := range ids {
		if _, enabled := next.Models[id]; enabled {
			t.Fatal("unreviewed PixVerse model entered the executable catalogue", id)
		}
		for _, sourceID := range []string{"_api", "releases"} {
			found := false
			for _, warning := range run.OpenWarnings {
				found = found || warning.Model == id && warning.Source == sourceID && warning.Reason == "Model requires an adapter contract before it can be enabled"
			}
			if !found {
				t.Fatal("model bypassed contract review", id, sourceID)
			}
		}
	}
}
