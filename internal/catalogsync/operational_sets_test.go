package catalogsync

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationalAllowedValuesUseSetEquality(t *testing.T) {
	for _, field := range []string{"output.resolutions", "output.ratios", "output.formats", "voices", "reference_modes", "workflow.references.ratios", "workflow.references.image.content_types", "workflow.references.video.codecs"} {
		a := json.RawMessage(`["a","b"]`)
		b := json.RawMessage(`["b","a","a"]`)
		if !sameOperationalValue(field, a, b) {
			t.Errorf("unordered choices treated as ordered for %s", field)
		}
		for _, different := range []json.RawMessage{json.RawMessage(`["a"]`), json.RawMessage(`["a","b","c"]`), json.RawMessage(`["A","b"]`), json.RawMessage(`["a",1]`)} {
			if sameOperationalValue(field, a, different) {
				t.Errorf("different or invalid choices accepted for %s: %s", field, different)
			}
		}
	}
	if !sameOperationalValue("reference_prompt", json.RawMessage(`{"syntax":"numbered","types":["image","video"]}`), json.RawMessage(`{"types":["video","image","image"],"syntax":"numbered"}`)) {
		t.Fatal("reference modality alternatives not normalized")
	}
	for _, field := range []string{"reference_positions", "workflow.first_last_frames.image.roles", "workflow.references.image.positions", "unknown.choices", "output.resolutions.extra"} {
		a := json.RawMessage(`["first","last"]`)
		b := json.RawMessage(`["last","first"]`)
		if sameOperationalValue(field, a, b) || sameOperationalValue(field, a, json.RawMessage(`["first","last","last"]`)) {
			t.Fatal("ordered or unknown data sorted", field)
		}
		obs := []Observation{
			{Source: Source{ID: "a"}, Hash: strings.Repeat("a", 64), Entries: []Entry{{ID: "model", OperationalFields: map[string]json.RawMessage{field: a}}}},
			{Source: Source{ID: "b"}, Hash: strings.Repeat("b", 64), Entries: []Entry{{ID: "model", OperationalFields: map[string]json.RawMessage{field: b}}}},
		}
		if !collectOperationalCandidates(State{}, obs)["model"][field].conflict {
			t.Fatal("ordered conflict hidden", field)
		}
	}
	if sameOperationalValue("reference_prompt", json.RawMessage(`{"types":["image","video"],"positions":[1,2]}`), json.RawMessage(`{"types":["video","image"],"positions":[2,1]}`)) {
		t.Fatal("nested ordered positions were sorted")
	}
}
