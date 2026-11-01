package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type remoteFixture struct {
	records map[string]remote.Record
	writes  int
}

func (f *remoteFixture) RemoteWorkspace(_ context.Context, owner string) (remote.Record, error) {
	r, ok := f.records[owner]
	if !ok {
		return r, remote.ErrNotFound
	}
	return r, nil
}
func (f *remoteFixture) SaveRemoteWorkspace(_ context.Context, r remote.Record, v int64) (remote.Record, error) {
	if f.records[r.Owner].Version != v {
		return r, remote.ErrConflict
	}
	f.writes++
	r.Version = v + 1
	f.records[r.Owner] = r
	return r, nil
}
func (f *remoteFixture) DisconnectRemoteWorkspace(_ context.Context, owner string, v int64) (remote.Record, error) {
	if f.records[owner].Version != v {
		return remote.Record{}, remote.ErrConflict
	}
	r := remote.Record{Owner: owner, Version: v + 1}
	f.records[owner] = r
	return r, nil
}
func TestRemoteProxyLocalPermissionsTenantAndNoSecrets(t *testing.T) {
	const company = "11111111-1111-4111-8111-111111111111"
	const token = "synthetic-remote-customer-generation-key"
	calls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong remote token")
		}
		switch r.URL.Path {
		case "/v1/key":
			fmt.Fprintf(w, `{"key":{"id":"22222222-2222-4222-8222-222222222222","user_id":"33333333-3333-4333-8333-333333333333","organization_id":%q,"resource_mode":"shared","scopes":["generation"]},"organization":{"id":%q,"name":"Company"}}`, company, company)
		case "/v1/models":
			fmt.Fprintf(w, `{"data":[],"message":%q}`, token)
		default:
			t.Error("unexpected request", r.URL.Path)
		}
	}))
	defer upstream.Close()
	repo := &remoteFixture{records: map[string]remote.Record{}}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("f", 32))
	manager := remote.New(repo, cipher, upstream.Client())
	s, _ := newPersonalTestServer()
	policy := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.system = policy
	s.remoteWorkspaceManager = manager
	h := s.handler()
	body, _ := json.Marshal(map[string]any{"origin": upstream.URL, "organization_id": company, "token": token, "version": 0, "consent": true})
	if w := sendTest(h, "POST", "/v1/system/remote-workspace", string(body), "bob", ""); w.Code != 403 || calls != 0 {
		t.Fatal("unprivileged connect", w.Code)
	}
	if w := sendTest(h, "HEAD", "/v1/system/remote-workspace", string(body), "alice", ""); w.Code != 200 || repo.writes != 0 || calls != 0 || w.Body.Len() != 0 {
		t.Fatal("HEAD caused effects", w.Code)
	}
	if w := sendTest(h, "POST", "/v1/system/remote-workspace", string(body), "alice", ""); w.Code != 409 || calls != 0 {
		t.Fatal("fresh existing-key bypass", w.Code, calls)
	}
	repo.records["alice"] = remote.Record{Owner: "alice", Version: 1, Active: true, Provisioning: "legacy", Origin: upstream.URL, OrganizationID: company, UserID: "33333333-3333-4333-8333-333333333333"}
	body, _ = json.Marshal(map[string]any{"origin": upstream.URL, "organization_id": company, "token": token, "version": 1, "consent": true})
	w := sendTest(h, "POST", "/v1/system/remote-workspace", string(body), "alice", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "cipher") {
		t.Fatal(w.Code, w.Body)
	}
	acceptedVersion := repo.records["alice"].Version
	acceptedKey := repo.records["alice"].KeyID
	beforeRetry := calls
	if retry := sendTest(h, "POST", "/v1/system/remote-workspace", string(body), "alice", ""); retry.Code != 200 || repo.records["alice"].Version != acceptedVersion || repo.records["alice"].KeyID != acceptedKey || calls != beforeRetry {
		t.Fatal("lost legacy reply was not idempotent", retry.Code, calls)
	}
	policy.admins["bob"] = true
	w = sendTest(h, "GET", "/v1/system/remote-workspace", "", "bob", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), company) {
		t.Fatal("another local admin saw owner connection", w.Code, w.Body)
	}
	before := calls
	w = sendTest(h, "GET", "/v1/system/remote-workspace/proxy/v1/models", "", "bob", "")
	if w.Code != 404 || calls != before {
		t.Fatal("cross-owner proxy", w.Code)
	}
	w = sendRemoteContext(h, "GET", "/v1/system/remote-workspace/proxy/v1/models", "", "alice", repo.records["alice"].SnapshotID(), "")
	if w.Code != 200 || strings.Contains(w.Body.String(), token) || !strings.Contains(w.Body.String(), "[redacted]") {
		t.Fatal("credential echoed", w.Code, w.Body)
	}
	before = calls
	w = sendTest(h, "POST", "/v1/system/remote-workspace/proxy/v1/organizations", `{"name":"Must not create"}`, "alice", "")
	if w.Code != 404 || calls != before {
		t.Fatal("management operation proxied", w.Code)
	}
	policy.admins["alice"] = false
	w = sendTest(h, "GET", "/v1/system/remote-workspace/proxy/v1/models", "", "alice", "")
	if w.Code != 403 || calls != before {
		t.Fatal("revoked admin retained remote access", w.Code)
	}
}
