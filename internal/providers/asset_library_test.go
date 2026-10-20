package providers

import (
	"context"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
)

func TestSavedManualModelsInheritPromptSyntaxWithoutIdentityChange(t *testing.T) {
	m, _, _ := fixtureConnection(t)
	for _, tc := range []struct{ provider, profile, syntax string }{{"bytedance", "dreamina-seedance-2-0-260128", "numbered"}, {"alibaba", "wan2.7-r2v", "numbered"}, {"agnes", "agnes-video-2.5", "picture"}} {
		d, ok := m.findSpec(tc.provider)
		if !ok {
			t.Fatal(tc.provider)
		}
		c := Credentials{APIKey: "fixture", BaseURL: "https://api.example", Connection: &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{{Profile: tc.profile, Model: core.MediaModel{ID: "private-model-alias", MediaType: "video"}}}}}
		before := connectionIdentity("fixture", d, c)
		a := connectionAdapter{credentials: c, definition: d}
		models, err := a.ListModels(context.Background())
		if err != nil || len(models) != 1 || models[0].ReferencePromptSyntax != tc.syntax || models[0].ID != "private-model-alias" {
			t.Fatal(tc, models, err)
		}
		if c.Connection.Models[0].Model.ReferencePromptSyntax != "" || connectionIdentity("fixture", d, c) != before {
			t.Fatal("saved connection rewritten")
		}
	}
}
