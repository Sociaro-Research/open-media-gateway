package api

import (
	"context"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type connectionAdminFixture struct {
	providerAdminStub
	owner   string
	creates int
}

func (f *connectionAdminFixture) Types() []providers.ProviderType {
	return []providers.ProviderType{{ID: "bytedance", Name: "BytePlus API"}}
}
func (f *connectionAdminFixture) Create(ctx context.Context, input providers.CreateConnection) (providers.AdminProvider, error) {
	f.owner = identity.ProviderOwnerID(ctx)
	f.creates++
	return providers.AdminProvider{ID: "bytedance--fixture", Name: input.Name, APIType: input.APIType}, nil
}
func TestProviderConnectionCreationAuthorizationAndScope(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	backend := &connectionAdminFixture{}
	s.providerAdmin = backend
	h := s.handler()
	for _, prefix := range []string{"/v1/admin/providers", "/v1/system/providers"} {
		if w := sendTest(h, "POST", prefix, `{"api_type":"bytedance","name":"Private"}`, "bob", ""); w.Code != 403 {
			t.Fatal("non-administrator created connection", w.Code)
		}
		if w := sendTest(h, "GET", prefix+"/types", "", "alice", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if w := sendTest(h, "POST", prefix, `{"api_type":"bytedance","name":"Private"}`, "alice", ""); w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		expected := "alice"
		if prefix == "/v1/system/providers" {
			expected = identity.SharedOwnerID
		}
		if backend.owner != expected {
			t.Fatal("incorrect resource owner", backend.owner)
		}
		if w := sendTest(h, "PUT", prefix+"/bytedance", `{"name":"Changed","connection":{"manual_catalog":true}}`, "alice", ""); w.Code != 400 {
			t.Fatal("missing version accepted", w.Code)
		}
	}
	if backend.creates != 2 {
		t.Fatal(backend.creates)
	}
}
