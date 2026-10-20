package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type catalogAPIFixture struct {
	catalogsync.Repository
	settings catalogsync.Settings
}

func (f *catalogAPIFixture) Settings(context.Context) (catalogsync.Settings, error) {
	return f.settings, nil
}
func (f *catalogAPIFixture) SaveSettings(_ context.Context, _ string, s catalogsync.Settings) (catalogsync.Settings, error) {
	if err := s.Validate(); err != nil {
		return s, err
	}
	f.settings = s
	f.settings.Version++
	return f.settings, nil
}
func TestCatalogSyncAdministratorBoundary(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.catalogSync = &catalogAPIFixture{settings: catalogsync.Settings{Timezone: "UTC", Version: 1}}
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Generation"}`)
	for _, path := range []string{"settings", "schedules", "runs", "documents/test", "providers/openai", "providers/openai/versions"} {
		for _, session := range []string{"bob", ""} {
			w := sendTest(h, http.MethodGet, "/v1/system/catalog/"+path, "", session, key)
			if w.Code != 403 {
				t.Fatalf("%s leaked to nonadmin: %d", path, w.Code)
			}
		}
	}
	w := sendTest(h, "GET", "/v1/system/catalog/settings", "", "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = sendTest(h, "PUT", "/v1/system/catalog/settings", `{"timezone":"wrong/zone","email":"a@example.test","version":1}`, "alice", "")
	if w.Code != 400 {
		t.Fatal("invalid timezone accepted", w.Code)
	}
	w = sendTest(h, "PUT", "/v1/system/catalog/settings", `{"timezone":"Asia/Nicosia","email":"a@example.test","version":1}`, "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}
