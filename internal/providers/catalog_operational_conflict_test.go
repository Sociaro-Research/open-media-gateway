package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	registry "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
)

func operationalConflictFixture(t *testing.T) (catalogsync.Capture, catalogsync.Observation, catalogsync.Observation) {
	t.Helper()
	manager, first, _ := operationalFixture(t, "alibaba", "wan27-r2v-api", "wan27")
	body, err := os.ReadFile("../../plugins/provider/alibaba/testdata/video-api/wan27-parameters.txt")
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(string(body), "integer from 2 to 15", "integer from 2 to 20")
	if changed == string(body) {
		t.Fatal("fixture mutation did not change duration")
	}
	second := first
	second.Source.ID = "wan27-second"
	second.Hash = fmt.Sprintf("%x", sha256.Sum256([]byte(changed)))
	second.Text = changed
	second.Entries, err = manager.InterpretCatalogSource(context.Background(), "alibaba", second.Source, changed)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := registry.Find("alibaba")
	ids := map[string]bool{}
	for _, entry := range first.Entries {
		ids[entry.ID] = true
	}
	capture := catalogsync.Capture{}
	for _, model := range definition.Profiles() {
		if ids[model.ID] {
			capture.Models = append(capture.Models, model)
			capture.IDs = append(capture.IDs, model.ID)
		}
	}
	schedule := catalogsync.Schedule{Provider: "alibaba", Time: "03:00", Version: 1, Sources: []catalogsync.Source{first.Source, second.Source}}
	if err := schedule.Validate(); err != nil {
		t.Fatal("reproduction configuration rejected", err)
	}
	return capture, first, second
}

func operationalConflicts(run catalogsync.Run) []catalogsync.Warning {
	var result []catalogsync.Warning
	for _, w := range run.OpenWarnings {
		if w.Reason == "Official sources disagree on operational field" {
			result = append(result, w)
		}
	}
	return result
}

func TestOperationalConflictingSourcesPreserveFactsWithoutOscillation(t *testing.T) {
	capture, first, second := operationalConflictFixture(t)
	now := time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC)
	baseline, _ := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{first}, now, nil)
	price := catalogsync.Rate{Unit: "second", Per: 1, Price: money.USD(12345), Source: "https://prices.example.test"}
	for id, entry := range baseline.Models {
		entry.Rates = []catalogsync.Rate{price}
		baseline.Models[id] = entry
	}
	var reference catalogsync.State
	for i, obs := range [][]catalogsync.Observation{{first, second}, {second, first}} {
		state, run := catalogsync.Reconcile(baseline, capture, obs, now.Add(time.Hour), nil)
		if len(operationalConflicts(run)) != 2 || run.Completeness.Status != "incomplete" {
			t.Fatal("conflict not explicitly reported", run.OpenWarnings, run.Completeness.Status)
		}
		if len(run.Changes) != 0 || state.Version != baseline.Version {
			t.Fatal("conflict published phantom changes", run.Changes)
		}
		for id, entry := range baseline.Models {
			if !reflect.DeepEqual(state.Models[id].Metadata.Operational["output.duration"], entry.Metadata.Operational["output.duration"]) || !reflect.DeepEqual(state.Models[id].Rates, entry.Rates) {
				t.Fatal("last-confirmed fact/provenance or rate changed", id)
			}
		}
		for _, model := range run.Completeness.Models {
			for _, field := range model.Fields {
				if field.Field == "output.duration" && field.Status != "unknown" {
					t.Fatal("differing compatible source values verified", field)
				}
			}
		}
		if i == 0 {
			reference = state
		} else if !reflect.DeepEqual(reference.Models, state.Models) || !reflect.DeepEqual(reference.Warnings, state.Warnings) {
			t.Fatal("source order changed publication or warning evidence")
		}
		for day := 2; day <= 3; day++ {
			var repeated catalogsync.Run
			state, repeated = catalogsync.Reconcile(state, capture, obs, now.Add(time.Duration(day)*time.Hour), nil)
			if len(repeated.Changes) != 0 || len(repeated.NewWarnings) != 0 || state.Version != baseline.Version || len(operationalConflicts(repeated)) != 2 {
				t.Fatal("unchanged sources created another publication/alert", repeated)
			}
		}
	}
	// An initial baseline may publish undisputed fields, but never either duration.
	initial, initialRun := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{first, second}, now, nil)
	for _, model := range capture.Models {
		if _, exists := initial.Models[model.ID].Metadata.Operational["output.duration"]; exists {
			t.Fatal("initial disagreement chose a source")
		}
	}
	next, repeat := catalogsync.Reconcile(initial, capture, []catalogsync.Observation{second, first}, now.Add(time.Hour), nil)
	if next.Version != initial.Version || len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 || len(operationalConflicts(initialRun)) != 2 {
		t.Fatal("initial conflict oscillates")
	}
}

