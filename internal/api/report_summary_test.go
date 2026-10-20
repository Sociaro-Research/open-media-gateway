package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func TestReportSummaryScopeFiltersAndPagination(t *testing.T) {
	s, _ := newPersonalTestServer()
	service := &exportReportService{reportTestService: &reportTestService{accountService: s.service.(*accountService)}, count: 151, markup: 2500}
	s.service = service
	w := sendTest(s.handler(), "GET", "/v1/admin/reports?view=summary&interval=day&limit=1&offset=50&from=2026-03-29&to=2026-03-29&timezone=Europe%2FNicosia&user=case%40example.com", "", "alice", "")
	var result core.ReportSummary
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Requests != 151 || result.Spend != 6606250 {
		t.Fatal(w.Code, w.Body)
	}
	if service.owner != identity.PersonalOwnerID("alice") || service.filter.UserEmail != "case@example.com" || service.filter.Limit != 0 || service.filter.Offset != 0 {
		t.Fatal(service.filter, service.owner)
	}
	for _, query := range []string{"view=other", "view=summary&interval=year", "view=summary&format=csv"} {
		if w := sendTest(s.handler(), "GET", "/v1/admin/reports?"+query, "", "alice", ""); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	if w := sendTest(s.handler(), "GET", "/v1/admin/reports?view=summary", "", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	// A member cannot override the author restriction by supplying another user.
	r := httptest.NewRequest("GET", "/v1/organizations/team/reports?view=summary&from=2026-03-29&to=2026-03-29&user=other%40example.com", nil)
	ctx := identity.WithOrganization(identity.WithUser(r.Context(), identity.User{ID: "alice"}), "team", false)
	out := httptest.NewRecorder()
	s.costReport(out, r.WithContext(ctx))
	if out.Code != 200 || service.filter.UserEmail != "" || service.owner != "team" {
		t.Fatal(out.Code, service.filter, service.owner)
	}
}
