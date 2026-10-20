package api

import (
	"context"
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type exportReportService struct {
	*reportTestService
	count      int
	markup     money.Percent
	historical bool
}

func (s *exportReportService) WalkCostReport(ctx context.Context, f core.ReportFilter, visit func(core.CostReportRow) error) error {
	s.filter = f
	s.owner = identity.OwnerID(ctx)
	for i := 0; i < s.count; i++ {
		cost := money.USD(35000)
		charged, err := s.markup.Apply(cost)
		if err != nil {
			return err
		}
		if err := visit(core.CostReportRow{IdentityHistorical: s.historical, CreatedAt: time.Date(2026, 3, 29, 3, 0, 0, 0, time.FixedZone("test", 3*3600)), Model: "=1+1", UserEmail: "test@example.com", ConsumedUSD: &charged, Cost: &core.TaskCost{MarkupPercent: s.markup, ExpectedUSD: cost, MeteredUSD: &cost, ActualUSD: &cost}}); err != nil {
			return err
		}
	}
	return nil
}
func TestReportTimezonesEmailsAndCSV(t *testing.T) {
	s, _ := newPersonalTestServer()
	service := &exportReportService{reportTestService: &reportTestService{accountService: s.service.(*accountService)}, count: 151, markup: 2500}
	s.service = service
	h := s.handler()
	w := sendTest(h, "GET", "/v1/admin/reports?from=2026-03-29&to=2026-03-29&timezone=Europe%2FNicosia&user=Case%40example.com", "", "alice", "")
	if w.Code != 200 || service.filter.Until.Sub(service.filter.From) != 23*time.Hour || service.filter.UserEmail != "Case@example.com" {
		t.Fatalf("local day/email: %d %+v", w.Code, service.filter)
	}
	for _, query := range []string{"timezone=invalid", "user=invalid-id", "user=bad@@email", "format=pdf"} {
		if w = sendTest(h, "GET", "/v1/admin/reports?"+query, "", "alice", ""); w.Code != 400 {
			t.Fatalf("invalid query %s: %d", query, w.Code)
		}
	}
	w = sendTest(h, "GET", "/v1/admin/reports?format=csv&limit=1&offset=50&from=2026-03-29&to=2026-03-29&timezone=Europe%2FNicosia", "", "alice", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("export %d %s", w.Code, w.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || len(rows) != 152 {
		t.Fatalf("full export rows=%d err=%v", len(rows), err)
	}
	columns := map[string]string{}
	for i, name := range rows[0] {
		columns[name] = rows[1][i]
	}
	if columns["Provider charged (USD)"] != "" || columns["Cost (USD)"] != "0.043750" || columns["Markup (%)"] != "" {
		t.Fatalf("markup export: %v", columns)
	}
	if rows[1][0] != "2026-03-29T00:00:00Z" || rows[1][8] != "'=1+1" {
		t.Fatalf("UTC/formula safety %v", rows[1])
	}
	if columns["Identity source"] != "Current labels; historical identity unavailable" {
		t.Fatal("legacy identity not marked", columns)
	}
	service.historical = true
	r := httptest.NewRequest("GET", "/v1/organizations/team/reports?format=csv&user=someone%40example.com", nil)
	ctx := identity.WithOrganization(identity.WithUser(r.Context(), identity.User{ID: "alice"}), "team", false)
	out := httptest.NewRecorder()
	s.costReport(out, r.WithContext(ctx))
	rows, err = csv.NewReader(strings.NewReader(out.Body.String())).ReadAll()
	if err != nil || len(rows[0]) != 13 || rows[0][11] != "Cost (USD)" || rows[1][12] != "Captured at request time" || service.filter.UserEmail != "" {
		t.Fatalf("member export exposed financial fields: %v %v", rows, err)
	}
}

func TestOnlySystemAdminCSVContainsProviderPrices(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.service = &exportReportService{reportTestService: &reportTestService{accountService: s.service.(*accountService)}, count: 1, markup: 2500}
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"root": true}}
	for _, actor := range []string{"root", "org-admin", "member", "personal"} {
		r := httptest.NewRequest("GET", "/v1/admin/reports?format=csv", nil)
		ctx := identity.WithUser(r.Context(), identity.User{ID: actor})
		if actor == "org-admin" || actor == "member" {
			ctx = identity.WithOrganization(ctx, "org", actor == "org-admin")
		}
		out := httptest.NewRecorder()
		s.exportCostReport(out, r.WithContext(ctx), core.ReportFilter{})
		rows, err := csv.NewReader(strings.NewReader(out.Body.String())).ReadAll()
		if err != nil || out.Code != 200 {
			t.Fatal(out.Code, err, out.Body)
		}
		cols := map[string]string{}
		for i, name := range rows[0] {
			cols[name] = rows[1][i]
		}
		if actor == "root" {
			if cols["Provider charged (USD)"] != "0.035000" || cols["Markup (%)"] != "25.00" {
				t.Fatal(cols)
			}
		} else {
			if cols["Cost (USD)"] != "0.043750" || len(cols) != 13 {
				t.Fatal(actor, cols)
			}
		}
	}
}
