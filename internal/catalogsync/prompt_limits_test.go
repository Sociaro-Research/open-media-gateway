package catalogsync

import (
	"bytes"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"testing"
	"time"
)

func TestPromptCharacterLimitsDoNotChangeExternalCatalogueContract(t *testing.T) {
	model := core.MediaModel{ID: "native", MediaType: "video"}
	before, _ := json.Marshal(contract(model))
	initial, _ := Reconcile(State{}, Capture{Models: []core.MediaModel{model}}, nil, time.Now(), nil)
	model.MaxPromptCharacters = 3072
	after, _ := json.Marshal(contract(model))
	if !bytes.Equal(before, after) {
		t.Fatal("local metadata changed external contract fingerprint")
	}
	next, run := Reconcile(initial, Capture{Models: []core.MediaModel{model}}, nil, time.Now(), nil)
	if next.Version != initial.Version || len(run.Changes) != 0 {
		t.Fatal("metadata created a catalogue revision", run.Changes)
	}
}
