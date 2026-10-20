package api

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"strings"
	"testing"
	"time"
)

type reportTestService struct {
	*accountService
	filter core.ReportFilter
	owner  string
}

func (s *reportTestService) CostReport(ctx context.Context, f core.ReportFilter) ([]core.CostReportRow, error) {
	s.filter = f
	s.owner = identity.OwnerID(ctx)
	return []core.CostReportRow{}, nil
}
func TestReportsRequireManagementAndValidateDates(t *testing.T) {
	s, _ := newPersonalTestServer()
	service := &reportTestService{accountService: s.service.(*accountService)}
	s.service = service
	h := s.handler()
	_, generation := issueTestKey(t, h, "alice", `{"name":"App"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"Admin","scopes":["generation","keys:manage"]}`)
	for _, tc := range []struct {
		query, session, key string
		status              int
	}{
		{"", "", "", 401}, {"", "", generation, 403}, {"", "", "service-secret", 403},
		{"?from=2026-09-01&to=2026-09-07", "alice", "", 200}, {"?from=2026-09-01&to=2026-09-01", "", manager, 200},
		{"?from=2026-09-08&to=2026-09-07", "alice", "", 400}, {"?from=2026-02-30", "alice", "", 400},
		{"?generation_id=bad", "alice", "", 400}, {"?api_key_id=bad", "alice", "", 400}, {"?limit=0", "alice", "", 400}, {"?offset=-1", "alice", "", 400},
	} {
		w := sendTest(h, "GET", "/v1/admin/reports"+tc.query, "", tc.session, tc.key)
		if w.Code != tc.status {
			t.Fatalf("%+v got %d %s", tc, w.Code, w.Body.String())
		}
	}
	if service.owner != identity.PersonalOwnerID("alice") || !service.filter.Until.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("owner=%s filter=%+v", service.owner, service.filter)
	}
}

func TestReportGenerationIDFilter(t *testing.T) {
	s, _ := newPersonalTestServer()
	service := &reportTestService{accountService: s.service.(*accountService)}
	s.service = service
	id := "abcdefab-1111-4111-8111-abcdefabcdef"
	w := sendTest(s.handler(), "GET", "/v1/admin/reports?generation_id="+strings.ToUpper(id), "", "alice", "")
	if w.Code != 200 || service.filter.GenerationID != id || service.owner != identity.PersonalOwnerID("alice") {
		t.Fatal(w.Code, service.filter, service.owner)
	}
}
