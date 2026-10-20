package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

type delayedReportService struct {
	*exportReportService
	delay time.Duration
}

func (s *delayedReportService) WalkCostReport(ctx context.Context, f core.ReportFilter, visit func(core.CostReportRow) error) error {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 55*time.Second {
		return context.DeadlineExceeded
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.delay):
		return s.exportReportService.WalkCostReport(ctx, f, visit)
	}
}

func TestHeavyReportsSurviveOrdinaryConnectionWriteDeadline(t *testing.T) {
	for _, format := range []string{"view=summary&interval=day", "format=csv"} {
		t.Run(format, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			s.service = &delayedReportService{exportReportService: &exportReportService{reportTestService: &reportTestService{accountService: s.service.(*accountService)}, count: 2}, delay: 200 * time.Millisecond}
			server := httptest.NewUnstartedServer(s.handler())
			server.Config.WriteTimeout = 50 * time.Millisecond
			server.Start()
			defer server.Close()
			request, _ := http.NewRequest("GET", server.URL+"/v1/admin/reports?"+format+"&from=2026-03-29&to=2026-03-29", nil)
			request.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal("ordinary write deadline interrupted a permitted report", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("report incomplete: %d %v", response.StatusCode, err)
			}
			if strings.Contains(format, "csv") {
				rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
				if err != nil || len(rows) != 3 || response.ContentLength != int64(len(body)) {
					t.Fatalf("CSV lost rows or length: rows=%d length=%d err=%v", len(rows), response.ContentLength, err)
				}
			} else {
				var summary core.ReportSummary
				if err := json.Unmarshal(body, &summary); err != nil || summary.Requests != 2 {
					t.Fatalf("summary lost rows: %+v %v", summary, err)
				}
			}
		})
	}
}

func TestCancelledReportWorkLeavesNoSuccessfulPartialCSV(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	s, _ := newPersonalTestServer()
	s.service = &delayedReportService{exportReportService: &exportReportService{reportTestService: &reportTestService{accountService: s.service.(*accountService)}, count: 2}, delay: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("GET", "/v1/admin/reports?format=csv", nil).WithContext(ctx)
	request.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
	response := httptest.NewRecorder()
	s.handler().ServeHTTP(response, request)
	if response.Code != 503 || strings.Contains(response.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("cancelled work reported CSV success: %d %s", response.Code, response.Body.String())
	}
	files, err := os.ReadDir(os.Getenv("TMPDIR"))
	if err != nil || len(files) != 0 {
		t.Fatalf("cancelled report left a spool: %v %v", files, err)
	}
}
