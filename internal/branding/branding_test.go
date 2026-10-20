package branding

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestLayoutValidationAndLegacyDefaults(t *testing.T) {
	s := Defaults()
	if err := json.Unmarshal([]byte(`{"name":"Existing brand"}`), &s); err != nil || s.SiteMaxWidth == nil || *s.SiteMaxWidth != 1280 || s.ContentMaxWidth != nil || s.SidebarSubsections {
		t.Fatal("legacy layout changed", s, err)
	}
	for _, tc := range []struct {
		site, content int
		valid         bool
	}{{960, 640, true}, {7680, 7680, true}, {959, 640, false}, {1280, 639, false}, {7681, 1000, false}, {1280, 7681, false}, {-1, 1000, false}} {
		s.SiteMaxWidth, s.ContentMaxWidth = &tc.site, &tc.content
		if (s.Validate() == nil) != tc.valid {
			t.Fatal(tc)
		}
	}
	s.SiteMaxWidth, s.ContentMaxWidth = nil, nil
	if err := s.Validate(); err != nil {
		t.Fatal("full width rejected", err)
	}
}

func TestBrandingValidation(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Settings){
		func(s *Settings) { s.Name = "   " }, func(s *Settings) { s.Name = "X\nInjected" }, func(s *Settings) { s.ContactEmail = "Name <a@example.test>" },
		func(s *Settings) { s.Dark["accent"] = "red; background:url(https://example.test)" }, func(s *Settings) { delete(s.Light, "text") },
		func(s *Settings) { s.Light["unexpected"] = "#ffffff" }, func(s *Settings) { s.Assets["unknown"] = Asset{} }, func(s *Settings) { s.Version = 0 },
	} {
		s := Defaults()
		mutate(&s)
		if s.Validate() == nil {
			t.Fatal("invalid settings accepted", s)
		}
	}
}
func TestPassiveSVG(t *testing.T) {
	good := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><defs><linearGradient id="a"><stop offset="0" stop-color="#f00"/></linearGradient></defs><path d="M0 0h20v20H0z" fill="url(#a)"/></svg>`
	if err := (Asset{MIME: "image/svg+xml", Data: []byte(good)}).Validate("logo"); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{`<svg onload="alert(1)"/>`, `<svg><script>alert(1)</script></svg>`, `<svg><foreignObject/></svg>`, `<svg><use href="https://example.test/a.svg"/></svg>`, `<!DOCTYPE svg><svg/>`, `<svg><path fill="url(https://example.test)"/></svg>`, `<svg style="background:red"/>`, `<svg/><svg/>`, `<svg><animate/></svg>`, `<svg><path fill="url(#a) url(https://example.test)"/></svg>`} {
		if (Asset{MIME: "image/svg+xml", Data: []byte(content)}).Validate("logo") == nil {
			t.Fatal("active SVG accepted", content)
		}
	}
	if (Asset{MIME: "image/svg+xml", Data: []byte(good)}).Validate("email_logo") == nil {
		t.Fatal("SVG email logo accepted")
	}
}
func TestRasterValidationAndFallbacks(t *testing.T) {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	a := Asset{MIME: "image/png", Data: b.Bytes()}
	if err := a.Validate("touch_icon"); err != nil {
		t.Fatal(err)
	}
	if (Asset{MIME: "image/jpeg", Data: b.Bytes()}).Validate("logo") == nil {
		t.Fatal("MIME mismatch accepted")
	}
	if (Asset{MIME: "image/png", Data: []byte(strings.Repeat("x", MaxAssetBytes+1))}).Validate("logo") == nil {
		t.Fatal("oversized image accepted")
	}
	s := Defaults()
	s.Assets["logo"] = a
	s.Version = 9
	if s.AssetURL("symbol") != "/branding/assets/logo?v=9" {
		t.Fatal("custom logo fallback missing")
	}
	s.Assets["favicon"] = a
	if s.AssetURL("touch_icon") != "/branding/assets/favicon?v=9" {
		t.Fatal("icon fallback missing")
	}
}

func TestThemeImageResolutionAndLegacyCompatibility(t *testing.T) {
	svg := Asset{MIME: "image/svg+xml", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}
	for _, tc := range []struct {
		assets      []string
		key, source string
	}{
		{[]string{"logo"}, "logo_light", "logo"},
		{[]string{"logo_light"}, "logo", "logo_light"},
		{[]string{"symbol", "symbol_light"}, "symbol", "symbol"},
		{[]string{"symbol", "symbol_light"}, "symbol_light", "symbol_light"},
		{[]string{"symbol"}, "symbol_light", "symbol"},
		{[]string{"symbol_light"}, "symbol", "symbol_light"},
		{[]string{"favicon", "favicon_light"}, "favicon_light", "favicon_light"},
		{[]string{"favicon_light"}, "favicon", "favicon_light"},
		{[]string{"symbol", "symbol_light"}, "favicon_light", "symbol_light"},
		{[]string{"favicon", "favicon_light"}, "symbol_light", "favicon_light"},
		{[]string{"logo_light"}, "symbol_light", "logo_light"},
		{[]string{"logo", "logo_light"}, "touch_icon", "logo_light"},
		{nil, "symbol_light", ""},
	} {
		s := Defaults()
		for _, key := range tc.assets {
			s.Assets[key] = svg
		}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		if got := s.ImageSource(tc.key); got != tc.source {
			t.Fatalf("%+v: source=%s", tc, got)
		}
	}
	s := Defaults()
	if s.AssetURL("symbol_light") != "" || s.AssetMIME("symbol_light") != "" {
		t.Fatal("unconfigured brand must not have a compact logo")
	}
}

func TestUnconfiguredBrandHasNoIdentityOrImages(t *testing.T) {
	s := Defaults()
	if s.Name != "" || s.Description != "" || s.ContactEmail != "" || len(s.Assets) != 0 {
		t.Fatal("unconfigured installation contains a brand", s)
	}
	for _, key := range AssetKeys {
		if s.ImageSource(key) != "" || s.AssetURL(key) != "" || s.AssetMIME(key) != "" {
			t.Fatal("unconfigured installation resolves an image", key)
		}
	}
}
