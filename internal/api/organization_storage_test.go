package api

import (
	"context"
	"encoding/json"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type organizationStorageFixture struct{ *organizationFixture }

func (f *organizationStorageFixture) OrganizationAccess(_ context.Context, actor, id string) (string, error) {
	if actor == "alice" {
		return "admin", nil
	}
	return "developer", nil
}

type organizationStorageMemory struct {
	settings map[string]storage.Settings
	writes   int
	checks   int
}

func (m *organizationStorageMemory) Get(ctx context.Context) (storage.Settings, error) {
	s, ok := m.settings[identity.StorageOwnerID(ctx)]
	if !ok {
		s.Driver = "gateway"
	}
	return s, nil
}
func (m *organizationStorageMemory) Update(ctx context.Context, u storage.Update) (storage.Settings, error) {
	s := storage.Settings{Driver: u.Driver}
	m.settings[identity.StorageOwnerID(ctx)] = s
	m.writes++
	return s, nil
}
func (m *organizationStorageMemory) Check(context.Context, storage.Update) error {
	m.checks++
	return nil
}
func TestOrganizationStoragePermissionsAndExistingKeyRouting(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &organizationStorageFixture{&organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
	f.settings.SharedResources = true
	s.system = f
	m := &organizationStorageMemory{settings: map[string]storage.Settings{}}
	s.storageAdmin = m
	service := &sharedRoutingService{}
	s.service = service
	h := s.handler()
	path := "/v1/organizations/" + fixtureOrg + "/storage"
	_, key := issueTestKey(t, h, "bob", `{"name":"Existing","organization_id":"`+fixtureOrg+`"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"Manager","organization_id":"`+fixtureOrg+`","scopes":["generation","organizations:manage"]}`)
	for _, method := range []string{"GET", "PUT"} {
		w := sendTest(h, method, path, `{"driver":"s3"}`, "bob", "")
		if w.Code != 403 {
			t.Fatalf("member %s: %d", method, w.Code)
		}
	}
	if w := sendTest(h, "PUT", path, `{"driver":"local"}`, "alice", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := sendTest(h, "GET", path, "", "", key); w.Code != 403 {
		t.Fatal("generation key read credentials", w.Code)
	}
	if w := sendTest(h, "PUT", "/v1/organizations/"+fixtureOtherOrg+"/storage", `{"driver":"s3"}`, "", manager); w.Code != 403 {
		t.Fatal("cross-org config", w.Code)
	}
	for _, driver := range []string{"gateway", "s3", "gateway"} {
		if w := sendTest(h, "PUT", path, `{"driver":"`+driver+`"}`, "", manager); w.Code != 200 {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
		if w := sendTest(h, "GET", "/v1/providers", "", "", key); w.Code != 200 {
			t.Fatalf("route: %d %s", w.Code, w.Body.String())
		}
		want := identity.SharedOwnerID
		if driver == "s3" {
			want = fixtureOrg
		}
		if service.storageOwner != want || service.consumer != fixtureOrg {
			t.Fatalf("routing %s: %s %s", driver, service.storageOwner, service.consumer)
		}
	}
	_, personal := issueTestKey(t, h, "bob", `{"name":"Personal","resource_mode":"personal"}`)
	m.settings["bob"] = storage.Settings{Driver: "s3"}
	if w := sendTest(h, "GET", "/v1/providers", "", "", personal); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if service.storageOwner != "bob" {
		t.Fatal("personal key used organization storage", service.storageOwner)
	}
	f.settings.PersonalProviders = false
	if w := sendTest(h, "GET", "/v1/admin/storage", "", "bob", ""); w.Code != 403 {
		t.Fatal("personal storage bypassed system policy", w.Code)
	}
	if m.writes != 3 {
		t.Fatal("unauthorized settings mutation", m.writes)
	}
}

// OrganizationAccess gives system administrators precedence over membership roles.
type systemOrganizationStorageFixture struct {
	*organizationFixture
	membership string
}

func (f *systemOrganizationStorageFixture) OrganizationAccess(context.Context, string, string) (string, error) {
	return "system_admin", nil
}
func (f *systemOrganizationStorageFixture) Organizations(context.Context, string, bool) ([]org.Organization, error) {
	if f.membership == "" {
		return nil, nil
	}
	return []org.Organization{{ID: fixtureOrg, Role: f.membership}}, nil
}
func TestSystemAdministratorOrganizationStorageRequiresAdminMembership(t *testing.T) {
	for _, role := range []string{"", "member", "admin"} {
		t.Run("membership_"+role, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := &systemOrganizationStorageFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, membership: role}
			s.system = f
			m := &organizationStorageMemory{settings: map[string]storage.Settings{fixtureOrg: {Driver: "s3", Bucket: "private-bucket", AccessKeyID: "private-access", Endpoint: "https://private.example.test", CredentialsConfigured: true}}}
			s.storageAdmin = m
			h := s.handler()
			path := "/v1/organizations/" + fixtureOrg + "/storage"
			w := sendTest(h, "GET", path, "", "alice", "")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var result struct {
				CanManage bool           `json:"can_manage"`
				Settings  map[string]any `json:"settings"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.CanManage != (role == "admin") || result.Settings["driver"] != "s3" {
				t.Fatal(result)
			}
			if role != "admin" && len(result.Settings) != 1 {
				t.Fatal("storage details exposed", result.Settings)
			}
			if role == "admin" && result.Settings["bucket"] != "private-bucket" {
				t.Fatal("org admin cannot read configuration")
			}
			for _, method := range []string{"PUT", "POST"} {
				target := path
				if method == "POST" {
					target += "/check"
				}
				w = sendTest(h, method, target, `{"driver":"s3"}`, "alice", "")
				want := 403
				if role == "admin" {
					want = 200
				}
				if w.Code != want {
					t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
				}
			}
			want := 0
			if role == "admin" {
				want = 1
			}
			if m.writes != want || m.checks != want {
				t.Fatal("unauthorized storage operation", m.writes, m.checks)
			}
		})
	}
}
