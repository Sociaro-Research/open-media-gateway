package branding

import (
	"maps"
	"reflect"
	"testing"
)

func TestLegacyPresetColorsSurviveDefaultChange(t *testing.T) {
	legacy := Defaults()
	legacy.Dark["accent"] = "#b8dc00"
	legacy.Light["switch-off"] = "#eef7e0"
	dark, light := maps.Clone(legacy.Dark), maps.Clone(legacy.Light)
	s := legacy.Normalize()
	if len(s.Presets) != 1 || s.DefaultPreset != "original" || s.Presets[0].Name != "Original" {
		t.Fatal(s)
	}
	for key, value := range dark {
		if s.Presets[0].Dark[key] != value {
			t.Fatal("original dark color changed", key)
		}
	}
	for key, value := range light {
		if s.Presets[0].Light[key] != value {
			t.Fatal("original light color changed", key)
		}
	}
	if s.Presets[0].Dark["switch-thumb"] != dark["on-accent"] {
		t.Fatal("legacy thumb changed")
	}
	custom := testCustomPreset()
	custom.Light["switch-off"] = "#66758a"
	s.Presets = append(s.Presets, custom)
	s.DefaultPreset = custom.ID
	s = s.Normalize()
	if s.Light["switch-off"] != "#66758a" || s.Presets[0].Light["switch-off"] != "#eef7e0" {
		t.Fatal("switching erased original", s)
	}
	s.Light["text"] = "#000001"
	if s.Presets[0].Light["text"] != light["text"] {
		t.Fatal("presets share mutable palettes")
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestColorPresetValidation(t *testing.T) {
	for _, mutate := range []func(*Settings){
		func(s *Settings) { s.Presets = []Preset{} },
		func(s *Settings) { s.DefaultPreset = "missing" },
		func(s *Settings) { s.Presets[1].ID = s.Presets[0].ID },
		func(s *Settings) { s.Presets[1].Name = "original" },
		func(s *Settings) { s.Presets[1].Name = " whitespace " },
		func(s *Settings) { s.Presets[1].ID = "bad;style" },
		func(s *Settings) { s.Presets[1].Light["switch-thumb"] = "url(secret)" },
		func(s *Settings) { delete(s.Presets[1].Dark, "text") },
		func(s *Settings) { s.Presets[1].Light = nil },
	} {
		s := Defaults().Normalize()
		s.Presets = append(s.Presets, testCustomPreset())
		mutate(&s)
		if s.Validate() == nil {
			t.Fatal("invalid preset accepted", s)
		}
	}
	if err := Defaults().Normalize().Validate(); err != nil {
		t.Fatal(err)
	}
}

func testCustomPreset() Preset {
	s := Defaults().Normalize()
	return Preset{ID: "custom", Name: "Custom", Dark: s.Dark, Light: s.Light}
}

func TestDefaultCatalogContainsOnlyOriginal(t *testing.T) {
	s := Defaults().Normalize()
	if len(s.Presets) != 1 || s.DefaultPreset != "original" || s.Presets[0].ID != "original" || s.Presets[0].Name != "Original" {
		t.Fatal("unexpected bundled catalog", s)
	}
	if s.HasPreset("clear-lime") || !s.HasPreset("original") || !s.HasPreset("") {
		t.Fatal("unexpected default selections")
	}
	if !reflect.DeepEqual(s, s.Normalize()) {
		t.Fatal("normalization changed defaults")
	}
}

func TestSavedCatalogRetainsExistingThemesAndDefault(t *testing.T) {
	for _, id := range []string{"clear-lime", "administrator-theme"} {
		t.Run(id, func(t *testing.T) {
			s := Defaults().Normalize()
			p := testCustomPreset()
			p.ID, p.Name, p.Dark["accent"] = id, "Saved theme", "#123456"
			s.Presets = append(s.Presets, p)
			s.DefaultPreset = id
			normalized := s.Normalize()
			if !reflect.DeepEqual(normalized.Presets, s.Presets) || normalized.DefaultPreset != id || normalized.Dark["accent"] != "#123456" || !normalized.HasPreset(id) {
				t.Fatal("saved catalog changed", normalized)
			}
			if err := normalized.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
