package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"net/http"
	"testing"
	"time"

	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type clientManagerFixture struct {
	*systemFixture
	org.Repository
	manager     bool
	assigned    string
	from, until time.Time
}

func (f *clientManagerFixture) OrganizationsWithUsage(_ context.Context, _ string, _ bool, from, until time.Time) ([]org.Organization, error) {
	f.from, f.until = from, until
	if f.assigned == "" {
		return []org.Organization{}, nil
	}
	return []org.Organization{{ID: f.assigned, Role: "client_manager", Usage: &org.UsageSummary{}}}, nil
}

func TestClientManagerCompanySummary(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, manager: true, assigned: "11111111-1111-4111-8111-111111111111"}
	s.system = f
	h := s.handler()
	w := sendTest(h, "GET", "/v1/organizations?view=summary&from=2026-03-08&to=2026-03-08&timezone=America/New_York", "", "bob", "")
	if w.Code != 200 {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}
	if f.until.Sub(f.from) != 23*time.Hour || f.from.UTC().Hour() != 5 {
		t.Fatalf("inclusive local day over DST: %v to %v", f.from, f.until)
	}
	var response struct {
		Data []org.Organization `json:"data"`
		AsOf time.Time          `json:"as_of"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Data) != 1 || response.Data[0].Usage == nil || response.AsOf.IsZero() {
		t.Fatalf("summary contract: %+v %v", response, err)
	}
	for _, query := range []string{"view=invalid", "view=summary&timezone=Invalid", "view=summary&from=invalid", "view=summary&to=invalid", "view=summary&from=2026-10-08&to=2026-10-07"} {
		if w := sendTest(h, "GET", "/v1/organizations?"+query, "", "bob", ""); w.Code != 400 {
			t.Fatalf("invalid filter %s: %d", query, w.Code)
		}
	}
	if w := sendTest(h, "GET", "/v1/organizations?view=summary&all=true", "", "bob", ""); w.Code != 403 {
		t.Fatalf("manager cannot widen scope: %d", w.Code)
	}
	f.assigned = ""
	w = sendTest(h, "GET", "/v1/organizations?view=summary", "", "bob", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Data) != 0 {
		t.Fatalf("removed assignment: %d %s", w.Code, w.Body.String())
	}
}

func (f *clientManagerFixture) IsClientManager(_ context.Context, id string) (bool, error) {
	return id == "bob" && f.manager, nil
}
func (f *clientManagerFixture) SetClientManager(context.Context, string, string, bool) error {
	return nil
}
func (f *clientManagerFixture) AssignClientManager(context.Context, string, string, string) error {
	return nil
}
func (f *clientManagerFixture) OrganizationAccess(_ context.Context, actor, id string) (string, error) {
	if actor == "bob" && id == f.assigned {
		return "client_manager", nil
	}
	return "", org.ErrForbidden
}
func (f *clientManagerFixture) Organizations(context.Context, string, bool) ([]org.Organization, error) {
	return []org.Organization{{ID: f.assigned, Role: "client_manager"}}, nil
}
func (f *clientManagerFixture) OrganizationMembers(context.Context, string, string) ([]org.Member, error) {
	return []org.Member{}, nil
}
func (f *clientManagerFixture) OrganizationCredits(context.Context, string, string, int) ([]org.Credit, error) {
	return []org.Credit{}, nil
}

func TestClientManagerReadOnlyAllowlist(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.storageAdmin = &storageAdminStub{}
	service := &reportTestService{accountService: s.service.(*accountService)}
	s.service = service
	s.references = &references.Library{}
	s.mediaSigner = newMediaURLSigner("fixture", time.Hour)
	// Retain a valid fixture key to verify the role guard independently of DB revocation.
	_, key := issueTestKey(t, s.handler(), "bob", `{"name":"Old key","scopes":["generation","keys:manage","organizations:manage"]}`)
	assigned := "11111111-1111-4111-8111-111111111111"
	f := &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, manager: true, assigned: assigned}
	s.system = f
	h := s.handler()
	for _, path := range []string{"/v1/me", "/v1/organizations", "/v1/organizations/" + assigned, "/v1/organizations/" + assigned + "/members", "/v1/organizations/" + assigned + "/credits", "/v1/organizations/" + assigned + "/reports"} {
		if w := sendTest(h, "GET", path, "", "bob", ""); w.Code != 200 {
			t.Fatalf("read %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if service.owner != assigned || service.filter.AllOrganizations {
		t.Fatalf("report scope: %s %+v", service.owner, service.filter)
	}
	for _, tc := range []struct{ method, path string }{
		{"POST", "/v1/api-keys"}, {"GET", "/v1/api-keys"}, {"POST", "/v1/generations"}, {"POST", "/v1/generations/estimate"}, {"POST", "/v1/videos"}, {"POST", "/v1/references"}, {"GET", "/v1/references"}, {"GET", "/v1/providers"}, {"GET", "/v1/generations"}, {"DELETE", "/v1/generations/anything/content"},
		{"PUT", "/v1/admin/storage"}, {"GET", "/v1/admin/providers"}, {"GET", "/v1/system/settings"}, {"POST", "/v1/organizations"}, {"PUT", "/v1/organizations/" + assigned}, {"POST", "/v1/organizations/" + assigned + "/credits"}, {"POST", "/v1/invitations/accept"}, {"GET", "/v1/organizations/" + assigned + "/invitations"}, {"GET", "/v1/organizations/" + assigned + "/generations"}, {"PUT", "/v1/organizations/" + assigned + "/client-manager"},
		{"GET", "/v1/organizations/22222222-2222-4222-8222-222222222222/reports"},
	} {
		for _, credential := range []string{"", key} {
			w := sendTest(h, tc.method, tc.path, `{}`, "bob", credential)
			if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s key=%t: %d %s", tc.method, tc.path, credential != "", w.Code, w.Body.String())
			}
		}
	}
	f.assigned = ""
	if w := sendTest(h, "GET", "/v1/organizations/"+assigned+"/reports", "", "bob", ""); w.Code != 403 {
		t.Fatalf("removed assignment: %d", w.Code)
	}
}
