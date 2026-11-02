package api

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"strings"
	"testing"
)

func TestExclusiveProviderModesAcrossRolesAndStaleKeys(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.system = f
	s.providerAdmin = &policyProviderAdmin{}
	h := s.handler()
	_, adminKey := issueTestKey(t, h, "alice", `{"name":"Admin personal","scopes":["generation","system:manage","providers:manage","keys:manage"]}`)
	_, memberKey := issueTestKey(t, h, "bob", `{"name":"Member personal"}`)
	f.settings.SharedResources = true // Retained legacy hybrid, not a permitted write.
	_, sharedKey := issueTestKey(t, h, "alice", `{"name":"Old shared","resource_mode":"shared"}`)
	read := sendTest(h, "GET", "/v1/system/settings", "", "alice", "")
	var policy system.Settings
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &policy) != nil || !policy.SharedResources || policy.PersonalProviders || !f.settings.PersonalProviders || !strings.Contains(read.Body.String(), `"mode_selection_required":true`) {
		t.Fatal("legacy policy not effective-only", read.Code, read.Body)
	}
	for _, actor := range []struct{ session, key string }{{"alice", ""}, {"bob", ""}, {"", adminKey}, {"", memberKey}} {
		for _, path := range []string{"/v1/providers", "/v1/models", "/v1/admin/providers"} {
			if actor.key == "" && path != "/v1/admin/providers" {
				continue
			}
			if w := sendTest(h, "GET", path, "", actor.session, actor.key); w.Code != 403 {
				t.Fatalf("personal read %s: %d", path, w.Code)
			}
		}
		for _, path := range []string{"/v1/generations", "/v1/generations/estimate"} {
			if actor.key == "" {
				continue
			}
			if w := sendTest(h, "POST", path, `{"prompt":"fixture"}`, actor.session, actor.key); w.Code != 403 {
				t.Fatalf("personal admission %s: %d", path, w.Code)
			}
		}
	}
	for _, path := range []string{"/v1/references/providers/bytedance/groups", "/v1/references/providers/bytedance/import", "/v1/references/providers/bytedance/assets", "/v1/provider-resources", "/v1/provider-resources/estimate"} {
		if w := sendTest(h, "POST", path, `{}`, "", adminKey); w.Code != 403 {
			t.Fatal("stale personal key admitted external asset work", path, w.Code)
		}
	}
	if w := sendTest(h, "PUT", "/v1/system/storage/quota", `{"limit_bytes":1000,"version":1}`, "alice", ""); w.Code != 400 {
		t.Fatal("quota update must request explicit legacy mode selection", w.Code)
	}
	if w := sendTest(h, "GET", "/v1/system/providers", "", "alice", ""); w.Code != 200 {
		t.Fatal("shared administration lost", w.Code)
	}
	if w := sendTest(h, "GET", "/v1/generations", "", "", adminKey); w.Code != 200 {
		t.Fatal("old personal history lost", w.Code)
	}
	for _, flags := range []string{`"shared_resources":true,"personal_providers":true`, `"shared_resources":false,"personal_providers":false`} {
		if w := sendTest(h, "PUT", "/v1/system/settings", `{"generation_ui":true,`+flags+`,"version":1}`, "alice", ""); w.Code != 400 {
			t.Fatal("invalid direct flags", w.Code)
		}
	}
	if w := sendTest(h, "POST", "/v1/system/setup-profile", `{"profile":"operator","allow_personal":true,"version":1}`, "alice", ""); w.Code != 400 {
		t.Fatal("hybrid profile override", w.Code)
	}
	if w := sendTest(h, "POST", "/v1/system/setup-profile", `{"profile":"personal","version":1}`, "alice", ""); w.Code != 200 || f.settings.SharedResources || !f.settings.PersonalProviders {
		t.Fatal("personal profile", w.Code, w.Body)
	}
	if w := sendTest(h, "GET", "/v1/providers", "", "", sharedKey); w.Code != 403 {
		t.Fatal("stale shared key bypassed personal mode", w.Code)
	}
	if w := sendTest(h, "GET", "/v1/generations", "", "", sharedKey); w.Code != 200 {
		t.Fatal("old shared history lost", w.Code)
	}
}
