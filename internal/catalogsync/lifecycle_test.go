package catalogsync

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOfficialLifecyclePublishesAndRetiresWithoutDiscoveryRemoval(t *testing.T) {
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	source := Source{ID: "notice", URL: "https://official.example.test/lifecycle", Parser: "document"}
	model := core.MediaModel{ID: "reviewed-media", MediaType: "video"}
	capture := Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}, LifecycleSources: []Source{source}}
	observation := Observation{Source: source, Hash: "new", Entries: []Entry{{ID: model.ID, Lifecycle: &Lifecycle{ShutdownDate: "2026-11-11", ShutdownAt: "2026-11-11T09:00:00Z"}}, {ID: "unknown-media", Lifecycle: &Lifecycle{ShutdownDate: "2026-11-11"}}}}
	old := State{Observations: map[string]Observation{source.ID: {Source: source, Hash: "old"}}}
	before, _ := json.Marshal(old)
	check := func(core.MediaModel, Rate) error { t.Fatal("unexpected price check"); return nil }
	state, run := Reconcile(old, capture, []Observation{observation}, cutoff.Add(-time.Nanosecond), check)
	entry := state.Models[model.ID]
	if entry.Lifecycle == nil || entry.Inactive || entry.Contract == nil || entry.Metadata.ShutdownAt != "2026-11-11T09:00:00Z" {
		t.Fatal(entry)
	}
	if len(run.OpenWarnings) != 1 || run.OpenWarnings[0].Model != "unknown-media" {
		t.Fatal("successful interpretation warned or unknown model enabled", run.OpenWarnings)
	}
	_, report, _ := Report(run)
	if !strings.Contains(report, "APPLIED CHANGES") || !strings.Contains(report, "lifecycle") || !strings.Contains(report, "unknown-media") {
		t.Fatal(report)
	}
	state, run = Reconcile(state, capture, []Observation{observation}, cutoff, check)
	if !state.Models[model.ID].Inactive || state.Models[model.ID].Contract != nil {
		t.Fatal("stale discovery reactivated retired model")
	}
	if len(run.Changes) != 2 {
		t.Fatal("expected contract removal and inactive change", run.Changes)
	}
	quiet, repeat := Reconcile(state, capture, []Observation{observation}, cutoff.Add(time.Hour), check)
	if len(repeat.Changes) != 0 || quiet.Version != state.Version {
		t.Fatal("quiet source oscillated", repeat.Changes)
	}
	after, _ := json.Marshal(old)
	if string(before) != string(after) {
		t.Fatal("previous state mutated")
	}
	failed := Observation{Source: source, Error: "fixture failure"}
	preserved, _ := Reconcile(state, capture, []Observation{failed}, cutoff.Add(time.Hour), check)
	if !preserved.Models[model.ID].Inactive || !reflect.DeepEqual(preserved.Models[model.ID].Lifecycle, entry.Lifecycle) {
		t.Fatal("failure discarded retirement")
	}
}

