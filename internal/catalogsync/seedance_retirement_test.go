package catalogsync

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestSeedanceReviewedTimestampAdoptsIntoExistingCatalogueAndKeepsHistory(t *testing.T) {
	const id = "seedance-1-5-pro-251215"
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	reviewed := modelcatalog.Enrich("bytedance", core.MediaModel{ID: id, MediaType: "video"})
	historicalRates := []Rate{{Region: "Singapore", Unit: "token", Per: 1_000_000, Price: money.USD(1_200_000)}}
	old := State{Version: 1, Models: map[string]Entry{id: {
		ID: id, Contract: &core.MediaModel{ID: id, MediaType: "video"},
		Metadata: &core.ModelMetadata{ShutdownDate: "2030-01-01"},
		Rates:    historicalRates,
	}}}
	immutable, _ := json.Marshal(old)
	ob := []Observation{{Source: Source{ID: "models", URL: "https://example.test/models", Parser: "manifest-v1"}, Hash: "stale",
		Entries: []Entry{{ID: id, Metadata: &core.ModelMetadata{ShutdownDate: "2030-01-01", ShutdownAt: "2030-01-01T00:00:00Z"}}}}}
	checkRate := func(core.MediaModel, Rate) error { t.Fatal("historical tariff was revalidated"); return nil }
	previous := old
	for _, instant := range []time.Time{cutoff.Add(-time.Nanosecond), cutoff, cutoff.Add(time.Nanosecond), cutoff.In(time.FixedZone("UTC+8", 8*3600))} {
		capture := Capture{Reviewed: []core.MediaModel{reviewed}, Models: []core.MediaModel{reviewed}, IDs: []string{id}}
		if modelcatalog.IsInactive("bytedance", id, instant) {
			capture.Inactive = []core.MediaModel{reviewed}
		}
		state, _ := Reconcile(previous, capture, ob, instant, checkRate)
		entry := state.Models[id]
		inactive := !instant.Before(cutoff)
		if entry.Inactive != inactive || (entry.Contract == nil) != inactive {
			t.Fatal("stale discovery overrode reviewed admission state", entry)
		}
		if entry.Metadata.ShutdownAt != "2026-11-11T09:00:00Z" || entry.Metadata.ShutdownDate != "2026-11-11" || !reflect.DeepEqual(entry.Rates, historicalRates) {
			t.Fatal("reviewed timestamp or historical prices lost", entry)
		}
		if len(state.Warnings) == 0 {
			t.Fatal("conflicting upstream shutdown date was silently accepted")
		}
		quiet, run := Reconcile(state, capture, ob, instant, checkRate)
		if quiet.Version != state.Version || len(run.Changes) != 0 || !reflect.DeepEqual(quiet.Models[id], state.Models[id]) {
			t.Fatal("repeat synchronization oscillates", run.Changes)
		}
		previous = quiet
	}
	after, _ := json.Marshal(old)
	if string(after) != string(immutable) {
		t.Fatal("historical input state mutated")
	}
}

func TestObservedTimestampDoesNotCreateReviewedRetirement(t *testing.T) {
	model := core.MediaModel{ID: "active-fixture", MediaType: "video"}
	ob := Observation{Source: Source{ID: "notice", URL: "https://example.test/notice", Parser: "manifest-v1"}, Hash: "observation",
		Entries: []Entry{{ID: model.ID, Metadata: &core.ModelMetadata{ShutdownDate: "2026-11-11", ShutdownAt: "2026-11-11T09:00:00Z"}}}}
	state, _ := Reconcile(State{}, Capture{Models: []core.MediaModel{model}}, []Observation{ob}, time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), func(core.MediaModel, Rate) error { return nil })
	if state.Models[model.ID].Inactive || state.Models[model.ID].Metadata.ShutdownAt != "" {
		t.Fatal("unreviewed observation became a retirement control", state.Models[model.ID])
	}
}
