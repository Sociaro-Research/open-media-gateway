package catalogsync

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationalHistoryRetainsExactMissingFieldSnapshot(t *testing.T) {
	source := Source{ID: "api-document", URL: "https://docs.example.test/video", Parser: "document"}
	old := Observation{Source: source, Hash: strings.Repeat("a", 64), Entries: []Entry{{ID: "model", OperationalFields: map[string]json.RawMessage{"output.duration": encoded(map[string]int{"min": 2, "max": 15})}}}}
	missing := Observation{Source: source, Hash: strings.Repeat("b", 64), Entries: []Entry{{ID: "model"}}}
	for i := 0; i < 3; i++ {
		missing.OperationalHistory = rememberOperationalHistory(old, missing)
		fact := missing.OperationalHistory["model"]["output.duration"]
		if fact.Hash != strings.Repeat("a", 64) || fact.Source.URL != source.URL {
			t.Fatal("omitted field relabeled as current source snapshot", fact)
		}
		previous := State{Observations: map[string]Observation{source.ID: missing}}
		candidate := collectOperationalCandidates(previous, []Observation{missing})["model"]["output.duration"]
		if candidate.current || len(candidate.refs) != 1 || candidate.refs[0].LastConfirmedHash != fact.Hash {
			t.Fatal("historical fact became current", candidate)
		}
		old = missing
	}
	// Fetched observations cannot inject read-only history as fresh authority.
	injected := Observation{Source: source, Hash: strings.Repeat("c", 64), OperationalHistory: old.OperationalHistory}
	if result := rememberOperationalHistory(Observation{}, injected); result != nil {
		t.Fatal("fetched history trusted", result)
	}
	if result := collectOperationalCandidates(State{}, []Observation{injected}); len(result) != 0 {
		t.Fatal("injected history supplied verification", result)
	}
}
