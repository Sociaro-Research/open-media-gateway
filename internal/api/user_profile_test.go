package api

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"strings"
	"testing"
)

type profileAuthFixture struct {
	browserAuthStub
	ids []string
}

func (f *profileAuthFixture) UpdateProfile(_ context.Context, id string, in auth.ProfileUpdate) (identity.User, error) {
	f.ids = append(f.ids, id)
	user := identity.User{ID: id, Theme: "dark"}
	if in.Name != nil {
		user.Name = *in.Name
	}
	if in.ReportColumns != nil {
		user.ReportColumns = *in.ReportColumns
	}
	if in.ColorPreset != nil {
		user.ColorPreset = *in.ColorPreset
	}
	return user, nil
}

func TestProfileColorThemesMustExist(t *testing.T) {
	s, _ := newPersonalTestServer()
	fixture := &profileAuthFixture{}
	s.auth = fixture
	h := s.handler()
	for _, id := range []string{"original", ""} {
		w := sendTest(h, "PATCH", "/v1/me", `{"color_preset":"`+id+`"}`, "alice", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"color_preset":"`+id+`"`) {
			t.Fatal(w.Code, w.Body)
		}
	}
	// Clear Lime is selectable only when an administrator has saved it.
	if w := sendTest(h, "PATCH", "/v1/me", `{"color_preset":"clear-lime"}`, "alice", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	saved := branding.Defaults().Normalize()
	palette := branding.Defaults().Normalize()
	saved.Presets = append(saved.Presets, branding.Preset{ID: "clear-lime", Name: "Clear Lime", Dark: palette.Dark, Light: palette.Light})
	s.system = &brandingFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}, brand: saved}
	h = s.handler()
	if w := sendTest(h, "PATCH", "/v1/me", `{"color_preset":"clear-lime"}`, "alice", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	calls := len(fixture.ids)
	if w := sendTest(h, "PATCH", "/v1/me", `{"color_preset":"missing"}`, "alice", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if len(fixture.ids) != calls {
		t.Fatal("unpublished preset reached profile writer")
	}
}
func TestProfileUpdatesRequireOwnBrowserSession(t *testing.T) {
	s, _ := newPersonalTestServer()
	fixture := &profileAuthFixture{}
	s.auth = fixture
	s.system = &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}, manager: true}
	h := s.handler()
	for _, id := range []string{"alice", "bob"} {
		if w := sendTest(h, "PATCH", "/v1/me", `{"name":"New name"}`, id, ""); w.Code != 200 {
			t.Fatal(id, w.Code, w.Body)
		}
	}
	if len(fixture.ids) != 2 || fixture.ids[0] != "alice" || fixture.ids[1] != "bob" {
		t.Fatal(fixture.ids)
	}
	if w := sendTest(h, "PATCH", "/v1/me", `{"name":"No session"}`, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := sendTest(h, "PATCH", "/v1/me", `{"name":"Other","id":"bob"}`, "alice", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	_, key := issueTestKey(t, h, "alice", `{"name":"Generation","scopes":["generation"]}`)
	if w := sendTest(h, "PATCH", "/v1/me", `{"name":"Via API key"}`, "alice", key); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	if len(fixture.ids) != 2 {
		t.Fatal("unauthorized update", fixture.ids)
	}
	w := sendTest(h, "PATCH", "/v1/me", `{"report_columns":["date","generation_id"]}`, "alice", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"report_columns":["date","generation_id"]`) {
		t.Fatal(w.Code, w.Body)
	}
	if fixture.ids[len(fixture.ids)-1] != "alice" {
		t.Fatal(fixture.ids)
	}
}
