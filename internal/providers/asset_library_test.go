package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func TestSavedManualModelsInheritPromptSyntaxWithoutIdentityChange(t *testing.T) {
	m, _, _ := fixtureConnection(t)
	for _, tc := range []struct{ provider, profile, syntax, types string }{{"bytedance", "dreamina-seedance-2-0-260128", "numbered", "image,video,audio"}, {"alibaba", "wan2.7-r2v", "numbered", "image,video"}, {"agnes", "agnes-video-2.5", "picture", "image,audio,video"}, {"agnes", "agnes-video-2.5-flash", "picture", "image,audio"}, {"alibaba", "happyhorse-1.1-r2v", "bracket", "image"}, {"alibaba", "happyhorse-1.1-i2v", "", ""}, {"alibaba", "happyhorse-1.0-r2v", "", ""}} {
		d, ok := m.findSpec(tc.provider)
		if !ok {
			t.Fatal(tc.provider)
		}
		c := Credentials{APIKey: "fixture", BaseURL: "https://api.example", Connection: &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{{Profile: tc.profile, Model: core.MediaModel{ID: "private-model-alias", MediaType: "video"}}}}}
		before := connectionIdentity("fixture", d, c)
		a := connectionAdapter{credentials: c, definition: d}
		models, err := a.ListModels(context.Background())
		if err != nil || len(models) != 1 || models[0].ReferencePromptSyntax != tc.syntax || models[0].ID != "private-model-alias" || strings.Join(models[0].ReferencePromptTypes, ",") != tc.types {
			t.Fatal(tc, models, err)
		}
		if len(c.Connection.Models[0].Model.ReferencePromptTypes) != 0 || c.Connection.Models[0].Model.ReferencePromptSyntax != "" || connectionIdentity("fixture", d, c) != before {
			t.Fatal("saved connection rewritten")
		}
	}
}

func TestManualAliasesInheritReviewedLimitsAndRemainTemplateEligible(t *testing.T) {
	m, _, _ := fixtureConnection(t)
	for _, tc := range []struct{ provider, profile string }{{"bytedance", "dreamina-seedance-2-0-260128"}, {"agnes", "agnes-video-2.5"}, {"agnes", "agnes-video-2.5-flash"}} {
		t.Run(tc.provider+tc.profile, func(t *testing.T) {
			definition, ok := m.findSpec(tc.provider)
			if !ok {
				t.Fatal(tc.provider)
			}
			var native core.MediaModel
			for _, profile := range definition.Profiles() {
				if profile.ID == tc.profile {
					native = profile
				}
			}
			if native.ID == "" || native.ReferenceMediaLimits == nil {
				t.Fatal("missing reviewed profile", tc)
			}
			saved := native
			saved.ID = "operator-chosen-alias"
			saved.DisplayName = "My custom model label"
			saved.ReferencePromptSyntax = ""
			saved.ReferencePromptTypes = nil
			saved.ReferenceMediaLimits = nil
			credentials := Credentials{APIKey: "fixture", BaseURL: "https://api.example", Connection: &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{{Profile: tc.profile, Model: saved}}}}
			identityBefore := connectionIdentity("fixture", definition, credentials)
			adapter := connectionAdapter{credentials: credentials, definition: definition}
			models, err := adapter.ListModels(context.Background())
			if err != nil || len(models) != 1 {
				t.Fatal(models, err)
			}
			candidate := models[0]
			prompt := "Image 1"
			if candidate.ReferencePromptSyntax == "picture" {
				prompt = "<Picture 1>"
			}
			recipe := templates.Definition{Title: "Alias", MediaType: "video", Prompt: prompt, Syntax: candidate.ReferencePromptSyntax, Options: core.MediaOptions{ReferenceMode: "references"}}
			mode, _ := templates.Mode(candidate, "references")
			recipe.Slots, err = templates.Infer(prompt, recipe.Syntax, candidate.ReferencePromptTypes, mode)
			if err != nil {
				t.Fatal(err)
			}
			if err = recipe.Compatible(candidate); err != nil {
				t.Fatal("same reviewed contract excluded under manual alias", err)
			}
			if credentials.Connection.Models[0].Model.ReferenceMediaLimits != nil || connectionIdentity("fixture", definition, credentials) != identityBefore {
				t.Fatal("saved configuration or connection identity changed")
			}
			candidate.ReferenceMediaLimits.Image.ContentTypes[0] = "mutation"
			again, err := adapter.ListModels(context.Background())
			if err != nil || again[0].ReferenceMediaLimits.Image.ContentTypes[0] == "mutation" {
				t.Fatal("returned metadata aliases another snapshot", err)
			}
			explicit := native.ReferenceMediaLimits.Clone()
			explicit.Image.MaxBytes = 12345
			credentials.Connection.Models[0].Model.ReferenceMediaLimits = explicit
			again, err = adapter.ListModels(context.Background())
			if err != nil || again[0].ReferenceMediaLimits.Image.MaxBytes != 12345 {
				t.Fatal("explicit operator metadata overwritten", err)
			}
		})
	}
}
