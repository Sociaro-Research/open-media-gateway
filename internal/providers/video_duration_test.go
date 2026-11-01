package providers

import (
	"context"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func TestSavedWan3InheritsCombinedDurationWithoutIdentityChange(t *testing.T) {
	m, _, _ := fixtureConnection(t)
	definition, ok := m.findSpec("alibaba")
	if !ok {
		t.Fatal("alibaba")
	}
	count := 0
	for _, profile := range definition.Profiles() {
		if profile.ID != "wan3.0-video" && profile.ID != "wan3.0-video-prime" {
			continue
		}
		count++
		for _, withLegacyLimits := range []bool{false, true} {
			saved := profile
			saved.ID = "operator-alias"
			saved.ReferenceMediaLimits = nil
			if withLegacyLimits {
				saved.ReferenceMediaLimits = profile.ReferenceMediaLimits.Clone()
				saved.ReferenceMediaLimits.MaxVideoAndOutputDuration = 0
				saved.ReferenceMediaLimits.Image.MaxBytes = 12345
			}
			credentials := Credentials{APIKey: "synthetic", Connection: &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{{Profile: profile.ID, Model: saved}}}}
			before := connectionIdentity("fixture", definition, credentials)
			adapter := connectionAdapter{credentials: credentials, definition: definition}
			models, err := adapter.ListModels(context.Background())
			if err != nil || len(models) != 1 || models[0].ReferenceMediaLimits.MaxVideoAndOutputDuration != 30 {
				t.Fatal("native combined constraint lost", models, err)
			}
			if withLegacyLimits && models[0].ReferenceMediaLimits.Image.MaxBytes != 12345 {
				t.Fatal("explicit file metadata changed")
			}
			stored := credentials.Connection.Models[0].Model
			if stored.ReferenceMediaLimits != nil && stored.ReferenceMediaLimits.MaxVideoAndOutputDuration != 0 || connectionIdentity("fixture", definition, credentials) != before {
				t.Fatal("saved configuration/identity changed")
			}
			models[0].ReferenceMediaLimits.MaxVideoAndOutputDuration = 1
			again, err := adapter.ListModels(context.Background())
			if err != nil || again[0].ReferenceMediaLimits.MaxVideoAndOutputDuration != 30 {
				t.Fatal("read-time metadata leaked", again, err)
			}
		}
	}
	if count != 2 {
		t.Fatalf("expected both Wan 3 variants, got %d", count)
	}
	// Unknown persisted profiles stay stored but are not executable.
	adapter := connectionAdapter{definition: definition, credentials: Credentials{Connection: &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{{Profile: "unknown", Model: core.MediaModel{ID: "custom"}}}}}}
	beforeUnknown := connectionIdentity("fixture", definition, adapter.credentials)
	models, err := adapter.ListModels(context.Background())
	if connectionIdentity("fixture", definition, adapter.credentials) != beforeUnknown || len(adapter.credentials.Connection.Models) != 1 || adapter.credentials.Connection.Models[0].Profile != "unknown" || adapter.credentials.Connection.Models[0].Model.ReferenceMediaLimits != nil {
		t.Fatal("unknown saved profile/configuration was mutated")
	}
	if err != nil || len(models) != 0 {
		t.Fatal("unknown profile changed", models, err)
	}
}
