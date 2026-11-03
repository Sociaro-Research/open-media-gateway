package companyinterface_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

type activeHTTPFixture struct {
	centralDB, localDB                  *sql.DB
	repo                                *postgres.CompanyInterfaceLifecycle
	central, local                      http.Handler
	localAuth                           *auth.Manager
	client                              *ci.Client
	wire                                *fixtureWire
	browser, centralCookie, localCookie *http.Cookie
}

func (f *activeHTTPFixture) restart(t *testing.T) {
	t.Helper()
	var e error
	f.repo, e = postgres.NewCompanyInterfaceLifecycle(f.localDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	coordinator, e := ci.NewCoordinator(f.repo, f.client, fixtureLocal)
	if e != nil {
		t.Fatal(e)
	}
	ci.FixtureCompleteProtocol(coordinator)
	f.local, e = api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: f.repo, CompanyCoordinator: coordinator, CompanyCentralOrigin: fixtureCentral, Auth: f.localAuth}), "", fixtureLocal, "")
	if e != nil {
		t.Fatal(e)
	}
}
func activeHTTP(t *testing.T) *activeHTTPFixture {
	t.Helper()
	f := &activeHTTPFixture{centralDB: fixtureDB(t), localDB: fixtureDB(t)}
	centralAuth, _, cookie := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	f.centralCookie = cookie
	localAuth, _, localCookie := fixtureAuth(t, f.localDB, fixtureLocal, true)
	f.localAuth = localAuth
	f.localCookie = localCookie
	centralRepo, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	f.central, e = api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: centralRepo, CompanyCentralOrigin: fixtureCentral, Auth: centralAuth}), "", fixtureCentral, "")
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ci.ProtocolBase+"/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ci.Protocol{Purpose: ci.Purpose, Version: ci.Version, Capabilities: ci.RequiredCapabilities()})
			return
		}
		f.central.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	f.wire = &fixtureWire{base: server.Client().Transport, target: target}
	f.client = ci.NewFixtureClient(fixtureCentral, &http.Client{Transport: f.wire, Timeout: ci.ProtocolTimeout})
	f.restart(t)
	session := callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/session", "{}")
	expectFixture(t, session, 200)
	f.browser = session.Result().Cookies()[0]
	f.authorize(t, "connect")
	return f
}
func (f *activeHTTPFixture) authorize(t *testing.T, mode string) {
	t.Helper()
	response := callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/begin", `{"mode":"`+mode+`","consent":true}`, f.browser, f.localCookie)
	expectFixture(t, response, 200)
	var v struct {
		ID   string `json:"attempt_id"`
		Code string `json:"user_code"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	expectFixture(t, callFixture(t, f.central, fixtureCentral, "/v1/company-interface/authorizations/inspect", `{"user_code":"`+v.Code+`"}`, f.centralCookie), 200)
	expectFixture(t, callFixture(t, f.central, fixtureCentral, "/v1/company-interface/authorizations/approve", `{"user_code":"`+v.Code+`","operation_id":"`+uuid.NewString()+`","consent":true}`, f.centralCookie), 200)
	expectFixture(t, callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/poll", `{"attempt_id":"`+v.ID+`"}`, f.browser), 200)
}
func (f *activeHTTPFixture) current(t *testing.T) ci.ActiveContext {
	t.Helper()
	v, e := f.repo.LocalActive(context.Background(), f.browser.Value)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func refreshBody(v ci.ActiveContext) string {
	return fmt.Sprintf(`{"context_epoch":%d,"token_version":%d}`, v.Epoch, v.Tokens.Version)
}
func logoutBody(v ci.ActiveContext) string { return fmt.Sprintf(`{"context_epoch":%d}`, v.Epoch) }
func TestCompanyHTTPActiveRefreshAndLogoutLostReplies(t *testing.T) {
	f := activeHTTP(t)
	before := f.current(t)
	f.wire.loseRefresh.Store(true)
	expectFixture(t, callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/refresh", refreshBody(before), f.browser), 503)
	unchanged := f.current(t)
	if unchanged.Tokens.Version != before.Tokens.Version {
		t.Fatal("uncertain refresh adopted")
	}
	f.restart(t)
	expectFixture(t, callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/refresh", refreshBody(before), f.browser), 200)
	next := f.current(t)
	if next.Tokens.Version != before.Tokens.Version+1 {
		t.Fatal("lost refresh duplicated version")
	}
	if countFixture(t, f.centralDB, "company_interface_rotations") != 1 || countFixture(t, f.localDB, "company_interface_local_operations") != 1 {
		t.Fatal("lost reply duplicated operation")
	}
	// Concurrent tabs name the same expected version, never silently rotate again.
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			responses <- callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/refresh", refreshBody(next), f.browser)
		}()
	}
	wg.Wait()
	close(responses)
	successes := 0
	for w := range responses {
		if w.Code == 200 {
			successes++
		} else if w.Code != 409 {
			t.Fatal("concurrent rotation", w.Code, w.Body.String())
		}
	}
	current := f.current(t)
	if successes < 1 || current.Tokens.Version != next.Tokens.Version+1 {
		t.Fatal("concurrent tabs changed wrong version")
	}
	f.wire.loseLogout.Store(true)
	expectFixture(t, callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/logout", logoutBody(current), f.browser), 503)
	if _, e := f.repo.LocalActive(context.Background(), f.browser.Value); e == nil {
		t.Fatal("uncertain logout left active context")
	}
	f.restart(t)
	response := callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/logout", logoutBody(current), f.browser)
	expectFixture(t, response, 200)
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("logout callback can clobber successor browser nonce")
	}
	var pending int
	if e := f.localDB.QueryRow(`SELECT count(*) FROM company_interface_local_operations WHERE state='pending'`).Scan(&pending); e != nil || pending != 0 {
		t.Fatal("logout proof not settled", e)
	}
	stored, e := f.repo.LocalInstallation(context.Background())
	if e != nil || stored.GrantID != current.Tokens.GrantID {
		t.Fatal("logout removed permanent custody", e)
	}
}
func TestCompanyHTTPActiveDelayedRefreshCannotUndoLogout(t *testing.T) {
	f := activeHTTP(t)
	before := f.current(t)
	f.wire.pauseRefresh = make(chan struct{}, 1)
	f.wire.releaseRefresh = make(chan struct{})
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/refresh", refreshBody(before), f.browser)
	}()
	select {
	case <-f.wire.pauseRefresh:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not commit")
	}
	expectFixture(t, callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/logout", logoutBody(before), f.browser), 200)
	close(f.wire.releaseRefresh)
	expectFixture(t, <-result, 409)
	if _, e := f.repo.LocalActive(context.Background(), f.browser.Value); e == nil {
		t.Fatal("late refresh reactivated logout")
	}
	var revoked bool
	if e := f.centralDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, before.Tokens.SessionID).Scan(&revoked); e != nil || !revoked {
		t.Fatal("rotated family survived logout", e)
	}
	// A stale old logout callback cannot clear a new per-user context.
	f.authorize(t, "login")
	successor := f.current(t)
	if successor.Epoch <= before.Epoch || successor.Tokens.SessionID == before.Tokens.SessionID {
		t.Fatal("new login reused old context")
	}
	stale := callFixture(t, f.local, fixtureLocal, "/auth/company-interface/local/logout", logoutBody(before), f.browser)
	if stale.Code != 409 && stale.Code != 403 {
		t.Fatal("stale logout response", stale.Code)
	}
	still := f.current(t)
	if still.Tokens.SessionID != successor.Tokens.SessionID {
		t.Fatal("stale logout touched successor")
	}
}
