package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/branding"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type brandingFixture struct {
	*systemFixture
	brand branding.Settings
}

func (f *brandingFixture) Branding(context.Context) (branding.Settings, error) { return f.brand, nil }
func (f *brandingFixture) SaveBranding(_ context.Context, actor string, s branding.Settings) (branding.Settings, error) {
	if !f.admins[actor] {
		return s, system.ErrForbidden
	}
	if s.Version != f.brand.Version {
		return s, system.ErrConflict
	}
	s.Version++
	f.brand = s
	return s, nil
}
func TestBrandingPermissionsPersistenceAndPublicPages(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &brandingFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, brand: branding.Defaults()}
	s.system = f
	h := s.handler()
	for _, tc := range []struct {
		user string
		code int
	}{{"", 401}, {"bob", 403}, {"alice", 200}} {
		w := sendTest(h, "GET", "/v1/system/branding", "", tc.user, "")
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	_, key := issueTestKey(t, h, "alice", `{"name":"Generate","scopes":["generation"]}`)
	if w := sendTest(h, "GET", "/v1/system/branding", "", "", key); w.Code != 403 {
		t.Fatal("generation key gained branding access", w.Code)
	}
	next := branding.Defaults()
	next.Name = `New & <Brand>`
	next.Dark["accent"] = "#aa11bb"
	next.ContactEmail = "support@example.test"
	next.Assets["logo"] = branding.Asset{MIME: "image/svg+xml", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 20"><path d="M0 0h100v20H0z"/></svg>`)}
	raw, _ := json.Marshal(next)
	for _, tc := range []struct {
		user string
		code int
	}{{"", 401}, {"bob", 403}} {
		w := sendTest(h, "PUT", "/v1/system/branding", string(raw), tc.user, "")
		if w.Code != tc.code {
			t.Fatal("unauthorized write", tc, w.Code)
		}
	}
	_, manager := issueTestKey(t, h, "alice", `{"name":"Manage","scopes":["generation","system:manage"]}`)
	if w := sendTest(h, "GET", "/v1/system/branding", "", "", manager); w.Code != 200 {
		t.Fatal("administrator management key rejected", w.Code)
	}
	w := sendTest(h, "PUT", "/v1/system/branding", string(raw), "alice", "")
	if w.Code != 200 || f.brand.Version != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = sendTest(h, "PUT", "/v1/system/branding", string(raw), "alice", ""); w.Code != 409 {
		t.Fatal("stale update accepted", w.Code)
	}
	w = sendTest(h, "GET", "/site.json", "", "", "")
	var site struct {
		Branding branding.Settings `json:"branding"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &site) != nil || site.Branding.Name != next.Name || site.Branding.Dark["accent"] != "#aa11bb" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = sendTest(h, "GET", "/branding/assets/logo?v=2", "", "", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(w.Code, w.Header())
	}
	if w = sendTest(h, "GET", "/branding/assets/missing", "", "", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	malicious := branding.Defaults()
	malicious.Version = f.brand.Version
	malicious.Assets["logo"] = branding.Asset{MIME: "image/svg+xml", Data: []byte(`<svg onload="alert(1)"/>`)}
	raw, _ = json.Marshal(malicious)
	if w = sendTest(h, "PUT", "/v1/system/branding", string(raw), "alice", ""); w.Code != 400 {
		t.Fatal("unsafe image accepted", w.Code)
	}
	if strings.Contains(string(f.brand.Assets["logo"].Data), "onload") {
		t.Fatal("invalid save altered confirmed logo")
	}
	f.admins["alice"] = false
	if w := sendTest(h, "GET", "/v1/system/branding", "", "", manager); w.Code != 403 {
		t.Fatal("revoked administrator retained appearance access", w.Code)
	}
}
func TestSeparateDocumentationResourcesUseBranding(t *testing.T) {
	f := &brandingFixture{brand: branding.Defaults()}
	f.brand.Name = "White Label"
	s := &Server{system: f}
	h, err := WithPublicAPI(s.handler(), "https://console.example.test", "", "https://docs.example.test", f)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://docs.example.test/site.json", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "White Label") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestLayoutSettingsHTTPAndPublicRendering(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &brandingFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, brand: branding.Defaults()}
	s.system = f
	h := s.handler()
	next := branding.Defaults()
	width := 1000
	next.SiteMaxWidth, next.ContentMaxWidth, next.SidebarSubsections = nil, &width, true
	raw, _ := json.Marshal(next)
	if w := sendTest(h, "PUT", "/v1/system/branding", string(raw), "alice", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if f.brand.SiteMaxWidth != nil || f.brand.ContentMaxWidth == nil || *f.brand.ContentMaxWidth != width || !f.brand.SidebarSubsections {
		t.Fatal("layout not saved", f.brand)
	}
	w := sendTest(h, "GET", "/site.json", "", "", "")
	if !strings.Contains(w.Body.String(), `"content_max_width":1000`) || !strings.Contains(w.Body.String(), `"sidebar_subsections":true`) {
		t.Fatal(w.Body.String())
	}
	// Old clients omit layout fields; absent fields retain the original defaults.
	var legacy map[string]any
	json.Unmarshal(raw, &legacy)
	for _, field := range []string{"site_max_width", "content_max_width", "sidebar_subsections"} {
		delete(legacy, field)
	}
	legacy["version"] = f.brand.Version
	raw, _ = json.Marshal(legacy)
	if w := sendTest(h, "PUT", "/v1/system/branding", string(raw), "alice", ""); w.Code != 200 || f.brand.SiteMaxWidth == nil || *f.brand.SiteMaxWidth != 1280 {
		t.Fatal("legacy request", w.Code, w.Body.String())
	}
	for _, invalid := range []any{-1, 959, 7681, 1200.5, "100%; color:red"} {
		legacy["site_max_width"], legacy["version"] = invalid, f.brand.Version
		raw, _ = json.Marshal(legacy)
		if w := sendTest(h, "PUT", "/v1/system/branding", string(raw), "alice", ""); w.Code != 400 {
			t.Fatal("invalid width accepted", invalid, w.Code)
		}
	}
}

func TestUnconfiguredPublicBrandingAndClearingIdentity(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &brandingFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, brand: branding.Defaults()}
	s.system = f
	h := s.handler()
	w := sendTest(h, "GET", "/site.json", "", "", "")
	var site struct {
		Branding branding.Settings `json:"branding"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &site) != nil || site.Branding.Name != "" || site.Branding.ContactEmail != "" || len(site.Branding.Assets) != 0 {
		t.Fatal("public defaults contain installation branding", w.Code, w.Body.String())
	}
	for _, key := range branding.AssetKeys {
		if w = sendTest(h, "GET", "/branding/assets/"+key, "", "", ""); w.Code != 404 {
			t.Fatal(key, w.Code)
		}
	}
	f.brand.Name, f.brand.ContactEmail = "Configured brand", "admin`example.test"
	f.brand.Assets["logo"] = branding.Asset{MIME: "image/svg+xml", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}
	raw, _ := json.Marshal(branding.Defaults())
	if w = sendTest(h, "PUT", "/v1/system/branding", string(raw), "alice", ""); w.Code != 200 {
		t.Fatal("empty identity rejected", w.Code, w.Body.String())
	}
	if f.brand.Name != "" || f.brand.ContactEmail != "" || len(f.brand.Assets) != 0 {
		t.Fatal("cleared brand retained identity or images")
	}
	if w = sendTest(h, "GET", "/branding/assets/logo", "", "", ""); w.Code != 404 {
		t.Fatal("cleared image still served", w.Code)
	}
}