func TestOperationalConflictRequiresFreshAgreementToResolve(t *testing.T) {
	capture, first, second := operationalConflictFixture(t)
	now := time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC)
	baseline, _ := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{first}, now, nil)
	conflicted, _ := catalogsync.Reconcile(baseline, capture, []catalogsync.Observation{first, second}, now.Add(time.Hour), nil)
	for _, scenario := range []string{"failed", "missing_field", "null_field", "missing_source", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			var changed catalogsync.Observation
			body, _ := json.Marshal(second)
			if err := json.Unmarshal(body, &changed); err != nil {
				t.Fatal(err)
			}
			obs := []catalogsync.Observation{first, changed}
			switch scenario {
			case "failed":
				changed.Error = "source unavailable"
				changed.Hash = ""
			case "missing_field":
				for i := range changed.Entries {
					delete(changed.Entries[i].OperationalFields, "output.duration")
				}
			case "null_field":
				for i := range changed.Entries {
					changed.Entries[i].OperationalFields["output.duration"] = json.RawMessage("null")
				}
			case "missing_source":
				obs = obs[:1]
			case "stale":
				obs = nil
			}
			if len(obs) == 2 {
				obs[1] = changed
			}
			state, run := catalogsync.Reconcile(conflicted, capture, obs, now.Add(2*time.Hour), nil)
			if len(operationalConflicts(run)) != 2 || len(run.ResolvedWarnings) != 0 || len(run.Changes) != 0 || state.Version != conflicted.Version {
				t.Fatal("unavailable source resolved conflict or changed publication", scenario, run)
			}
			for id, entry := range baseline.Models {
				if !reflect.DeepEqual(state.Models[id].Metadata.Operational["output.duration"], entry.Metadata.Operational["output.duration"]) {
					t.Fatal("lost last-confirmed provenance", scenario, id)
				}
			}
			next, repeat := catalogsync.Reconcile(state, capture, obs, now.Add(3*time.Hour), nil)
			if len(repeat.NewWarnings) != 0 || len(repeat.ResolvedWarnings) != 0 || len(operationalConflicts(repeat)) != 2 || len(repeat.Changes) != 0 || next.Version != state.Version {
				t.Fatal("unchanged unavailable evidence duplicated alert", scenario, repeat)
			}
		})
	}
	retired := capture
	retired.Inactive = capture.Models
	retired.Models = nil
	_, retiredRun := catalogsync.Reconcile(conflicted, retired, nil, now.Add(4*time.Hour), nil)
	if len(operationalConflicts(retiredRun)) != 0 {
		t.Fatal("inactive model retained an active operational conflict")
	}
	agreed := second
	agreed.Hash = first.Hash
	agreed.Text = first.Text
	agreed.Entries = first.Entries
	resolved, run := catalogsync.Reconcile(conflicted, capture, []catalogsync.Observation{agreed, first}, now.Add(4*time.Hour), nil)
	if len(operationalConflicts(run)) != 0 {
		t.Fatal("fresh exact agreement did not resolve conflict")
	}
	count := 0
	documentWarning := false
	for _, w := range run.ResolvedWarnings {
		if w.Reason == "Official sources disagree on operational field" {
			count++
		}
	}
	for _, w := range run.OpenWarnings {
		if strings.HasPrefix(w.Reason, "Documentation changed") {
			documentWarning = true
		}
	}
	if count != 2 || !documentWarning {
		t.Fatal("agreement must resolve field conflict without accepting changed document", run.OpenWarnings, run.ResolvedWarnings)
	}
	for _, model := range run.Completeness.Models {
		for _, field := range model.Fields {
			if field.Field == "output.duration" && field.Status != "verified" {
				t.Fatal("fresh field agreement not verified", field)
			}
		}
	}
	quietState, quiet := catalogsync.Reconcile(resolved, capture, []catalogsync.Observation{first, agreed}, now.Add(5*time.Hour), nil)
	if len(quiet.Changes) != 0 || len(quiet.NewWarnings) != 0 || len(quiet.ResolvedWarnings) != 0 || quietState.Version != resolved.Version {
		t.Fatal("confirmed agreement not stable")
	}
}

