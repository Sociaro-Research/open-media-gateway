package catalogsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

func conditionFileModel() core.MediaModel {
	return core.MediaModel{ID: "reviewed-file-media", ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "file_resource", Subtypes: []string{"image", "video", "audio", "regeneration_source"}, MaxCount: 8}, {Kind: "voice", MaxCount: 1}, {Kind: "element", Subtypes: []string{"video_character"}, MaxCount: 1}}, ReferenceRoleSchemas: []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"reference", "person"}, Default: "reference"}, {Kind: "video", Roles: []string{"reference", "base_video"}, Default: "reference"}, {Kind: "audio", Roles: []string{"reference", "background"}, Default: "reference"}}}
}

func addConditionResource(o *core.MediaOptions, kind, subtype, alias string) {
	id := uuid.NewString()
	sourceKind := subtype
	if kind == "voice" {
		sourceKind = "audio"
	} else if kind == "element" || subtype == "regeneration_source" {
		sourceKind = "video"
	}
	o.ResourceReferences = append(o.ResourceReferences, core.ResourceReference{BindingID: id, ReferenceID: alias})
	o.ResourceSnapshots = append(o.ResourceSnapshots, core.ResourceSnapshot{BindingID: id, ReferenceID: alias, Kind: kind, Subtype: subtype, RemoteID: "private-native", ConnectionIdentity: "trusted-revision", Sources: []core.ResourceSourceEvidence{{MediaType: sourceKind}}})
}

func TestFileMediaConditionsMatchURLTransportAndPositionalRoles(t *testing.T) {
	m := conditionFileModel()
	yes := true
	rate := Rate{ImageInput: &yes, AudioInput: &yes, ReferenceRoles: []RoleCountSelector{{Kind: "image", Role: "person", Min: 1, Max: 1}, {Kind: "video", Role: "base_video", Min: 1, Max: 1}, {Kind: "audio", Role: "background", Min: 1, Max: 1}}}
	for _, files := range []bool{false, true} {
		t.Run(fmt.Sprintf("files=%t", files), func(t *testing.T) {
			o := core.MediaOptions{ReferenceRoles: map[string]string{"image_1": "person", "video_1": "base_video", "audio_1": "background"}}
			if files {
				for _, kind := range []string{"image", "video", "audio"} {
					addConditionResource(&o, "file_resource", kind, kind+"_1")
				}
			} else {
				o.ImageURLs = []string{"https://media.example/image.png"}
				o.VideoURL = "https://media.example/video.mp4"
				o.AudioURL = "https://media.example/audio.wav"
			}
			before, _ := json.Marshal(o)
			if !rate.MatchesConditions(m, core.MediaRequest{Options: o}) {
				t.Fatal("equivalent transport did not match explicit media/role selectors")
			}
			after, _ := json.Marshal(o)
			if !bytes.Equal(before, after) {
				t.Fatal("tariff matching mutated accepted media evidence")
			}
		})
	}
	// Reviewed files follow URL positions of the same kind; defaults and
	// explicit aliases retain the existing role semantics.
	o := core.MediaOptions{ImageURLs: []string{"https://media.example/first.png"}, ReferenceRoles: map[string]string{"image_2": "person"}}
	addConditionResource(&o, "file_resource", "image", "image_2")
	mixed := Rate{ImageInput: &yes, ReferenceRoles: []RoleCountSelector{{Kind: "image", Role: "reference", Min: 1, Max: 1}, {Kind: "image", Role: "person", Min: 1, Max: 1}}}
	if !mixed.MatchesConditions(m, core.MediaRequest{Options: o}) {
		t.Fatal("mixed URL/file aliases did not retain ordered roles")
	}
}

func TestFileMediaConditionsDoNotCountVoicesOrElementsAsOrdinaryMedia(t *testing.T) {
	m := conditionFileModel()
	o := core.MediaOptions{}
	addConditionResource(&o, "voice", "", "voice_1")
	addConditionResource(&o, "element", "video_character", "element_1")
	no, yes := false, true
	rate := Rate{ImageInput: &no, AudioInput: &no, ResourceInputs: []ResourceCountSelector{{Kind: "voice", Min: 1, Max: 1}, {Kind: "element", Subtype: "video_character", Min: 1, Max: 1}}, ReferenceRoles: []RoleCountSelector{{Kind: "image", Role: "reference", Max: 0}, {Kind: "video", Role: "reference", Max: 0}, {Kind: "audio", Role: "reference", Max: 0}}}
	if !rate.MatchesConditions(m, core.MediaRequest{Options: o}) {
		t.Fatal("typed resources were confused with media-array inputs")
	}
	rate.AudioInput = &yes
	if rate.MatchesConditions(m, core.MediaRequest{Options: o}) {
		t.Fatal("voice or element was counted as ordinary audio")
	}
	rate.AudioInput, rate.ImageInput = &no, &yes
	if rate.MatchesConditions(m, core.MediaRequest{Options: o}) {
		t.Fatal("voice or element was counted as ordinary image")
	}
}

func TestFileMediaConditionsRejectUnpairedUnreviewedOrMistypedEvidence(t *testing.T) {
	for _, mutation := range []string{"unpaired", "orphan snapshot", "remote identity", "revision", "position", "provenance type", "unreviewed subtype"} {
		t.Run(mutation, func(t *testing.T) {
			m := conditionFileModel()
			o := core.MediaOptions{}
			addConditionResource(&o, "file_resource", "image", "image_1")
			switch mutation {
			case "unpaired":
				o.ResourceSnapshots = nil
			case "orphan snapshot":
				o.ResourceReferences = nil
			case "remote identity":
				o.ResourceSnapshots[0].RemoteID = ""
			case "revision":
				o.ResourceSnapshots[0].ConnectionIdentity = ""
			case "position":
				o.ResourceReferences[0].ReferenceID, o.ResourceSnapshots[0].ReferenceID = "image_2", "image_2"
			case "provenance type":
				o.ResourceSnapshots[0].Sources[0].MediaType = "audio"
			case "unreviewed subtype":
				m.ResourceInputSchemas[0].Subtypes = nil
			}
			if (Rate{}).MatchesConditions(m, core.MediaRequest{Options: o}) {
				t.Fatal("invalid resource evidence matched even an unconditioned tariff")
			}
		})
	}
}
