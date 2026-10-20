package api

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"testing"
)

type salesFixture struct {
	*clientManagerFixture
	role      string
	placement system.SalesPlacement
	writes    int
}

func (f *salesFixture) SalesRole(_ context.Context, id string) (string, error) {
	if id == "bob" {
		return f.role, nil
	}
	return "", nil
}
func (f *salesFixture) SalesStructure(context.Context, string) (system.SalesStructure, error) {
	return system.SalesStructure{Version: 1, Members: []system.SalesMember{}}, nil
}
func (f *salesFixture) PlaceSalesMember(_ context.Context, _, _ string, in system.SalesPlacement) (system.SalesStructure, error) {
	f.placement = in
	f.writes++
	return system.SalesStructure{Version: 2, Members: []system.SalesMember{}}, nil
}
func (f *salesFixture) SalesTeam(_ context.Context, id string) ([]system.SalesMember, error) {
	if id != "bob" || system.SalesRank(f.role) < 2 {
		return nil, system.ErrForbidden
	}
	return []system.SalesMember{{ID: "bob", Role: f.role}}, nil
}

func TestSalesStructureAdministratorSessionAndInput(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &salesFixture{clientManagerFixture: &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
	s.system = f
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"System","scopes":["generation","system:manage"]}`)
	for _, tc := range []struct {
		session, key string
		status       int
	}{{"alice", "", 200}, {"bob", "", 403}, {"", key, 403}, {"", "", 401}} {
		w := sendTest(h, "GET", "/v1/system/sales", "", tc.session, tc.key)
		if w.Code != tc.status {
			t.Fatalf("read %+v: %d %s", tc, w.Code, w.Body.String())
		}
		if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable structure")
		}
	}
	path := "/v1/system/sales/11111111-1111-4111-8111-111111111111"
	for _, body := range []string{`{}`, `{"role":"system_admin","version":1}`, `{"role":"lead_client_manager","version":1}`, `{"role":"lead_client_manager","version":0}`, `{"role":"lead_client_manager","version":1,"supervisor_id":"invalid"}`, `{"role":"lead_client_manager","version":1,"extra":true}`} {
		if w := sendTest(h, "PUT", path, body, "alice", ""); w.Code != 400 {
			t.Fatalf("invalid %s: %d", body, w.Code)
		}
	}
	if f.writes != 0 {
		t.Fatal("invalid input mutated tree")
	}
	if w := sendTest(h, "PUT", path, `{"role":"client_manager","supervisor_id":"","version":1,"fill_vacancy":true}`, "alice", ""); w.Code != 400 || f.writes != 0 {
		t.Fatal("ordinary manager cannot fill a supervisor vacancy", w.Code)
	}
	if w := sendTest(h, "PUT", path, `{"role":"lead_client_manager","supervisor_id":"","version":1,"fill_vacancy":true}`, "alice", ""); w.Code != 200 || !f.placement.FillVacancy {
		t.Fatal("vacancy intent not passed to the repository", w.Code)
	}
	for _, body := range []string{
		`{"role":"client_manager","supervisor_id":"","version":1,"new_team":true}`,
		`{"role":"lead_client_manager","supervisor_id":"","version":1,"new_team":true,"fill_vacancy":true}`,
		`{"role":"lead_client_manager","supervisor_id":"","version":1,"new_team":"yes"}`,
	} {
		before := f.writes
		if w := sendTest(h, "PUT", path, body, "alice", ""); w.Code != 400 || f.writes != before {
			t.Fatalf("invalid new-team input: %s: %d", body, w.Code)
		}
	}
	for _, role := range []string{system.LeadClientManager, system.RegionalClientManager} {
		body := `{"role":"` + role + `","supervisor_id":"","version":1,"new_team":true}`
		if w := sendTest(h, "PUT", path, body, "alice", ""); w.Code != 200 || !f.placement.NewTeam || f.placement.FillVacancy {
			t.Fatalf("new-team intent not passed: %d %+v", w.Code, f.placement)
		}
		for _, actor := range []struct{ session, key string }{{"bob", ""}, {"", key}} {
			before := f.writes
			if w := sendTest(h, "PUT", path, body, actor.session, actor.key); w.Code != 403 || f.writes != before {
				t.Fatalf("new-team mutation without administrator session: %d", w.Code)
			}
		}
	}

	for _, role := range []string{system.ClientManager, system.LeadClientManager, system.RegionalClientManager} {
		body := `{"role":"` + role + `","supervisor_id":"","version":1}`
		if w := sendTest(h, "PUT", path, body, "alice", ""); w.Code != 200 {
			t.Fatalf("placement %s: %d %s", role, w.Code, w.Body.String())
		}
		f.manager = true
		f.assigned = "11111111-1111-4111-8111-111111111111"
		f.role = role
		if w := sendTest(h, "PUT", path, body, "bob", ""); w.Code != 403 {
			t.Fatalf("sales employee edited structure: %d", w.Code)
		}
		want := 200
		if role == system.ClientManager {
			want = 403
		}
		if w := sendTest(h, "GET", "/v1/sales/team", "", "bob", ""); w.Code != want {
			t.Fatalf("scoped team %s: %d %s", role, w.Code, w.Body.String())
		}
		transferStatus := 204
		if role == system.ClientManager {
			transferStatus = 403
		}
		if w := sendTest(h, "PUT", "/v1/organizations/11111111-1111-4111-8111-111111111111/client-manager", `{"user":"bob"}`, "bob", ""); w.Code != transferStatus {
			t.Fatalf("sales reassignment %s: %d %s", role, w.Code, w.Body.String())
		}
		if w := sendTest(h, "POST", "/v1/api-keys", `{}`, "bob", ""); w.Code != 403 {
			t.Fatalf("sales rank issued key: %d", w.Code)
		}
	}
	f.manager = false
	if w := sendTest(h, "PUT", path, `{"role":"lead_client_manager","version":1}`, "", key); w.Code != 403 {
		t.Fatal("API key edited tree", w.Code)
	}
}
