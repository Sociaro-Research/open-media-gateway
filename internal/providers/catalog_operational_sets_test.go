package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
)

func TestOperationalSelectorSetsAgreeAcrossDocumentAndManifest(t *testing.T) {
	root := os.Getenv("GATEWAY_OPERATIONAL_SOURCE_FIXTURES")
	if root == "" {
		t.Skip("exact saved source snapshots not supplied")
	}
	manager, prototype, _ := operationalFixture(t, "alibaba", "wan27-r2v-api", "wan27")
	body, err := os.ReadFile(filepath.Join(root, "wan27.html"))
	if err != nil {
		t.Fatal(err)
	}
	_, text, err := catalogsync.Parse(prototype.Source, body)
	if err != nil {
		t.Fatal(err)
	}
	first := prototype
	first.Text = text
	first.Hash = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	first.Entries, err = manager.InterpretCatalogSource(context.Background(), "alibaba", first.Source, text)
	if err != nil {
		t.Fatal(err)
	}
	capture, _, _ := operationalConflictFixture(t)
	now := time.Date(2026, 10, 26, 22, 0, 0, 0, time.UTC)
	baseline, positive := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{first}, now, nil)
	if positive.Completeness.Status != "complete" {
		t.Fatal("exact saved source control failed", positive.Completeness.Status)
	}
	source := catalogsync.Source{ID: "contract-mirror", URL: "https://public-contract.fixture.test/wan27.json", Parser: "manifest-v1"}
	for _, tc := range []struct {
		name     string
		values   []string
		conflict bool
	}{
		{"same", []string{"1080p", "720p"}, false},
		{"reverse", []string{"720p", "1080p"}, false},
		{"duplicates", []string{"720p", "1080p", "720p"}, false},
		{"added", []string{"720p", "1080p", "2160p"}, true},
		{"removed", []string{"720p"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.values)
			var entries []catalogsync.Entry
			for _, entry := range first.Entries {
				entries = append(entries, catalogsync.Entry{ID: entry.ID, OperationalFields: map[string]json.RawMessage{"output.resolutions": raw}})
			}
			payload, _ := json.Marshal(map[string]any{"models": entries})
			parsed, text, err := catalogsync.Parse(source, payload)
			if err != nil {
				t.Fatal(err)
			}
			mirror := catalogsync.Observation{Source: source, Entries: parsed, Text: text, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(text)))}
			schedule := catalogsync.Schedule{Provider: "alibaba", Version: 1, Time: "03:00", Sources: []catalogsync.Source{first.Source, source}}
			if err := schedule.Validate(); err != nil {
				t.Fatal(err)
			}
			var reference catalogsync.State
			for order, obs := range [][]catalogsync.Observation{{first, mirror}, {mirror, first}} {
				state, run := catalogsync.Reconcile(baseline, capture, obs, now.Add(time.Hour), nil)
				if len(run.Changes) != 0 || state.Version != baseline.Version {
					t.Fatal("equivalent set or conflict changed publication", tc.name, run.Changes)
				}
				if tc.conflict {
					if len(operationalConflicts(run)) != 2 || run.Completeness.Status != "incomplete" {
						t.Fatal("actual added/removed choices not conflicting", run.Completeness.Status, run.OpenWarnings)
					}
				} else {
					if len(run.OpenWarnings) != 0 || run.Completeness.Status != "complete" {
						t.Fatal("same selector set reported conflict", run.Completeness.Status, run.OpenWarnings)
					}
				}
				for id, entry := range baseline.Models {
					fact := state.Models[id].Metadata.Operational["output.resolutions"]
					if tc.conflict {
						if !reflect.DeepEqual(fact, entry.Metadata.Operational["output.resolutions"]) {
							t.Fatal("real conflict changed last-confirmed provenance")
						}
					} else if string(fact.Value) != string(raw) || fact.Snapshot != mirror.Hash || fact.Source.URL != source.URL {
						t.Fatal("raw source value/hash rewritten by canonical comparison", fact)
					}
				}
				if order == 0 {
					reference = state
				} else if !reflect.DeepEqual(reference.Models, state.Models) || !reflect.DeepEqual(reference.Warnings, state.Warnings) {
					t.Fatal("observation order affected result")
				}
				next, repeat := catalogsync.Reconcile(state, capture, obs, now.Add(2*time.Hour), nil)
				if len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 || len(repeat.ResolvedWarnings) != 0 || next.Version != state.Version {
					t.Fatal("identical set observations not stable", repeat)
				}
			}
		})
	}
	// A single source may reorder/deduplicate alternatives without a new fact or
	// catalogue version. Its changed document fingerprint is still independent.
	var changed catalogsync.Observation
	encoded, _ := json.Marshal(first)
	if err := json.Unmarshal(encoded, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Hash = strings.Repeat("f", 64)
	for i := range changed.Entries {
		changed.Entries[i].OperationalFields["output.resolutions"] = json.RawMessage(`["720p","1080p","720p"]`)
	}
	state, run := catalogsync.Reconcile(baseline, capture, []catalogsync.Observation{changed}, now.Add(3*time.Hour), nil)
	if len(run.Changes) != 0 || state.Version != baseline.Version || len(operationalConflicts(run)) != 0 {
		t.Fatal("order-only source edit published phantom change", run.Changes, run.OpenWarnings)
	}
	documentWarning := false
	for _, w := range run.OpenWarnings {
		documentWarning = documentWarning || strings.HasPrefix(w.Reason, "Documentation changed")
	}
	if !documentWarning {
		t.Fatal("semantic equality acknowledged document fingerprint")
	}
	for _, model := range capture.Models {
		fact := state.Models[model.ID].Metadata.Operational["output.resolutions"]
		if fact.Snapshot != changed.Hash || string(fact.Value) != `["720p","1080p","720p"]` {
			t.Fatal("current exact raw provenance was canonicalized", fact)
		}
	}
}
