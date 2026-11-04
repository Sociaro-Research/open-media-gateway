package companyinterface_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

type companyReportWork struct {
	api.Service
	walker   core.ReportWalker
	delay    time.Duration
	copies   int
	fail     bool
	before   func(context.Context)
	mu       sync.Mutex
	deadline time.Duration
}

func (s *companyReportWork) WalkCostReport(ctx context.Context, f core.ReportFilter, visit func(core.CostReportRow) error) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("missing report deadline")
	}
	s.mu.Lock()
	s.deadline = time.Until(deadline)
	s.mu.Unlock()
	if s.before != nil {
		s.before(ctx)
	}
	timer := time.NewTimer(s.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return s.walker.WalkCostReport(ctx, f, func(row core.CostReportRow) error {
		for i := 0; i < s.copies; i++ {
			if s.copies > 1 {
				row.OrganizationName = strings.Repeat("report-volume-", 1000)
			}
			if e := visit(row); e != nil {
				return e
			}
		}
		if s.fail {
			return errors.New("fixture report failed after row")
		}
		return nil
	})
}
func TestCompanyReadReportPreparationAndLargeCSV(t *testing.T) {
	var work *companyReportWork
	f, store, _, ctx := mediaHTTPFixture(t, func(d *api.Dependencies) {
		work = &companyReportWork{Service: d.Service, walker: d.Service.(core.ReportWalker), delay: 31 * time.Second, copies: 700}
		d.Service = work
	})
	v := f.current(t)
	completeMediaTask(t, f, store, ctx, testmedia.PNG512("large report"))
	start := time.Now()
	got := facadeRequest(t, f, "GET", "/v1/organizations/"+v.Tokens.Company+"/reports?format=csv", "", "", true, func(r *http.Request) {
		r.Header.Set("X-Timeout", "3600")
		r.Header.Set("X-Company-Header-Timeout", "3600")
	})
	expectFixture(t, got, 200)
	work.mu.Lock()
	deadline := work.deadline
	work.mu.Unlock()
	if deadline < 54*time.Second || deadline > 55*time.Second || time.Since(start) < 31*time.Second || time.Since(start) > 59*time.Second {
		t.Fatal("report budget not preserved", deadline, time.Since(start))
	}
	if got.Body.Len() <= ci.MaxCustomerResponse || got.Header().Get("Content-Length") != strconv.Itoa(got.Body.Len()) || !strings.Contains(got.Header().Get("Content-Disposition"), "generation-report.csv") {
		t.Fatal("large report truncated", got.Body.Len(), got.Header())
	}
	for _, header := range []string{ci.MediaLengthHeader, ci.MediaCompleteHeader, "Trailer", "X-Timeout", "X-Company-Header-Timeout"} {
		if got.Header().Get(header) != "" {
			t.Fatal("internal budget/completion leaked", header)
		}
	}
	if ci.CustomerTimeout("GET", "/v1/info") != ci.ProtocolTimeout || ci.CustomerTimeout("GET", "/v1/generations/00000000-0000-0000-0000-000000000001/content") != ci.MediaTimeout || ci.CustomerHeaderTimeout("GET", "/v1/generations/00000000-0000-0000-0000-000000000001/content") != ci.MediaIdleTimeout {
		t.Fatal("other route budgets changed")
	}
}
func TestCompanyReadReportFailureAndEpochCannotCommit(t *testing.T) {
	for _, phase := range []string{"spool_failure", "logout_during_spool"} {
		t.Run(phase, func(t *testing.T) {
			var work *companyReportWork
			f, store, _, ctx := mediaHTTPFixture(t, func(d *api.Dependencies) {
				work = &companyReportWork{Service: d.Service, walker: d.Service.(core.ReportWalker), copies: 1}
				d.Service = work
			})
			v := f.current(t)
			completeMediaTask(t, f, store, ctx, testmedia.PNG512("withheld report"))
			if phase == "spool_failure" {
				work.fail = true
			} else {
				work.before = mutationLogout(t, f, v)
			}
			got := facadeRequest(t, f, "GET", "/v1/organizations/"+v.Tokens.Company+"/reports?format=csv", "", "", true, nil)
			if phase == "spool_failure" {
				expectFixture(t, got, 503)
			} else if got.Code != 403 && got.Code != 409 && got.Code != 503 {
				t.Fatal("revoked report completed", got.Code)
			}
			if strings.Contains(got.Body.String(), "Date (UTC)") || got.Header().Get("Content-Disposition") != "" || got.Header().Get(ci.MediaCompleteHeader) != "" {
				t.Fatal("failed report committed CSV", got.Code, got.Body.String())
			}
		})
	}
}
func TestCompanyReadExactContractMatrix(t *testing.T) {
	company := "00000000-0000-0000-0000-000000000002"
	p := ci.Principal{Grant: ci.Grant{Company: company}, Kind: ci.CentralSession}
	routes := []string{"/v1/info", "/v1/organizations/" + company + "/storage", "/v1/organizations/" + company + "/reports", "/v1/organizations/" + company + "/audit/events", "/v1/organizations/" + company + "/audit/events/1", "/v1/organizations/" + company + "/audit/revoked-keys"}
	for _, path := range routes {
		for _, method := range []string{"GET", "HEAD"} {
			if _, e := ci.RequestContract(method, path, "", nil, p); e != nil {
				t.Fatal(method, path, e)
			}
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			if _, e := ci.RequestContract(method, path, "", nil, p); e == nil {
				t.Fatal("unadmitted verb", method, path)
			}
		}
		for _, query := range []string{"unknown=1", "organization_id=" + company} {
			if _, e := ci.RequestContract("GET", path, query, nil, p); e == nil {
				t.Fatal("extra query", path, query)
			}
		}
	}
	for _, query := range []string{"format=csv&format=csv", "view=summary&view=summary"} {
		if _, e := ci.RequestContract("GET", routes[2], query, nil, p); e == nil {
			t.Fatal("duplicate query")
		}
	}
	for _, suffix := range []string{"/audit/events/0", "/audit/events/+1", "/audit/events/01", "/audit/events/9223372036854775808", "/audit/events/1/content", "/storage/check"} {
		if ci.Classify("GET", "/v1/organizations/"+company+suffix).Class == ci.CentralCustomer {
			t.Fatal("neighbor admitted", suffix)
		}
	}
	if c := ci.Classify("GET", routes[2]); c.Timeout != time.Minute || c.HeaderTimeout != time.Minute || !c.Stream || c.RequestBody != "" {
		t.Fatal(fmt.Sprintf("report contract%+v", c))
	}
}
