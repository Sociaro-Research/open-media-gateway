package providers

import (
	"fmt"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

func catalogFileMediaModel() core.MediaModel {
	return core.MediaModel{ID: "reviewed-file-media", ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "file_resource", Subtypes: []string{"image", "video", "audio", "regeneration_source"}, MaxCount: 8}, {Kind: "voice", MaxCount: 1}, {Kind: "element", Subtypes: []string{"video_character"}, MaxCount: 1}}, ReferenceRoleSchemas: []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"reference", "person"}, Default: "reference"}, {Kind: "video", Roles: []string{"reference", "base_video"}, Default: "reference"}, {Kind: "audio", Roles: []string{"reference", "background"}, Default: "reference"}}}
}
func catalogMediaResource(kind, subtype, alias string) core.MediaOptions {
	id := uuid.NewString()
	sourceKind := subtype
	if kind == "voice" {
		sourceKind = "audio"
	} else if kind == "element" || subtype == "regeneration_source" {
		sourceKind = "video"
	}
	return core.MediaOptions{ResourceReferences: []core.ResourceReference{{BindingID: id, ReferenceID: alias}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: id, ReferenceID: alias, Kind: kind, Subtype: subtype, RemoteID: "private-native", ConnectionIdentity: "trusted-revision", Sources: []core.ResourceSourceEvidence{{MediaType: sourceKind}}}}}
}
func catalogMediaBase() core.TaskCost {
	return core.TaskCost{EstimatedUnits: 1, ExpectedUSD: 10000, Pricing: core.PricingSnapshot{Currency: "USD", Region: "Global", Unit: "second", Price: 10000, Per: 1, Version: "baseline"}}
}
func catalogMediaRate() catalogsync.Rate {
	return catalogsync.Rate{Region: "Global", Unit: "second", Price: 100000, Per: 1}
}
func catalogMediaApply(t *testing.T, model core.MediaModel, o core.MediaOptions, rate catalogsync.Rate, wantApplied bool) {
	t.Helper()
	base := catalogMediaBase()
	state := catalogsync.State{Version: 7, Models: map[string]catalogsync.Entry{model.ID: {ID: model.ID, Rates: []catalogsync.Rate{rate}}}}
	got, err := applyCatalogQuote(base, nil, model, core.MediaRequest{Model: model.ID, Options: o}, state)
	if err != nil {
		t.Fatal(err)
	}
	if wantApplied {
		if got.ExpectedUSD != 100000 || got.Pricing.Price != 100000 || got.Pricing.Version != "catalog-7/baseline" {
			t.Fatal("valid media transport lost refreshed tariff", got)
		}
	} else if got.ExpectedUSD != base.ExpectedUSD || got.Pricing.Price != base.Pricing.Price || got.Pricing.Version != base.Pricing.Version {
		t.Fatal("ineligible request changed base quote", got)
	}
}

func TestCatalogFileMediaTransportMatchesURLPresenceAndRoles(t *testing.T) {
	m := catalogFileMediaModel()
	for _, kind := range []string{"image", "video", "audio"} {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/file=%t", kind, file), func(t *testing.T) {
				o := core.MediaOptions{ReferenceRoles: map[string]string{kind + "_1": "reference"}}
				if file {
					o = catalogMediaResource("file_resource", kind, kind+"_1")
				} else {
					switch kind {
					case "image":
						o.ImageURLs = []string{"https://media.example/image.png"}
					case "video":
						o.VideoURL = "https://media.example/video.mp4"
					case "audio":
						o.AudioURL = "https://media.example/audio.wav"
					}
				}
				rate := catalogMediaRate()
				yes := true
				switch kind {
				case "image":
					rate.ImageInput = &yes
				case "video":
					rate.VideoInput = &yes
				case "audio":
					rate.AudioInput = &yes
				}
				rate.ReferenceRoles = []catalogsync.RoleCountSelector{{Kind: kind, Role: "reference", Min: 1, Max: 1}}
				catalogMediaApply(t, m, o, rate, true)
			})
		}
	}
}

func TestCatalogFileAudioRequiresExplicitAudioTariff(t *testing.T) {
	m := catalogFileMediaModel()
	o := catalogMediaResource("file_resource", "audio", "audio_1")
	rate := catalogMediaRate()
	catalogMediaApply(t, m, o, rate, false)
	no := false
	rate.AudioInput = &no
	catalogMediaApply(t, m, o, rate, false)
	yes := true
	rate.AudioInput = &yes
	catalogMediaApply(t, m, o, rate, true)
}

func TestCatalogFileVideoIsTransportButVideoElementStillRequiresOptIn(t *testing.T) {
	m := catalogFileMediaModel()
	yes, no := true, false
	file := catalogMediaResource("file_resource", "video", "video_1")
	rate := catalogMediaRate()
	catalogMediaApply(t, m, file, rate, false)
	rate.VideoInput = &no
	catalogMediaApply(t, m, file, rate, false)
	rate.VideoInput = &yes
	catalogMediaApply(t, m, file, rate, true)
	element := catalogMediaResource("element", "video_character", "element_1")
	catalogMediaApply(t, m, element, rate, false)
	rate.VideoInputIncludesResources = true
	catalogMediaApply(t, m, element, rate, true)
	// A protected regeneration source is not an ordinary video file transport.
	source := catalogMediaResource("file_resource", "regeneration_source", "owned_source")
	catalogMediaApply(t, m, source, rate, false)
}

func TestCatalogVoicesAndElementsDoNotBecomeAudioOrImageInputs(t *testing.T) {
	m := catalogFileMediaModel()
	yes, no := true, false
	for _, o := range []core.MediaOptions{catalogMediaResource("voice", "", "voice_1"), catalogMediaResource("element", "video_character", "element_1")} {
		rate := catalogMediaRate()
		rate.ImageInput, rate.AudioInput = &no, &no
		catalogMediaApply(t, m, o, rate, true)
		rate.ImageInput = &yes
		catalogMediaApply(t, m, o, rate, false)
		rate.ImageInput, rate.AudioInput = &no, &yes
		catalogMediaApply(t, m, o, rate, false)
	}
}

func TestCatalogInvalidFileCountsPreserveBaseAndDurationSelectors(t *testing.T) {
	for _, invalid := range []string{"unpaired", "position", "media kind", "unreviewed"} {
		t.Run(invalid, func(t *testing.T) {
			m := catalogFileMediaModel()
			o := catalogMediaResource("file_resource", "video", "video_1")
			switch invalid {
			case "unpaired":
				o.ResourceReferences = nil
			case "position":
				o.ResourceReferences[0].ReferenceID, o.ResourceSnapshots[0].ReferenceID = "video_2", "video_2"
			case "media kind":
				o.ResourceSnapshots[0].Sources[0].MediaType = "image"
			case "unreviewed":
				m.ResourceInputSchemas[0].Subtypes = nil
			}
			catalogMediaApply(t, m, o, catalogMediaRate(), false)
		})
	}
	m := catalogFileMediaModel()
	o := catalogMediaResource("file_resource", "video", "video_1")
	o.Duration = 5
	yes := true
	rate := catalogMediaRate()
	rate.VideoInput, rate.Duration = &yes, 6
	catalogMediaApply(t, m, o, rate, false)
	rate.Duration = 5
	catalogMediaApply(t, m, o, rate, true)
}
