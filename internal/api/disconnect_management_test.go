package api

import (
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func TestDisconnectManagementPreservesLocalAuthorityAndOwnerBinding(t *testing.T) {
	s, keys := newPersonalTestServer()
	policy := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.system = policy
	original := remote.Record{Owner: "alice", Access: "company-api-v1", Active: true, Version: 7, Origin: "https://central.example.test", OrganizationID: "company-a"}
	other := remote.Record{Owner: "bob", Access: "company-api-v1", Active: true, Version: 3, Origin: "https://central.example.test", OrganizationID: "company-b"}
	repo := &remoteFixture{records: map[string]remote.Record{"alice": original, "bob": other}}
	s.remoteWorkspaceManager = remote.New(repo, nil, &http.Client{})
	const secret = "gw_fffffffffffffffffffffffffffffffffffffffffff"
	keys.records = append(keys.records, storedTestKey{owner: "alice", hash: apikey.Hash(secret), key: apikey.Key{ID: "synthetic-key", Scopes: []string{apikey.Generation}, ResourceMode: "personal"}})
	h := s.handler()
	for _, tc := range []struct {
		name, session, bearer string
		status                int
	}{
		{"anonymous", "", "", 401},
		{"nonadministrator", "bob", "", 403},
		{"generation-key-not-local-admin-authority", "", secret, 403},
		{"invalid-explicit-key-does-not-use-cookie", "alice", "invalid", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := sendTest(h, "DELETE", "/v1/system/remote-workspace", `{"version":7}`, tc.session, tc.bearer)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if repo.records["alice"].Version != original.Version || !repo.records["alice"].Active || !repo.records["bob"].Active {
				t.Fatal("denied request changed a connection")
			}
		})
	}
	if w := sendTest(h, "DELETE", "/v1/system/remote-workspace", `{"version":6}`, "alice", ""); w.Code != 409 {
		t.Fatalf("stale version: %d", w.Code)
	}
	// A URL query cannot substitute another local account's connection.
	if w := sendTest(h, "DELETE", "/v1/system/remote-workspace?owner=bob&organization_id=company-b", `{"version":7}`, "alice", ""); w.Code != 200 {
		t.Fatalf("authorized disconnect: %d %s", w.Code, w.Body.String())
	}
	if repo.records["alice"].Active || repo.records["alice"].Version != 8 || repo.records["bob"].Version != 3 || !repo.records["bob"].Active {
		t.Fatal("wrong owner changed")
	}
}
