package providers

import (
	"context"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func TestSavedWanVoiceInheritsOnlyMissingNativeConstraint(t *testing.T) {
	manager, _, _ := fixtureConnection(t)
	definition, ok := manager.findSpec("alibaba")
	if !ok {
		t.Fatal("alibaba")
	}
	var profile core.MediaModel
	for _, candidate := range definition.Profiles() {
		if candidate.ReferenceMediaLimits != nil && candidate.ReferenceMediaLimits.ReferenceVoice != nil {
			profile = candidate
			break
		}
	}
	if profile.ID == "" {
		t.Fatal("missing native reference voice profile")
	}
	for _, explicit := range []bool{false, true} {
		saved := profile
		saved.ID = "operator-alias"
		saved.ReferenceMediaLimits = profile.ReferenceMediaLimits.Clone()
		saved.ReferenceMediaLimits.Image.MaxBytes = 12345
		saved.ReferenceMediaLimits.ReferenceVoice = nil
		if explicit {
			saved.ReferenceMediaLimits.ReferenceVoice = &core.MediaFileLimits{MaxDuration: 7}
		}
		credentials := Credentials{APIKey: "synthetic", Connection: &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{{Profile: profile.ID, Model: saved}}}}
		before := connectionIdentity("fixture", definition, credentials)
		adapter := connectionAdapter{definition: definition, credentials: credentials}
		models, err := adapter.ListModels(context.Background())
		if err != nil || len(models) != 1 {
			t.Fatal(models, err)
		}
		want := profile.ReferenceMediaLimits.ReferenceVoice.MaxDuration
		if explicit {
			want = 7
		}
		if models[0].ReferenceMediaLimits.ReferenceVoice.MaxDuration != want || models[0].ReferenceMediaLimits.Image.MaxBytes != 12345 {
			t.Fatal("native/default or explicit constraints lost", models)
		}
		models[0].ReferenceMediaLimits.ReferenceVoice.MaxDuration = 999
		again, err := adapter.ListModels(context.Background())
		if err != nil || again[0].ReferenceMediaLimits.ReferenceVoice.MaxDuration != want {
			t.Fatal("read-time metadata leaked", again, err)
		}
		if connectionIdentity("fixture", definition, credentials) != before {
			t.Fatal("saved connection identity changed")
		}
		if !explicit && credentials.Connection.Models[0].Model.ReferenceMediaLimits.ReferenceVoice != nil {
			t.Fatal("saved configuration mutated")
		}
	}
}
