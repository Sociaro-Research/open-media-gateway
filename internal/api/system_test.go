package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"slices"
	"strings"
	"testing"
)

type systemFixture struct {
	system.Repository
	settings system.Settings
	admins   map[string]bool
}

func (f *systemFixture) Settings(context.Context) (system.Settings, error)  { return f.settings, nil }
func (f *systemFixture) IsAdmin(_ context.Context, id string) (bool, error) { return f.admins[id], nil }
func (f *systemFixture) SetAdmin(_ context.Context, actor, target string, admin, bootstrap bool) error {
	if !f.admins[actor] {
		return system.ErrForbidden
	}
	f.admins[target] = admin
	return nil
}
func (f *systemFixture) SaveSettings(_ context.Context, actor string, next system.Settings) (system.Settings, error) {
	if !f.admins[actor] {
		return next, system.ErrForbidden
	}
	f.settings = next
	f.settings.Version++
	return f.settings, nil
}
func TestSystemAccessAndProviderPolicy(t *testing.T) {
	s, _ := newPersonalTestServer()
	policy := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.system = policy
	h := s.handler()
	_, generation := issueTestKey(t, h, "alice", `{"name":"Generation"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"System","scopes":["generation","system:manage"]}`)
	for _, tc := range []struct {
		session, key string
		status       int
	}{{"", "", 401}, {"bob", "", 403}, {"alice", "", 200}, {"", generation, 403}, {"", manager, 200}, {"", "service-secret", 403}} {
		w := sendTest(h, "GET", "/v1/system/settings", "", tc.session, tc.key)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	w := sendTest(h, "POST", "/v1/api-keys", `{"name":"Escalate","scopes":["generation","system:manage"]}`, "bob", "")
	if w.Code != 403 {
		t.Fatalf("escalation: %d %s", w.Code, w.Body.String())
	}

	target := "11111111-1111-4111-8111-111111111111"
	w = sendTest(h, "PUT", "/v1/system/users/"+target+"/role", `{"is_system_admin":true}`, "alice", "")
	if w.Code != 204 || !policy.admins[target] {
		t.Fatalf("role update: %d %s", w.Code, w.Body.String())
	}
	w = sendTest(h, "PUT", "/v1/system/settings", `{"generation_ui":false,"personal_providers":true,"version":1}`, "", manager)
	if w.Code != 200 || policy.settings.GenerationUI || policy.settings.Version != 2 || !policy.settings.AllowInlineReferences {
		t.Fatalf("settings update: %d %s", w.Code, w.Body.String())
	}
	w = sendTest(h, "PUT", "/v1/system/settings", `{"generation_ui":true}`, "alice", "")
	if w.Code != 400 {
		t.Fatalf("missing fields: %d", w.Code)
	}
	w = sendTest(h, "GET", "/v1/system/settings", "", "alice", "")
	if strings.Contains(w.Body.String(), "allowed_providers") || strings.Contains(w.Body.String(), "available_providers") {
		t.Fatal("obsolete provider policy exposed", w.Body)
	}
	w = sendTest(h, "PUT", "/v1/system/settings", `{"generation_ui":true,"personal_providers":true,"allowed_providers":[],"version":2}`, "alice", "")
	if w.Code != 400 {
		t.Fatal("obsolete provider policy accepted", w.Code, w.Body)
	}
	policy.admins["alice"] = false
	w = sendTest(h, "GET", "/v1/system/settings", "", "", manager)
	if w.Code != 403 {
		t.Fatal("revoked administrator retained key access")
	}
	policy.settings.PersonalProviders = false
	w = sendTest(h, "GET", "/v1/admin/providers", "", "alice", "")
	if w.Code != 403 {
		t.Fatalf("provider policy: %d", w.Code)
	}
	for _, path := range []string{"/v1/generations", "/v1/generations/estimate"} {
		w = sendTest(h, "POST", path, `{"prompt":"test","model":"mock/mock-image-v1"}`, "", generation)
		if w.Code != 403 {
			t.Fatalf("disabled %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	policy.settings.GenerationUI = false
	policy.settings.PersonalProviders = true
	w = sendTest(h, "POST", "/v1/generations", `{"prompt":"test","model":"mock/mock-image-v1"}`, "", generation)
	if w.Code == 403 {
		t.Fatalf("UI toggle blocked API: %s", w.Body.String())
	}
}

type policyProviderAdmin struct {
	providerAdminStub
	calls int
}

func (p *policyProviderAdmin) List(context.Context) ([]providers.AdminProvider, error) {
	return []providers.AdminProvider{{ID: "bytedance", Name: "BytePlus ModelArk"}, {ID: "alibaba", Name: "Alibaba Cloud"}}, nil
}
func (p *policyProviderAdmin) Get(_ context.Context, id string) (providers.AdminProvider, error) {
	p.calls++
	return providers.AdminProvider{ID: id}, nil
}
func (p *policyProviderAdmin) Update(ctx context.Context, id string, _ providers.ProviderUpdate) (providers.AdminProvider, error) {
	return p.Get(ctx, id)
}
func (p *policyProviderAdmin) Check(ctx context.Context, id string) (providers.AdminProvider, error) {
	return p.Get(ctx, id)
}

type policyCatalogService struct{ accountService }

func (s *policyCatalogService) ListProviders(context.Context) []core.ProviderDescriptor {
	return []core.ProviderDescriptor{{ID: "bytedance", Models: []core.MediaModel{{ID: "fixture-video", MediaType: "video"}}}, {ID: "alibaba", Models: []core.MediaModel{{ID: "fixture-video", MediaType: "video"}}}}
}

func TestProviderConnectionsHaveNoGlobalAllowlist(t *testing.T) {
	s, _ := newPersonalTestServer()
	policy := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	backend := &policyProviderAdmin{}
	s.system = policy
	s.providerAdmin = backend
	s.service = &policyCatalogService{}
	h := s.handler()
	_, key := issueTestKey(t, h, "bob", `{"name":"Connections","scopes":["generation","providers:manage"]}`)
	assertList := func(path, session, secret string, want []string) {
		t.Helper()
		w := sendTest(h, "GET", path, "", session, secret)
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
		}
		got := []string{}
		for _, p := range result.Data {
			got = append(got, p.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: got %v want %v", path, got, want)
		}
	}
	for _, auth := range []struct{ session, key string }{{"bob", ""}, {"", key}, {"alice", ""}} {
		assertList("/v1/admin/providers", auth.session, auth.key, []string{"bytedance", "alibaba"})
		for _, op := range []struct{ method, suffix, body string }{{"GET", "", ""}, {"PUT", "", `{"enabled":true,"api_key":"fixture"}`}, {"POST", "/check", ""}} {
			w := sendTest(h, op.method, "/v1/admin/providers/bytedance"+op.suffix, op.body, auth.session, auth.key)
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", op.method, w.Code, w.Body.String())
			}
		}
	}
	assertList("/v1/providers", "", key, []string{"bytedance", "alibaba"})
	if backend.calls != 9 {
		t.Fatalf("connection management calls: %d", backend.calls)
	}

}

type sharedRoutingAdmin struct {
	providerAdminStub
	providerOwner, consumer string
}

func (p *sharedRoutingAdmin) List(ctx context.Context) ([]providers.AdminProvider, error) {
	p.providerOwner = identity.ProviderOwnerID(ctx)
	p.consumer = identity.OwnerID(ctx)
	return []providers.AdminProvider{}, nil
}

type sharedRoutingService struct {
	accountService
	providerOwner, storageOwner, consumer string
}

func (s *sharedRoutingService) ListProviders(ctx context.Context) []core.ProviderDescriptor {
	s.providerOwner = identity.ProviderOwnerID(ctx)
	s.storageOwner = identity.StorageOwnerID(ctx)
	s.consumer = identity.OwnerID(ctx)
	return []core.ProviderDescriptor{}
}
func TestSharedRoutingKeepsConsumerAndRequiresAdministratorForConnections(t *testing.T) {
	s, _ := newPersonalTestServer()
	policy := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	policy.settings.SharedResources = true
	s.system = policy
	backend := &sharedRoutingAdmin{}
	service := &sharedRoutingService{}
	s.providerAdmin = backend
	s.service = service
	h := s.handler()
	shared, secret := issueTestKey(t, h, "bob", `{"name":"Shared"}`)
	if shared.ResourceMode != "shared" || shared.StorageMode != "" {
		t.Fatalf("key defaults: %+v", shared)
	}
	w := sendTest(h, "GET", "/v1/providers", "", "", secret)
	if w.Code != 200 || service.providerOwner != identity.SharedOwnerID || service.storageOwner != identity.SharedOwnerID || service.consumer != identity.PersonalOwnerID("bob") {
		t.Fatalf("routing: %d %+v", w.Code, service)
	}
	for _, session := range []string{"bob", "alice"} {
		w = sendTest(h, "GET", "/v1/system/providers", "", session, "")
		want := 403
		if session == "alice" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s shared connection access: %d", session, w.Code)
		}
	}
	if backend.providerOwner != identity.SharedOwnerID || backend.consumer != "alice" {
		t.Fatal("shared management changed acting user")
	}
	w = sendTest(h, "GET", "/v1/admin/providers", "", "bob", "")
	if w.Code != 200 || backend.providerOwner != "bob" {
		t.Fatal("shared mode leaked into personal connections")
	}
	policy.settings.SharedResources = false
	w = sendTest(h, "GET", "/v1/providers", "", "", secret)
	if w.Code != 403 {
		t.Fatal("disabled shared generation catalog")
	}
	w = sendTest(h, "GET", "/v1/generations", "", "", secret)
	if w.Code != 200 {
		t.Fatal("disabled sharing hid existing history")
	}
	w = sendTest(h, "POST", "/v1/api-keys", `{"name":"Unavailable","resource_mode":"shared"}`, "bob", "")
	if w.Code != 403 {
		t.Fatal("issued shared key while disabled")
	}
}

func TestConsumerStorageAndDisabledProviderModes(t *testing.T) {
	s, _ := newPersonalTestServer()
	policy := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.system = policy
	s.storageAdmin = &storageAdminStub{}
	h := s.handler()
	_, key := issueTestKey(t, h, "bob", `{"name":"Personal"}`)
	for _, path := range []string{"/v1/admin/storage", "/v1/admin/storage/check"} {
		method := "PUT"
		if strings.HasSuffix(path, "/check") {
			method = "POST"
		}
		for _, tc := range []struct {
			user, driver string
			status       int
		}{{"bob", "local", 403}, {"bob", "s3", 200}, {"alice", "local", 200}} {
			w := sendTest(h, method, path, `{"driver":"`+tc.driver+`"}`, tc.user, "")
			if w.Code != tc.status {
				t.Fatalf("storage %s %s: %d %s", tc.user, tc.driver, w.Code, w.Body)
			}
		}
	}
	// A saved/default local destination must be rejected before any provider call,
	// including the compatibility endpoint and estimation.
	for _, path := range []string{"/v1/generations", "/v1/generations/estimate", "/v1/videos", "/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/audio/generations", "/v1/generations", "/v1/generations/estimate"} {
		w := sendTest(h, "POST", path, `{"prompt":"test"}`, "", key)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "personal_storage_required") {
			t.Fatalf("local consumption %s: %d %s", path, w.Code, w.Body)
		}
	}
	policy.settings.PersonalProviders = false
	for _, path := range []string{"/v1/generations", "/v1/generations/estimate", "/v1/videos", "/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/audio/generations", "/v1/generations", "/v1/generations/estimate"} {
		w := sendTest(h, "POST", path, `{"prompt":"test"}`, "", key)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "personal_providers_disabled") {
			t.Fatalf("disabled personal %s: %d %s", path, w.Code, w.Body)
		}
	}
	for _, mode := range []string{"personal", "shared"} {
		w := sendTest(h, "POST", "/v1/api-keys", `{"name":"Blocked","resource_mode":"`+mode+`"}`, "bob", "")
		if w.Code != 403 {
			t.Fatalf("issued unavailable %s key: %d", mode, w.Code)
		}
	}
	if w := sendTest(h, "GET", "/v1/generations", "", "", key); w.Code != 200 {
		t.Fatal("lost history")
	}
	policy.settings.SharedResources = true
	_, shared := issueTestKey(t, h, "bob", `{"name":"Shared"}`)
	if w := sendTest(h, "GET", "/v1/providers", "", "", shared); w.Code != 200 {
		t.Fatalf("shared access: %d", w.Code)
	}
	// Shared storage may be local if chosen by an administrator.
	if w := sendTest(h, "POST", "/v1/generations", `{"prompt":"test"}`, "", shared); w.Code == 403 {
		t.Fatalf("shared storage blocked: %s", w.Body)
	}
}

func TestInlineReferenceSettingUpdates(t *testing.T) {
	s, _ := newPersonalTestServer()
	policy := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.system = policy
	h := s.handler()
	for _, value := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"allow_inline_references": value, "generation_ui": true, "personal_providers": true, "version": policy.settings.Version})
		w := sendTest(h, "PUT", "/v1/system/settings", string(body), "alice", "")
		if w.Code != 200 {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
		w = sendTest(h, "GET", "/v1/system/settings", "", "alice", "")
		var saved system.Settings
		if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.AllowInlineReferences != value {
			t.Fatalf("read: %s %v", w.Body.String(), err)
		}
	}
}
