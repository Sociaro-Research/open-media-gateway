package openai

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const lifecycleFixture = "# Deprecations\n\n| Shutdown date | Model / system | Recommended replacement |\n| --- | --- | --- |\n| Dec 1, 2026 | `gpt-image-1.5` \\| `gpt-image-1.5-2025-12-16` | `gpt-image-2.6-flare` |\n| Jan 6, 2027 | `tts-1` | `gpt-realtime-2.1-mini` |\n| January 6, 2027 | `gpt-4o-mini-tts` | `gpt-realtime-2.1-mini` |\n| 2027-02-01 | `gpt-image-future` | `gpt-image-replacement` |\n"

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
	for _, body := range []string{strings.Replace(lifecycleFixture, "Dec 1, 2026", "Dec 32, 2026", 1), strings.Replace(lifecycleFixture, "Shutdown date", "Unknown boundary", 1), lifecycleFixture + "\n| Jan 6, 2027 | `tts-1-hd` | `gpt-realtime-2.1-mini` |\n", "JavaScript required"} {
		if _, err := interpretLifecycle(source, body); err == nil {
			t.Fatal("silently accepted incomplete lifecycle evidence", body)
		}
	}
	if entries, err := interpretLifecycle(catalogsync.Source{URL: "https://unregistered.example.test"}, lifecycleFixture); err != nil || entries != nil {
		t.Fatal("unregistered authority", entries, err)
	}
}

// testdata/deprecations.md keeps verbatim rows of the official deprecations.md
// fetched on 2026-11-14 (sha256 7abd85d08446beeb7b8f41692856a5b463ce6a1647337e647a5d4f8adbe45a83).
func TestCurrentDeprecationTablesUseAffectedModelCells(t *testing.T) {
	b, err := os.ReadFile("testdata/deprecations.md")
	if err != nil {
		t.Fatal(err)
	}
	doc, source := string(b), lifecycleSources()[0]
	entries, err := interpretLifecycle(source, doc)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.ID] = e.Lifecycle.ShutdownDate
	}
	// Exhaustive: replacement-only IDs (gpt-image-2.6-*, the dall-e-2 replacements), text models and feature rows stay out.
	want := map[string]string{
		"gpt-image-1-mini": "2026-12-01", "gpt-image-1.5": "2026-12-01", "gpt-image-1.5-2025-12-16": "2026-12-01", "chatgpt-image-latest": "2026-12-01",
		"tts-1": "2027-01-06", "tts-1-1106": "2027-01-06", "tts-1-hd": "2027-01-06", "tts-1-hd-1106": "2027-01-06",
		"gpt-4o-mini-tts": "2027-01-06", "gpt-4o-mini-tts-2025-03-20": "2027-01-06", "gpt-4o-mini-tts-2025-12-15": "2027-01-06",
		"gpt-audio": "2027-01-20", "gpt-audio-2025-08-28": "2027-01-20", "gpt-audio-mini": "2027-01-20", "gpt-audio-mini-2025-12-15": "2027-01-20",
		"gpt-image-2": "2027-02-10", "gpt-image-2-2026-04-21": "2027-02-10", "gpt-image-1": "2026-10-23",
		"sora-2": "2026-09-24", "sora-2-pro": "2026-09-24", "sora-2-2025-10-06": "2026-09-24", "sora-2-2025-12-08": "2026-09-24", "sora-2-pro-2025-10-06": "2026-09-24",
		"gpt-audio-mini-2025-10-06": "2026-07-23", "dall-e-2": "2026-05-12", "dall-e-3": "2026-05-12",
	}
	if len(entries) != len(want) || !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	for _, body := range []string{
		strings.Replace(doc, "Shutdown date", "Retirement date", 1),
		strings.Replace(doc, "Notice date", "Shutdown date", 1),
		strings.Replace(doc, "| Status ", "| Model ", 1),
		strings.Replace(doc, "| Deprecated |", "|", 1),
		strings.Replace(doc, "Dec 1, 2026", "Dec 32, 2026", 1),
		// A feature shutdown naming a media model needs review, not a guessed retirement.
		strings.Replace(doc, "`/v1/audio/translations`", "`tts-1` speech", 1),
	} {
		if _, err := interpretLifecycle(source, body); err == nil {
			t.Fatal("silently accepted incomplete lifecycle evidence", body)
		}
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
