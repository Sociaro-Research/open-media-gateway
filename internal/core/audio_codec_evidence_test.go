package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

func codecCount(n int) *int { return &n }
func TestAudioCodecEvidenceRequiresCompleteKnownInventory(t *testing.T) {
	for _, tc := range []struct {
		count  *int
		codecs []string
		ok     bool
	}{{nil, nil, false}, {codecCount(0), nil, true}, {codecCount(1), nil, false}, {codecCount(1), []string{""}, false}, {codecCount(1), []string{"aac"}, true}, {codecCount(2), []string{"aac", "mp3"}, true}, {codecCount(2), []string{"aac"}, false}, {codecCount(1), []string{"opus"}, false}, {codecCount(-1), nil, false}} {
		if got := AudioCodecsAllowed(tc.count, tc.codecs, []string{"aac", "mp3"}); got != tc.ok {
			t.Fatal(tc, got)
		}
		if !AudioCodecsAllowed(tc.count, tc.codecs, nil) {
			t.Fatal("undeclared constraint changed existing support")
		}
	}
}
func TestAudioCodecEvidenceValueEqualityAndDeepCloning(t *testing.T) {
	a := InputMediaEvidence{AudioStreams: codecCount(2), VideoStreams: codecCount(1), AudioCodecs: []string{"aac", "mp3"}, Type: "video"}
	b := CloneInputMediaEvidence(map[string]InputMediaEvidence{"x": a})["x"]
	if !EqualInputMediaEvidence(a, b) || a.AudioStreams == b.AudioStreams {
		t.Fatal("equal count values compared by address")
	}
	b.AudioCodecs[0] = "opus"
	*b.AudioStreams = 7
	if a.AudioCodecs[0] != "aac" || *a.AudioStreams != 2 || EqualInputMediaEvidence(a, b) {
		t.Fatal("caller evidence mutated")
	}
	b = a
	b.AudioCodecs = []string{"mp3", "aac"}
	if EqualInputMediaEvidence(a, b) {
		t.Fatal("stream order ignored")
	}
	if EqualInputMediaEvidence(InputMediaEvidence{}, InputMediaEvidence{AudioStreams: codecCount(0)}) {
		t.Fatal("unknown inventory became silence")
	}
}
func TestAudioCodecsPersistAndCloneResourceInspection(t *testing.T) {
	duration := 4.0
	source := ResourceSourceInput{ReferenceID: "owned", MediaType: "video", Duration: &duration, AudioStreams: codecCount(2), AudioCodecs: []string{"aac", "mp3"}}
	creation := CloneResourceCreation(&ResourceCreateRequest{FileSources: []ResourceSourceInput{source}})
	creation.FileSources[0].AudioCodecs[0] = "opus"
	if source.AudioCodecs[0] != "aac" {
		t.Fatal("resource clone aliased codec list")
	}
	evidence := InspectedResourceSource(source)
	cloned := CloneResourceSources([]ResourceSourceEvidence{evidence})
	cloned[0].Inspection.AudioCodecs[1] = "opus"
	if evidence.Inspection.AudioCodecs[1] != "mp3" {
		t.Fatal("persisted source clone aliased")
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var reopened ResourceSourceEvidence
	if err = json.Unmarshal(raw, &reopened); err != nil || !EqualInputMediaEvidence(*evidence.Inspection, *reopened.Inspection) {
		t.Fatal("source JSON lost codec inventory", reopened, err)
	}
	limits := (&ReferenceMediaLimits{Video: MediaFileLimits{AllowedAudioCodecs: []string{"aac", "mp3"}}}).Clone()
	copy := limits.Clone()
	copy.Video.AllowedAudioCodecs[0] = "opus"
	if !reflect.DeepEqual(limits.Video.AllowedAudioCodecs, []string{"aac", "mp3"}) {
		t.Fatal("limits clone aliased")
	}
}