func TestOperationalMatchingSourcesCannotHideRepeatedMissingField(t *testing.T) {
	capture, first, second := operationalConflictFixture(t)
	second.Entries = first.Entries
	second.Hash = first.Hash
	now := time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC)
	baseline, positive := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{first, second}, now, nil)
	for _, model := range positive.Completeness.Models {
		for _, field := range model.Fields {
			if field.Field == "output.duration" && field.Status != "verified" {
				t.Fatal("matching source positive failed", field)
			}
		}
	}
	var missing catalogsync.Observation
	body, _ := json.Marshal(second)
	if err := json.Unmarshal(body, &missing); err != nil {
		t.Fatal(err)
	}
	missing.Hash = strings.Repeat("f", 64)
	for i := range missing.Entries {
		delete(missing.Entries[i].OperationalFields, "output.duration")
	}
	state := baseline
	for day := 1; day <= 3; day++ {
		next, run := catalogsync.Reconcile(state, capture, []catalogsync.Observation{first, missing}, now.Add(time.Duration(day)*time.Hour), nil)
		if run.Completeness.Status != "incomplete" || len(run.Changes) != 0 || next.Version != baseline.Version {
			t.Fatal("missing agreeing source field lost verification gap", day, run.Completeness.Status, run.Changes)
		}
		for _, model := range run.Completeness.Models {
			for _, field := range model.Fields {
				if field.Field == "output.duration" && field.Status != "stale" {
					t.Fatal("repeated missing field falsely verified", day, field)
				}
			}
		}
		if day > 1 && (len(run.NewWarnings) != 0 || len(run.ResolvedWarnings) != 0) {
			t.Fatal("unchanged omitted evidence caused another alert", run)
		}
		for id, entry := range baseline.Models {
			if !reflect.DeepEqual(next.Models[id].Metadata.Operational["output.duration"], entry.Metadata.Operational["output.duration"]) {
				t.Fatal("missing source changed last-confirmed fact", id)
			}
		}
		state = next
	}
}

func TestOperationalNullFieldCannotResolveAdapterReview(t *testing.T) {
	_, ob, model := operationalFixture(t, "alibaba", "happyhorse-r2v-api", "happyhorse")
	// Use an explicit synthetic mismatch after the native GW-175 correction.
	model.Ratios = append(append([]string(nil), model.Ratios...), "2:1")
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	now := time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC)
	state, initial := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{ob}, now, nil)
	if len(initial.OpenWarnings) != 1 {
		t.Fatal("expected native ratio mismatch", initial.OpenWarnings)
	}
	var unknown catalogsync.Observation
	body, _ := json.Marshal(ob)
	if err := json.Unmarshal(body, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown.Entries[0].OperationalFields["output.ratios"] = json.RawMessage("null")
	for day := 1; day <= 3; day++ {
		next, run := catalogsync.Reconcile(state, capture, []catalogsync.Observation{unknown}, now.Add(time.Duration(day)*time.Hour), nil)
		if len(run.OpenWarnings) != 1 || len(run.NewWarnings) != 0 || len(run.ResolvedWarnings) != 0 || run.OpenWarnings[0].Key != initial.OpenWarnings[0].Key || !reflect.DeepEqual(run.OpenWarnings[0].Evidence, initial.OpenWarnings[0].Evidence) {
			t.Fatal("unknown field resolved or rewrote pending adapter review", run)
		}
		state = next
	}
}
