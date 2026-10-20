// Package branding defines installation-wide appearance without executable content.
package branding

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxAssetBytes = 512 * 1024
const MaxRequestBytes = 6 * 1024 * 1024

type Reader interface {
	Branding(context.Context) (Settings, error)
}
type Store interface {
	Reader
	SaveBranding(context.Context, string, Settings) (Settings, error)
}
type Asset struct {
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}
type Settings struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	ContactEmail       string            `json:"contact_email"`
	SiteMaxWidth       *int              `json:"site_max_width"`
	ContentMaxWidth    *int              `json:"content_max_width"`
	SidebarSubsections bool              `json:"sidebar_subsections"`
	Dark               map[string]string `json:"dark"`
	Light              map[string]string `json:"light"`
	Presets            []Preset          `json:"presets,omitempty"`
	DefaultPreset      string            `json:"default_preset,omitempty"`
	Assets             map[string]Asset  `json:"assets"`
	Version            int64             `json:"version"`
}

type Color struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Group string `json:"group"`
}

var Colors = []Color{
	{"accent", "Primary accent", "Actions"}, {"accent-dark", "Accent background", "Actions"}, {"highlight", "Links and focus", "Actions"},
	{"on-accent", "Text on accent", "Actions"}, {"on-highlight", "Text on highlighted actions", "Actions"},
	{"accent-text", "Accent text", "Actions"},
	{"bg", "Page background", "Surfaces"}, {"panel", "Panels", "Surfaces"}, {"panel-2", "Secondary panels and table headers", "Surfaces"},
	{"field", "Fields", "Surfaces"}, {"popup", "Popups", "Surfaces"}, {"line", "Borders", "Surfaces"}, {"switch-off", "Switch off", "Surfaces"},
	{"switch-thumb", "Switch off thumb", "Surfaces"}, {"switch-thumb-on", "Switch on thumb", "Surfaces"}, {"switch-on-border", "Switch on edge", "Surfaces"},
	{"text", "Primary text", "Text"}, {"muted", "Secondary text", "Text"},
	{"blue", "Information", "Feedback"}, {"red", "Error", "Feedback"}, {"success", "Success", "Feedback"}, {"yellow", "Warning", "Feedback"}, {"on-warning", "Text on warning", "Feedback"},
	{"chart-1", "Chart 1", "Charts"}, {"chart-2", "Chart 2", "Charts"}, {"chart-3", "Chart 3", "Charts"}, {"chart-4", "Chart 4", "Charts"}, {"chart-5", "Chart 5", "Charts"}, {"chart-6", "Chart 6", "Charts"},
}
var AssetKeys = []string{"logo", "logo_light", "symbol", "symbol_light", "favicon", "favicon_light", "touch_icon", "email_logo"}

func Defaults() Settings {
	keys := []string{"bg", "panel", "panel-2", "field", "popup", "line", "switch-off", "text", "muted", "accent", "accent-dark", "highlight", "on-accent", "on-highlight", "blue", "red", "success", "yellow", "on-warning", "chart-2", "chart-3", "chart-4", "chart-5", "chart-6"}
	dark := []string{"#0a0a0a", "#141414", "#1c1c1c", "#101010", "#1c1c1c", "#303030", "#404040", "#f5f5f5", "#a3a3a3", "#e5e5e5", "#262626", "#ffffff", "#171717", "#171717", "#6ca8ff", "#f08088", "#79d6a0", "#f3c45b", "#171717", "#24a5bf", "#9767dc", "#db9950", "#4aa881", "#d86e9a"}
	light := []string{"#fafafa", "#ffffff", "#f0f0f0", "#ffffff", "#ffffff", "#d4d4d4", "#737373", "#171717", "#525252", "#262626", "#f0f0f0", "#444444", "#ffffff", "#ffffff", "#235dba", "#b42336", "#147844", "#916000", "#ffffff", "#24a5bf", "#9767dc", "#db9950", "#4aa881", "#d86e9a"}
	width := 1280
	s := Settings{SiteMaxWidth: &width, Dark: map[string]string{}, Light: map[string]string{}, Assets: map[string]Asset{}, Version: 1}
	for i, key := range keys {
		s.Dark[key] = dark[i]
		s.Light[key] = light[i]
	}
	return s
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s Settings) Validate() error {
	for _, field := range []struct {
		name    string
		value   *int
		minimum int
	}{{"site width", s.SiteMaxWidth, 960}, {"content width", s.ContentMaxWidth, 640}} {
		if field.value != nil && (*field.value < field.minimum || *field.value > 7680) {
			return fmt.Errorf("%s must be between %d and 7680 pixels, or empty for full width", field.name, field.minimum)
		}
	}
	if s.Version < 1 {
		return errors.New("version is required")
	}
	if !utf8.ValidString(s.Name) || (s.Name != "" && strings.TrimSpace(s.Name) == "") || len(s.Name) > 80 || strings.IndexFunc(s.Name, unicode.IsControl) >= 0 {
		return errors.New("site name must be empty or contain at most 80 bytes without control characters")
	}
	if !utf8.ValidString(s.Description) || len(s.Description) > 240 || strings.IndexFunc(s.Description, unicode.IsControl) >= 0 {
		return errors.New("description must contain at most 240 bytes without control characters")
	}
	a, err := mail.ParseAddress(s.ContactEmail)
	if s.ContactEmail != "" && (err != nil || a.Address != s.ContactEmail || len(s.ContactEmail) > 254 || strings.ContainsAny(s.ContactEmail, "\r\n")) {
		return errors.New("enter a valid contact email address")
	}
	for _, palette := range []map[string]string{s.Dark, s.Light} {
		if err := validatePalette(palette); err != nil {
			return err
		}
	}
	if err := s.validatePresets(); err != nil {
		return err
	}
	for key, asset := range s.Assets {
		known := false
		for _, k := range AssetKeys {
			if key == k {
				known = true
			}
		}
		if !known {
			return errors.New("unknown brand asset")
		}
		if err := asset.Validate(key); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

// ImageSource resolves both themes symmetrically. Legacy unsuffixed keys remain
// the dark slot, so previously saved brands need no migration.
func (s Settings) ImageSource(key string) string {
	if _, ok := s.Assets[key]; ok {
		return key
	}
	theme := "dark"
	kind := key
	if strings.HasSuffix(key, "_light") {
		theme = "light"
		kind = strings.TrimSuffix(key, "_light")
	}
	keysFor := func(role string) []string {
		if theme == "light" {
			return []string{role + "_light", role}
		}
		return []string{role, role + "_light"}
	}
	var candidates []string
	switch kind {
	case "logo":
		candidates = keysFor("logo")
	case "symbol":
		candidates = append(keysFor("symbol"), keysFor("favicon")...)
		candidates = append(candidates, keysFor("logo")...)
	case "favicon":
		candidates = append(keysFor("favicon"), keysFor("symbol")...)
		candidates = append(candidates, keysFor("logo")...)
	case "touch_icon":
		candidates = []string{"favicon_light", "favicon", "symbol_light", "symbol", "logo_light", "logo"}
	}
	for _, candidate := range candidates {
		if _, ok := s.Assets[candidate]; ok {
			return candidate
		}
	}
	return ""
}
func (s Settings) AssetURL(key string) string {
	if source := s.ImageSource(key); source != "" {
		return fmt.Sprintf("/branding/assets/%s?v=%d", source, s.Version)
	}
	return ""
}
func (s Settings) AssetMIME(key string) string {
	if source := s.ImageSource(key); source != "" {
		return s.Assets[source].MIME
	}
	return ""
}
