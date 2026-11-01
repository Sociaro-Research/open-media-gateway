package catalogsync

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"testing"
	"time"
)

func TestReferenceRecommendationPublicationIsOptionalAndEvidenceBound(t *testing.T) {
	model := completeImageModel()
	model.ReferencePromptRecommendation = &core.ReferencePromptRecommendation{Text: "Reviewed API", Kind: "instruction", SourceURL: "https://api.example.test/reference", Modes: []string{"references"}}
	parsed := core.ReferencePromptRecommendation{Text: "The endpoint request example uses @Image1.", Kind: "example", SourceURL: model.ReferencePromptRecommendation.SourceURL, Modes: []string{"references"}}
	ob := structuredContract(model)
	ob.Source.URL = model.ReferencePromptRecommendation.SourceURL
	ob.Entries[0].OperationalFields = map[string]json.RawMessage{"reference_prompt_recommendation": encoded(parsed)}
	capture := Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}}
	state, run := Reconcile(State{}, capture, []Observation{ob}, time.Now(), accepted)
	if run.Completeness.Status != "complete" || string(state.Models[model.ID].Metadata.Operational["reference_prompt_recommendation"].Value) != string(encoded(parsed)) {
		t.Fatalf("advice not published: %+v %+v", state.Models[model.ID], run)
	}
	// Missing advice is neither admission failure nor mandatory completeness failure.
	missing := ob
	missing.Entries = []Entry{{ID: model.ID, Contract: &model}}
	next, run := Reconcile(state, capture, []Observation{missing}, time.Now().Add(time.Hour), accepted)
	if run.Completeness.Status != "complete" || string(next.Models[model.ID].Metadata.Operational["reference_prompt_recommendation"].Value) != string(encoded(parsed)) {
		t.Fatal("missing optional advice altered contract", run)
	}
	foreign := ob
	foreign.Source.URL = "https://unreviewed.example.test/catalogue"
	badSource, _ := Reconcile(State{}, capture, []Observation{foreign}, time.Now(), accepted)
	if badSource.Models[model.ID].Metadata != nil && len(badSource.Models[model.ID].Metadata.Operational) != 0 {
		t.Fatal("copied hint URL impersonated actual source")
	}
	without := model
	without.ReferencePromptRecommendation = nil
	if !sameJSON(encoded(contract(model)), encoded(contract(without))) {
		t.Fatal("optional hint changed executable contract")
	}

	parsed.SourceURL = "https://unreviewed.example.test"
	ob.Entries[0].OperationalFields["reference_prompt_recommendation"] = encoded(parsed)
	bad, _ := Reconcile(State{}, capture, []Observation{ob}, time.Now(), accepted)
	if bad.Models[model.ID].Metadata != nil && len(bad.Models[model.ID].Metadata.Operational) != 0 {
		t.Fatal("foreign endpoint advice published")
	}
}