func TestLifecycleAuthorityConflictsAndInvalidDates(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	a := Source{ID: "a", URL: "https://official.example.test/a", Parser: "document"}
	b := Source{ID: "b", URL: "https://official.example.test/b", Parser: "document"}
	model := core.MediaModel{ID: "m", MediaType: "image"}
	good := Lifecycle{ShutdownDate: "2026-11-09"}
	for _, tc := range []struct {
		name    string
		trusted []Source
		obs     []Observation
	}{
		{"unregistered", nil, []Observation{{Source: a, Hash: "a", Entries: []Entry{{ID: "m", Lifecycle: &good}}}}},
		{"parser spoof", []Source{a}, []Observation{{Source: Source{ID: "a", URL: a.URL, Parser: "manifest-v1"}, Hash: "a", Entries: []Entry{{ID: "m", Lifecycle: &good}}}}},
		{"invalid calendar date", []Source{a}, []Observation{{Source: a, Hash: "a", Entries: []Entry{{ID: "m", Lifecycle: &Lifecycle{ShutdownDate: "2026-02-30"}}}}}},
		{"conflict", []Source{a, b}, []Observation{{Source: a, Hash: "a", Entries: []Entry{{ID: "m", Lifecycle: &good}}}, {Source: b, Hash: "b", Entries: []Entry{{ID: "m", Lifecycle: &Lifecycle{ShutdownDate: "2027-01-01"}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, run := Reconcile(State{}, Capture{Models: []core.MediaModel{model}, LifecycleSources: tc.trusted}, tc.obs, now, func(core.MediaModel, Rate) error { return nil })
			if state.Models["m"].Inactive || state.Models["m"].Lifecycle != nil || len(run.OpenWarnings) == 0 {
				t.Fatal(state, run)
			}
		})
	}
}

func TestLifecycleAvailabilityIsNotReleaseDate(t *testing.T) {
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	lifecycle := Lifecycle{AvailableAt: at.Format(time.RFC3339), ShutdownDate: "2026-12-01"}
	if !lifecycle.Unavailable(at.Add(-time.Nanosecond)) || lifecycle.Unavailable(at) || !lifecycle.Unavailable(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(lifecycle)
	}
	model := core.MediaModel{ID: "m", MediaType: "image", Metadata: &core.ModelMetadata{ReleaseDate: "2027-01"}}
	state, _ := Reconcile(State{}, Capture{Models: []core.MediaModel{model}}, nil, at, func(core.MediaModel, Rate) error { return nil })
	if state.Models["m"].Inactive {
		t.Fatal("release month became admission control")
	}
}

type requiredLifecycleProvider struct{ source Source }

func (p requiredLifecycleProvider) CaptureCatalog(context.Context, string) (Capture, error) {
	return Capture{LifecycleSources: []Source{p.source}}, errors.New("discovery unavailable")
}
func (requiredLifecycleProvider) CheckCatalogRate(context.Context, string, core.MediaModel, Rate) error {
	return nil
}
func (requiredLifecycleProvider) InterpretCatalogSource(context.Context, string, Source, string) ([]Entry, error) {
	return []Entry{{ID: "m", Lifecycle: &Lifecycle{ShutdownDate: "2026-11-09"}}}, nil
}

func TestRunnerChecksRequiredLifecycleOnOldSchedulesAndDiscoveryFailure(t *testing.T) {
	source := Source{ID: "official", URL: "https://official.example.test/deprecations", Parser: "document"}
	schedule := Schedule{Provider: "fixture", Time: "03:00", Sources: []Source{}}
	original, _ := json.Marshal(schedule)
	repo := &inactiveRunnerRepository{state: State{Models: map[string]Entry{"m": {ID: "m", Contract: &core.MediaModel{ID: "m", MediaType: "image"}}}}}
	calls := 0
	client := &http.Client{Transport: sourceAliasTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.String() != source.URL || r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") == "" {
			t.Fatal("incorrect public request", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat("Public official lifecycle evidence. ", 5)))}, nil
	})}
	runner := Runner{Repo: repo, Providers: requiredLifecycleProvider{source}, Client: client}
	if err := runner.check(context.Background(), schedule, Settings{Timezone: "UTC"}, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(schedule)
	if calls != 1 || string(original) != string(after) || !repo.state.Models["m"].Inactive || repo.state.Models["m"].Contract != nil {
		t.Fatal("old schedule missed required source or was changed", calls, repo.state)
	}
	if len(repo.run.OpenWarnings) != 1 || repo.run.OpenWarnings[0].Source != "_api" || len(repo.run.Changes) == 0 {
		t.Fatal("failure and applied retirement were misclassified", repo.run)
	}
	// An existing exact source is fetched once, preserving its saved ID.
	sources := captureSources([]Source{{ID: "saved", URL: source.URL, Parser: source.Parser}}, []Source{source})
	if len(sources) != 1 || sources[0].ID != "saved" {
		t.Fatal(sources)
	}
}

