package branding

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A preset changes colors only. Installation identity, images and layout are shared.
type Preset struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Dark  map[string]string `json:"dark"`
	Light map[string]string `json:"light"`
}

const MaxPresets = 32

var presetID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var roleFallbacks = map[string]string{
	"accent-text": "accent", "chart-1": "accent", "switch-thumb": "on-accent",
	"switch-thumb-on": "on-accent", "switch-on-border": "accent",
}

func ValidPresetID(id string) bool { return id == "" || presetID.MatchString(id) }

func completePalette(palette map[string]string) map[string]string {
	out := maps.Clone(palette)
	if out == nil {
		return nil
	}
	for role, source := range roleFallbacks {
		if _, ok := out[role]; !ok {
			out[role] = out[source]
		}
	}
	return out
}

// Normalize preserves legacy colors exactly, including legacy switch thumbs.
// The original pair remains a separate preset when the default is changed later.
func (s Settings) Normalize() Settings {
	if s.Presets == nil {
		s.Presets = []Preset{{ID: "original", Name: "Original", Dark: s.Dark, Light: s.Light}, ClearLime()}
		s.DefaultPreset = "original"
	}
	presets := make([]Preset, len(s.Presets))
	for i, p := range s.Presets {
		p.Dark = completePalette(p.Dark)
		p.Light = completePalette(p.Light)
		presets[i] = p
	}
	s.Presets = presets
	for _, p := range s.Presets {
		if p.ID == s.DefaultPreset {
			s.Dark = p.Dark
			s.Light = p.Light
			break
		}
	}
	return s
}

func (s Settings) HasPreset(id string) bool {
	if id == "" {
		return true
	}
	for _, p := range s.Normalize().Presets {
		if p.ID == id {
			return true
		}
	}
	return false
}

func validatePalette(palette map[string]string) error {
	known := make(map[string]bool, len(Colors))
	for _, c := range Colors {
		known[c.Key] = true
		value, exists := palette[c.Key]
		if !exists {
			if _, optional := roleFallbacks[c.Key]; optional {
				continue
			}
		}
		if !hexColor.MatchString(value) {
			return fmt.Errorf("%s must be a six-digit hex color", c.Label)
		}
	}
	for key := range palette {
		if !known[key] {
			return errors.New("unknown palette color")
		}
	}
	return nil
}

func (s Settings) validatePresets() error {
	if s.Presets == nil {
		if s.DefaultPreset != "" {
			return errors.New("default color theme requires a preset catalog")
		}
		return nil
	}
	if len(s.Presets) == 0 || len(s.Presets) > MaxPresets {
		return errors.New("provide between 1 and 32 color themes")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, p := range s.Presets {
		if p.ID == "" || !ValidPresetID(p.ID) || ids[p.ID] {
			return errors.New("color theme IDs must be unique and contain at most 64 letters, digits, underscores or hyphens")
		}
		name := strings.TrimSpace(p.Name)
		if name == "" || name != p.Name || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.ContainsFunc(name, unicode.IsControl) || names[strings.ToLower(name)] {
			return errors.New("color theme names must be unique and contain 1–80 characters without surrounding whitespace or control characters")
		}
		ids[p.ID] = true
		names[strings.ToLower(name)] = true
		for _, palette := range []map[string]string{p.Dark, p.Light} {
			if err := validatePalette(palette); err != nil {
				return fmt.Errorf("%s: %w", p.Name, err)
			}
		}
	}
	if !ids[s.DefaultPreset] {
		return errors.New("choose an existing default color theme")
	}
	return nil
}

// ClearLime is an editable starting point, never an override of the original colors.
func ClearLime() Preset {
	base := Defaults()
	dark := map[string]string{"bg": "#080b10", "panel": "#121922", "panel-2": "#1b2532", "field": "#0d131c", "popup": "#1b2532", "line": "#334153", "text": "#edf2f7", "muted": "#aab4c2", "accent": "#b8dc00", "accent-dark": "#1d2812", "highlight": "#c5eb00", "on-accent": "#182000", "on-highlight": "#182000", "on-warning": "#11151b", "switch-off": "#66758a", "accent-text": "#b8dc00", "chart-1": "#b8dc00", "switch-thumb": "#ffffff", "switch-thumb-on": "#182000", "switch-on-border": "#b8dc00"}
	light := map[string]string{"bg": "#f5f7fa", "panel-2": "#edf1f5", "line": "#d2d9e2", "text": "#202938", "muted": "#596575", "accent": "#b8dc00", "accent-dark": "#f4f8e7", "highlight": "#526b00", "on-accent": "#1a210e", "on-highlight": "#ffffff", "switch-off": "#66758a", "accent-text": "#526b00", "chart-1": "#668700", "switch-thumb": "#ffffff", "switch-thumb-on": "#1a210e", "switch-on-border": "#b8dc00", "chart-2": "#157e96", "chart-3": "#7d50bd", "chart-4": "#af6b1b", "chart-5": "#287b57", "chart-6": "#b14f7a"}
	maps.Copy(base.Dark, dark)
	maps.Copy(base.Light, light)
	return Preset{ID: "clear-lime", Name: "Clear Lime", Dark: base.Dark, Light: base.Light}
}
