package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

type companyHTTPBoundary struct {
	ci.Lifecycle
	calls, rateCalls int
	limited          bool
}

func (f *companyHTTPBoundary) AllowAttempt(context.Context, []byte, int, time.Duration) (bool, error) {
	f.rateCalls++
	return !f.limited, nil
}
func (f *companyHTTPBoundary) Start(context.Context, ci.Start) (ci.Challenge, error) {
	f.calls++
	return ci.Challenge{}, ci.ErrDenied
}

type companyLocalBoundary struct{ ci.LocalStore }

func TestCompanyHTTPProofHeadersBudgetsAndRate(t *testing.T) {
	fixture := &companyHTTPBoundary{}
	handler, e := WithPublicAPI(NewGateway(Dependencies{CompanyInterface: fixture, CompanyCentralOrigin: installation.DefaultCentralOrigin}), "", installation.DefaultCentralOrigin, "")
	if e != nil {
		t.Fatal(e)
	}
	for _, header := range []string{"Origin", "Cookie", "Referer", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Authorization", "X-API-Key"} {
		t.Run(header, func(t *testing.T) {
			r := httptest.NewRequest("POST", installation.DefaultCentralOrigin+ci.ProtocolBase+"/start", strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set(header, "untrusted")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 403 || fixture.calls != 0 || fixture.rateCalls != 0 {
				t.Fatal("browser authority admitted", w.Code, fixture.calls, fixture.rateCalls)
			}
		})
	}
	for _, tc := range []struct{ query, content, body string }{{"?proof=secret", "application/json", "{}"}, {"", "application/json-malformed", "{}"}, {"", "application/json", `{"unknown":"secret"}`}, {"", "application/json", "{} {}"}, {"", "application/json", strings.Repeat("x", ci.MaxProtocolBody+1)}} {
		r := httptest.NewRequest("POST", installation.DefaultCentralOrigin+ci.ProtocolBase+"/start"+tc.query, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 || fixture.calls != 0 || strings.Contains(w.Body.String(), "secret") {
			t.Fatal("invalid proof request reached lifecycle", w.Code, fixture.calls)
		}
	}
	fixture.limited = true
	r := httptest.NewRequest("POST", installation.DefaultCentralOrigin+ci.ProtocolBase+"/start", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" || fixture.calls != 0 {
		t.Fatal("rate boundary", w.Code, fixture.calls)
	}
}
func TestCompanyHTTPMissingLocalDependencyFailsClosed(t *testing.T) {
	client, e := ci.NewClient(installation.DefaultCentralOrigin)
	if e != nil {
		t.Fatal(e)
	}
	coordinator, e := ci.NewCoordinator(companyLocalBoundary{}, client, "https://local.fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	h, e := WithPublicAPI(NewGateway(Dependencies{CompanyCoordinator: coordinator}), "", "https://local.fixture.test", "")
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "https://local.fixture.test/auth/company-interface/local/session", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://local.fixture.test")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 || len(w.Result().Cookies()) != 0 {
		t.Fatal("incomplete composition advertised readiness", w.Code)
	}
}