func TestHistoricalTariffsDoNotWarnAsFailedRetirement(t *testing.T) {
	source := Source{ID: "official", URL: "https://official.example.test", Parser: "document"}
	model := core.MediaModel{ID: "m", MediaType: "image"}
	rate := Rate{Region: "fixture", Unit: "image", Per: 1, Price: 1}
	capture := Capture{Models: []core.MediaModel{model}, LifecycleSources: []Source{source}}
	ob := Observation{Source: source, Hash: "fixture", Entries: []Entry{{ID: "m", Lifecycle: &Lifecycle{ShutdownDate: "2026-11-09"}, Rates: []Rate{rate}}}}
	state, run := Reconcile(State{}, capture, []Observation{ob}, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), func(core.MediaModel, Rate) error { t.Fatal("retired tariff revalidated"); return nil })
	if !state.Models["m"].Inactive || len(run.OpenWarnings) != 0 || len(run.Changes) == 0 {
		t.Fatal(state, run)
	}
}

func TestLifecycleConflictIsOrderIndependentAndMissingRecordWarns(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	a := Source{ID: "a", URL: "https://official.example.test/a", Parser: "document"}
	b := Source{ID: "b", URL: "https://official.example.test/b", Parser: "document"}
	model := core.MediaModel{ID: "m", MediaType: "image"}
	capture := Capture{Models: []core.MediaModel{model}, LifecycleSources: []Source{a, b}}
	first := Observation{Source: a, Hash: "one", Entries: []Entry{{ID: "m", Lifecycle: &Lifecycle{ShutdownDate: "2026-12-01"}}}}
	state, _ := Reconcile(State{}, capture, []Observation{first}, now, nil)
	other := Observation{Source: b, Hash: "two", Entries: []Entry{{ID: "m", Lifecycle: &Lifecycle{ShutdownDate: "2026-12-02"}}}}
	one, runOne := Reconcile(state, capture, []Observation{first, other}, now, nil)
	two, runTwo := Reconcile(state, capture, []Observation{other, first}, now, nil)
	if !reflect.DeepEqual(one.Models, two.Models) || !reflect.DeepEqual(runOne.OpenWarnings, runTwo.OpenWarnings) || len(runOne.OpenWarnings) != 1 {
		t.Fatal("unstable conflict", runOne, runTwo)
	}
	missing := first
	missing.Entries = nil
	missing.Hash = "missing"
	next, run := Reconcile(state, capture, []Observation{missing}, now, nil)
	if next.Models["m"].Lifecycle.ShutdownDate != "2026-12-01" || len(run.OpenWarnings) == 0 {
		t.Fatal("disappeared boundary silently discarded or accepted", next, run)
	}
	// A changed official source URL can replace provenance without changing dates.
	replacement := first
	replacement.Source = b
	moved, run := Reconcile(state, capture, []Observation{replacement}, now, nil)
	if moved.Models["m"].Lifecycle.Source != b.URL || len(run.OpenWarnings) != 0 {
		t.Fatal("official source move was not adopted", moved, run)
	}
}

func TestUninterpretedDateCannotDisplaceConfirmedLifecycleMetadata(t *testing.T) {
	source := Source{ID: "notice", URL: "https://official.example.test/notice", Parser: "document"}
	other := Source{ID: "manifest", URL: "https://example.test/evidence", Parser: "manifest-v1"}
	capture := Capture{Models: []core.MediaModel{{ID: "m", MediaType: "image"}}, LifecycleSources: []Source{source}}
	observations := []Observation{{Source: source, Hash: "official", Entries: []Entry{{ID: "m", Lifecycle: &Lifecycle{ShutdownDate: "2026-12-01"}}}}, {Source: other, Hash: "conflict", Entries: []Entry{{ID: "m", Metadata: &core.ModelMetadata{ShutdownDate: "2027-01-01"}}}}}
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	state, run := Reconcile(State{}, capture, observations, now, nil)
	if state.Models["m"].Metadata.ShutdownDate != "2026-12-01" || len(run.OpenWarnings) == 0 {
		t.Fatal("unreviewed metadata displaced confirmed lifecycle", state, run)
	}
	found := false
	for _, s := range state.Models["m"].Metadata.Sources {
		found = found || s.URL == source.URL
	}
	if !found {
		t.Fatal("official provenance missing from public metadata")
	}
	_, repeat := Reconcile(state, capture, observations, now, nil)
	if len(repeat.Changes) != 0 {
		t.Fatal("conflicting metadata oscillated", repeat.Changes)
	}
}
