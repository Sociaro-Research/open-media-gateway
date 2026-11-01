package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type setupFixture struct {
	*systemFixture
	fail   bool
	writes int
}

func TestSystemSettingsPreserveScenarioPolicyWhenOmitted(t *testing.T) {
	for _, policy := range []struct{ shared, personal bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		s, _ := newPersonalTestServer()
		settings := system.Defaults()
		settings.SharedResources = policy.shared
		settings.PersonalProviders = policy.personal
		settings.CustomerWorkspaces = true
		settings.SetupPending = true
		limit := int64(1234)
		settings.SharedStorageLimitBytes = &limit
		f := &setupFixture{systemFixture: &systemFixture{settings: settings, admins: map[string]bool{"alice": true}}}
		s.system = f
		h := s.handler()
		body := `{"generation_ui":false,"allow_inline_references":false,"customer_workspaces":true,"version":1}`
		if w := sendTest(h, "PUT", "/v1/system/settings", body, "bob", ""); w.Code != 403 || f.writes != 0 {
			t.Fatal("unauthorized partial settings update", w.Code)
		}
		w := sendTest(h, "PUT", "/v1/system/settings", body, "alice", "")
		if w.Code != 200 || f.settings.SharedResources != policy.shared || f.settings.PersonalProviders != policy.personal {
			t.Fatalf("policy %+v changed: %d %s", policy, w.Code, w.Body.String())
		}
		if f.settings.GenerationUI || f.settings.AllowInlineReferences || !f.settings.CustomerWorkspaces || !f.settings.SetupPending || f.settings.SharedStorageLimitBytes != &limit {
			t.Fatal("independent settings lost", f.settings)
		}
		w = sendTest(h, "GET", "/v1/system/settings", "", "alice", "")
		var read system.Settings
		if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil || w.Code != 200 || read.SharedResources != policy.shared || read.PersonalProviders != policy.personal {
			t.Fatal("confirmed read lost scenario policy", w.Code, err, read)
		}
		before := f.settings
		if w = sendTest(h, "PUT", "/v1/system/settings", body, "alice", ""); w.Code != 409 || f.settings != before {
			t.Fatal("stale partial update changed policy", w.Code)
		}
		// Existing API clients may still explicitly change both policies.
		w = sendTest(h, "PUT", "/v1/system/settings", `{"generation_ui":true,"shared_resources":true,"personal_providers":false,"version":2}`, "alice", "")
		if w.Code != 200 || !f.settings.SharedResources || f.settings.PersonalProviders || !f.settings.CustomerWorkspaces {
			t.Fatal("explicit policy compatibility lost", w.Code, w.Body.String())
		}
	}
}

func (f *setupFixture) SaveSettings(ctx context.Context, actor string, next system.Settings) (system.Settings, error) {
	if f.fail {
		return next, errors.New("fixture storage failure")
	}
	if next.Version != f.settings.Version {
		return next, system.ErrConflict
	}
	f.writes++
	return f.systemFixture.SaveSettings(ctx, actor, next)
}
func TestSetupProfilePreservesSettingsAndAuthorization(t *testing.T) {
	s, _ := newPersonalTestServer()
	limit := int64(12345)
	settings := system.Defaults()
	settings.SharedStorageLimitBytes = &limit
	settings.AllowInlineReferences = false
	settings.GenerationUI = false
	settings.SetupPending = true
	settings.CustomerWorkspaces = true
	f := &setupFixture{systemFixture: &systemFixture{settings: settings, admins: map[string]bool{"alice": true}}}
	s.system = f
	h := s.handler()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := sendTest(h, method, "/v1/system/setup-profile", `{"profile":"operator","version":1}`, "alice", "")
		if w.Code != 405 || f.writes != 0 {
			t.Fatal("read mutated profile", w.Code)
		}
	}
	if w := sendTest(h, "POST", "/v1/system/setup-profile", `{"profile":"operator","version":1}`, "bob", ""); w.Code != 403 {
		t.Fatal("ordinary user changed policy", w.Code)
	}
	for _, profile := range []string{"skipped", "sociaro", "invalid"} {
		w := sendTest(h, "POST", "/v1/system/setup-profile", `{"profile":"`+profile+`","version":1}`, "alice", "")
		if w.Code != 400 || f.writes != 0 {
			t.Fatal("unsupported profile mutated settings", w.Code)
		}
	}
	f.fail = true
	if w := sendTest(h, "POST", "/v1/system/setup-profile", `{"profile":"operator","version":1}`, "alice", ""); w.Code != 503 || f.settings != settings {
		t.Fatal("failed save changed settings")
	}
	f.fail = false
	w := sendTest(h, "POST", "/v1/system/setup-profile", `{"profile":"operator","allow_personal":true,"version":1}`, "alice", "")
	if w.Code != 200 || !f.settings.SharedResources || !f.settings.PersonalProviders || f.settings.SetupPending {
		t.Fatal(w.Code, w.Body)
	}
	if !f.settings.CustomerWorkspaces || f.settings.GenerationUI || f.settings.AllowInlineReferences || f.settings.SharedStorageLimitBytes != &limit {
		t.Fatal("profile overwrote unrelated settings")
	}
	before := f.settings
	w = sendTest(h, "POST", "/v1/system/setup-profile", `{"profile":"personal","version":1}`, "alice", "")
	if w.Code != 409 || f.settings != before {
		t.Fatal("stale profile changed state")
	}
	w = sendTest(h, "POST", "/v1/system/setup-profile", `{"profile":"personal","version":2}`, "alice", "")
	if w.Code != 200 || f.settings.SharedResources || !f.settings.PersonalProviders {
		t.Fatal(w.Code, w.Body)
	}
	if f.writes != 2 {
		t.Fatal("unexpected side effects", f.writes)
	}
}
