package catalogsync_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/sonilo"
)

func TestSoniloContractReplayAfterStateJSONRoundTrip(t *testing.T) {
	var model core.MediaModel
	for _, candidate := range sonilo.Definition().Profiles() {
		if candidate.ID == "text-to-sfx" {
			model = candidate
		}
	}
	if model.ID == "" || model.Parameters[0].Default != 8 {
		t.Fatal("missing native Sonilo numeric-default fixture")
	}
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	state, first := catalogsync.Reconcile(catalogsync.State{}, capture, nil, now, nil)
	if state.Version != 1 || len(first.Changes) != 1 {
		t.Fatalf("initial import: version=%d changes=%+v", state.Version, first.Changes)
	}
	for i := 1; i <= 3; i++ {
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		var saved catalogsync.State
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		next, run := catalogsync.Reconcile(saved, capture, nil, now.Add(time.Duration(i)*time.Hour), nil)
		if next.Version != state.Version || len(run.Changes) != 0 || run.EmailStatus != "not_needed" {
			t.Fatalf("unchanged replay %d: initial_version=%d repeat_version=%d changes=%+v", i, state.Version, next.Version, run.Changes)
		}
		if run.Completeness.Status != first.Completeness.Status {
			t.Fatalf("replay concealed independent completeness: %+v", run.Completeness)
		}
		state = next
	}
}

func TestContractDefaultsSurviveRepeatedPersistence(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b any
		same bool
	}{
		{"integral", 8, 8.0, true},
		{"negative", -2, json.Number("-2.0"), true},
		{"zero", 0, 0.0, true},
		{"fractional", 0.125, json.Number("1.25e-1"), true},
		{"nested", map[string]any{"items": []any{8, -0.5, true}}, map[string]any{"items": []any{8.0, json.Number("-5e-1"), true}}, true},
		{"large integral", uint64(9007199254740993), json.Number("9007199254740993.0"), true},
		{"large changed", uint64(9007199254740992), uint64(9007199254740993), false},
		{"fraction changed", -0.125, -0.25, false},
		{"string changed", 8, "8", false},
		{"boolean changed", 0, false, false},
		{"array order changed", []int{1, 2}, []int{2, 1}, false},
		{"nested changed", map[string]int{"n": 1}, map[string]int{"n": 2}, false},
		{"default removed", 0, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := core.MediaModel{ID: "provider-neutral", MediaType: "audio", Parameters: []core.ParameterField{{Key: "value", Default: tc.a}}}
			capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
			now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
			state, _ := catalogsync.Reconcile(catalogsync.State{}, capture, nil, now, nil)
			capture.Models[0].Parameters[0].Default = tc.b
			for i := 0; i < 3; i++ {
				data, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				var saved catalogsync.State
				if err := json.Unmarshal(data, &saved); err != nil {
					t.Fatal(err)
				}
				next, run := catalogsync.Reconcile(saved, capture, nil, now.Add(time.Duration(i+1)*time.Hour), nil)
				wantVersion, wantChanges := int64(1), 0
				if !tc.same {
					wantVersion = 2
					if i == 0 {
						wantChanges = 1
					}
				}
				if next.Version != wantVersion || len(run.Changes) != wantChanges {
					t.Fatalf("replay %d: version=%d changes=%+v", i, next.Version, run.Changes)
				}
				state = next
			}
		})
	}
}
