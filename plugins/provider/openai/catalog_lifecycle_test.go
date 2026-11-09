package openai

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"strings"
	"testing"
	"time"
)

const lifecycleFixture = "# Deprecations\n\n| Shutdown date | Model / system | Recommended replacement |\n| --- | --- | --- |\n| Dec 1, 2026 | `gpt-image-1.5` \\| `gpt-image-1.5-2025-12-16` | `gpt-image-2.6-flare` |\n| Jan 6, 2027 | `tts-1` | `gpt-realtime-2.1-mini` |\n| Jan 6, 2027 | `gpt-4o-mini-tts` | `gpt-realtime-2.1-mini` |\n| 2027-02-01 | `gpt-image-future` | `gpt-image-replacement` |\n"

func TestOfficialDeprecationsUseModelCellsAndExactDates(t *testing.T) {
	source := lifecycleSources()[0]
	entries, err := interpretLifecycle(source, lifecycleFixture)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.ID] = e.Lifecycle.ShutdownDate
	}
	for id, date := range map[string]string{"gpt-image-1.5": "2026-12-01", "gpt-image-1.5-2025-12-16": "2026-12-01", "tts-1": "2027-01-06", "tts-1-1106": "2027-01-06", "gpt-4o-mini-tts": "2027-01-06", "gpt-image-future": "2027-02-01"} {
		if got[id] != date {
			t.Fatal(id, got)
		}
	}
	if got["gpt-image-2.6-flare"] != "" || got["gpt-image-replacement"] != "" {
		t.Fatal("retired a replacement", got)
	}
	for _, body := range []string{strings.Replace(lifecycleFixture, "Dec 1, 2026", "Dec 32, 2026", 1), strings.Replace(lifecycleFixture, "Shutdown date", "Unknown boundary", 1), "JavaScript required"} {
		if _, err := interpretLifecycle(source, body); err == nil {
			t.Fatal("silently accepted incomplete lifecycle evidence", body)
		}
	}
	if entries, err := interpretLifecycle(catalogsync.Source{URL: "https://unregistered.example.test"}, lifecycleFixture); err != nil || entries != nil {
		t.Fatal("unregistered authority", entries, err)
	}
}
func TestCachedOpenAIModelsRecheckEveryReviewedBoundary(t *testing.T) {
	for _, tc := range []struct{ id, date string }{{"gpt-image-1.5", "2026-12-01"}, {"tts-1", "2027-01-06"}, {"gpt-audio-mini", "2027-01-20"}} {
		cutoff, _ := time.Parse("2006-01-02", tc.date)
		models := []core.MediaModel{describeModel(tc.id), describeModel("gpt-image-2.6-flare")}
		if len(activeModels(cloneModels(models), cutoff.Add(-time.Nanosecond))) != 2 || len(activeModels(cloneModels(models), cutoff)) != 1 {
			t.Fatal(tc)
		}
	}
	// A stale cache populated before a past retirement cannot advertise its old ID.
	a := &Adapter{models: []core.MediaModel{describeModel("gpt-image-1"), describeModel("gpt-image-2.6-flare")}, modelsAt: time.Now()}
	models, err := a.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "gpt-image-2.6-flare" {
		t.Fatal(models, err)
	}
}
