package providerassets

import (
	"github.com/Sociaro-Research/media-gateway-dev/internal/catalogsync"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
	"net/http"
	"testing"
)

func TestRegistryRejectsAmbiguousComposition(t *testing.T) {
	d := providerkit.Definition{ID: "fixture", Name: "Fixture", Factory: func(providerkit.Credentials, providerkit.Settings, *http.Client) (core.MediaProvider, error) {
		return nil, nil
	}}
	if _, err := NewRegistry(d, d); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	d.Aliases = []string{"fixture"}
	if _, err := NewRegistry(d); err == nil {
		t.Fatal("colliding alias accepted")
	}
	d.Aliases = nil
	d.Fields = []providerkit.Field{{Key: "enabled"}}
	if _, err := NewRegistry(d); err == nil {
		t.Fatal("reserved update field accepted")
	}
}
func TestModuleAssetsAndAliases(t *testing.T) {
	if Canonical("dashscope") != "alibaba" {
		t.Fatal("legacy alias lost")
	}
	for _, d := range All() {
		if d.IconType == "image/png" {
			b, ok := Icon(d.ID)
			if !ok || len(b) == 0 {
				t.Fatal("raster asset missing")
			}
		}
		if d.IconType == "image/svg+xml" {
			b, ok := SVGIcon(d.ID)
			if !ok || len(b) == 0 {
				t.Fatal("vector asset missing")
			}
		}
	}
}

func TestPaidModulesRegisterCatalogueSources(t *testing.T) {
	for _, d := range All() {
		if d.Free {
			continue
		}
		if len(d.CatalogSources) == 0 {
			t.Fatal("provider must declare catalogue sources", d.ID)
		}
		schedule := catalogsync.Schedule{Provider: d.ID, Time: "03:00", Version: 1, Sources: d.CatalogSources}
		if err := schedule.Validate(); err != nil {
			t.Fatal("invalid default source registration", d.ID, err)
		}
	}
}
