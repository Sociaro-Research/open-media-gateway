package companyinterface_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
)

type facadeWire struct {
	base        http.RoundTripper
	target      *url.URL
	calls       atomic.Int32
	lose        atomic.Bool
	loseRefresh atomic.Bool
	after       func()
	complete    atomic.Bool
}

func (w *facadeWire) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "console.sociaro.ai" {
		return nil, errors.New("wrong fixed destination")
	}
	customer := strings.HasPrefix(r.URL.Path, ci.CustomerBrowserPrefix) || strings.HasPrefix(r.URL.Path, ci.CustomerSDKPrefix)
	if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || r.Header.Get("Forwarded") != "" || (!customer && r.Header.Get("Authorization") != "") {
		return nil, errors.New("caller authority leaked")
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = w.target.Scheme
	u.Host = w.target.Host
	copy.URL = &u
	if customer {
		w.calls.Add(1)
	}
	response, e := w.base.RoundTrip(copy)
	if e == nil && r.URL.Path == ci.ProtocolBase+"/refresh" && response.StatusCode == 200 && w.loseRefresh.Swap(false) {
		response.Body.Close()
		return nil, errors.New("committed refresh reply lost")
	}
	if e == nil && customer {
		if w.after != nil {
			w.after()
		}
		if w.lose.Swap(false) {
			response.Body.Close()
			return nil, errors.New("customer response lost")
		}
	}
	return response, e
}
func facadeHTTP(t *testing.T) (*activeHTTPFixture, *facadeWire) {
	t.Helper()
	f := activeHTTP(t)
	f.central = customerHandler(t, f, postgres.NewCompanyInterfaceAuthority(f.centralDB))
	wire := &facadeWire{}
	wire.complete.Store(true)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ci.ProtocolBase+"/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			protocol := ci.ImplementedProtocol()
			if wire.complete.Load() {
				protocol = ci.Protocol{Purpose: ci.Purpose, Version: ci.Version, Capabilities: ci.RequiredCapabilities()}
			}
			json.NewEncoder(w).Encode(protocol)
			return
		}
		f.central.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	wire.base = server.Client().Transport
	wire.target, _ = url.Parse(server.URL)
	f.client = ci.NewFixtureClient(fixtureCentral, &http.Client{Transport: wire, Timeout: ci.ProtocolTimeout})
	f.restart(t)
	return f, wire
}
func facadeRequest(t *testing.T, f *activeHTTPFixture, method, path, body, key string, browser bool, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, fixtureLocal+"/auth/company-interface/local/customer/"+strings.TrimPrefix(path, "/"), strings.NewReader(body))
	if browser {
		r.AddCookie(f.browser)
		r.Header.Set("Origin", fixtureLocal)
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	f.local.ServeHTTP(w, r)
	return w
}
func TestCompanyFacadeCentralKeyAndBrowserIsolation(t *testing.T) {
	f, wire := facadeHTTP(t)
	v := f.current(t)
	if countFixture(t, f.localDB, "user_api_keys") != 0 {
		t.Fatal("fixture unexpectedly has local keys")
	}
	sdk := facadeRequest(t, f, "GET", "/v1/key", "", v.Tokens.PlaygroundKey, false, nil)
	expectFixture(t, sdk, 200)
	if !strings.Contains(sdk.Body.String(), v.Tokens.Company) || !strings.Contains(sdk.Body.String(), v.Tokens.Actor) {
		t.Fatal("SDK lost central recipient/company")
	}
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/me", "", "", true, func(r *http.Request) {
		r.Header.Set(ci.InstallationHeader, "attacker")
		r.Header.Set(ci.SessionHeader, "attacker")
		r.Header.Set("Forwarded", "host=foreign")
	}), 200)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/key", "", v.Tokens.PlaygroundKey, true, nil), 200)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/me", "", "invalid", true, nil), 403)
	for _, header := range []string{"Cookie", "Origin", "Referer", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User"} {
		before := wire.calls.Load()
		expectFixture(t, facadeRequest(t, f, "GET", "/v1/key", "", v.Tokens.PlaygroundKey, false, func(r *http.Request) { r.Header[header] = []string{""} }), 403)
		if wire.calls.Load() != before {
			t.Fatal("browser signal fell back to SDK", header)
		}
	}
	before := wire.calls.Load()
	expectFixture(t, facadeRequest(t, f, "PATCH", "/v1/me", `{"name":"rejected"}`, "", true, func(r *http.Request) { r.Header.Del("Origin") }), 403)
	if wire.calls.Load() != before {
		t.Fatal("mutation without origin dispatched")
	}
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/me", "", "", true, func(r *http.Request) { r.Header.Set("Origin", "https://foreign.fixture.test") }), 403)
	// Partial central capability cannot dispatch, refresh or change local custody.
	old := f.current(t)
	wire.complete.Store(false)
	before = wire.calls.Load()
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/me", "", "", true, nil), 409)
	current := f.current(t)
	if wire.calls.Load() != before || current.Tokens.Version != old.Tokens.Version || countFixture(t, f.localDB, "company_interface_local_operations") != 0 {
		t.Fatal("partial capability changed context")
	}
}
func TestCompanyFacadeLostMutationDoesNotRetryOrFallback(t *testing.T) {
	f, wire := facadeHTTP(t)
	v := f.current(t)
	wire.lose.Store(true)
	before := wire.calls.Load()
	expectFixture(t, facadeRequest(t, f, "PATCH", "/v1/me", `{"name":"central committed"}`, "", true, nil), 503)
	if wire.calls.Load() != before+1 {
		t.Fatal("uncertain mutation retried")
	}
	var name string
	if e := f.centralDB.QueryRow(`SELECT display_name FROM users WHERE id=$1`, v.Tokens.Actor).Scan(&name); e != nil || name != "central committed" {
		t.Fatalf("central mutation missing: %q %v", name, e)
	}
	var localCount int
	if e := f.localDB.QueryRow(`SELECT count(*) FROM users WHERE display_name='central committed'`).Scan(&localCount); e != nil || localCount != 0 {
		t.Fatal("local mutation fallback")
	}
}
func TestCompanyFacadeLateBodyCannotCrossLocalLogout(t *testing.T) {
	f, wire := facadeHTTP(t)
	v := f.current(t)
	wire.after = func() {
		wire.after = nil
		if _, e := f.repo.BeginLocalLogout(context.Background(), f.browser.Value, v.Epoch); e != nil {
			t.Fatal(e)
		}
	}
	response := facadeRequest(t, f, "GET", "/v1/me", "", "", true, nil)
	expectFixture(t, response, 409)
	if strings.Contains(response.Body.String(), v.Tokens.Actor) || strings.Contains(response.Body.String(), `"email"`) {
		t.Fatal("late local body released")
	}
}

func TestCompanyFacadeRefreshRecoversBeforeCustomerDispatch(t *testing.T) {
	f, wire := facadeHTTP(t)
	v := f.current(t)
	// Advance only the fixture's access deadline in central metadata and its
	// encrypted local copy; all proofs, epochs and actual approval stay intact.
	v.Tokens.AccessExpiresAt = time.Now().Add(20 * time.Second)
	cipher, e := ci.NewReceiptCipher(fixtureMaster)
	if e != nil {
		t.Fatal(e)
	}
	encrypted, e := cipher.Seal(v.Tokens, []any{ci.Purpose, ci.Version, "session", v.Tokens.LocalSessionHash, v.Epoch, v.Revision, v.Tokens.SessionID, v.Tokens.Version})
	if e != nil {
		t.Fatal(e)
	}
	hash := ci.Digest(f.browser.Value)
	if _, e = f.localDB.Exec(`UPDATE company_interface_local_contexts SET ciphertext=$1 WHERE local_session_hash=$2`, encrypted, hash[:]); e != nil {
		t.Fatal(e)
	}
	if _, e = f.centralDB.Exec(`UPDATE company_interface_sessions SET access_expires_at=$1 WHERE id=$2`, v.Tokens.AccessExpiresAt, v.Tokens.SessionID); e != nil {
		t.Fatal(e)
	}
	wire.loseRefresh.Store(true)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/me", "", "", true, nil), 503)
	if wire.calls.Load() != 0 || f.current(t).Tokens.Version != v.Tokens.Version {
		t.Fatal("uncertain refresh dispatched or adopted")
	}
	f.restart(t)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/me", "", "", true, nil), 200)
	if wire.calls.Load() != 1 || f.current(t).Tokens.Version != v.Tokens.Version+1 || countFixture(t, f.centralDB, "company_interface_rotations") != 1 {
		t.Fatal("refresh retry did not recover exact operation")
	}
}
func TestCompanyFacadeSDKLateBodyCannotCrossGlobalRevision(t *testing.T) {
	f, wire := facadeHTTP(t)
	v := f.current(t)
	wire.after = func() {
		wire.after = nil
		if e := f.repo.AdvanceLocalRevision(context.Background(), v.Revision); e != nil {
			t.Fatal(e)
		}
	}
	response := facadeRequest(t, f, "GET", "/v1/key", "", v.Tokens.PlaygroundKey, false, nil)
	expectFixture(t, response, 409)
	if strings.Contains(response.Body.String(), v.Tokens.Actor) || strings.Contains(response.Body.String(), v.Tokens.Company) {
		t.Fatal("old global SDK response released")
	}
}

func TestCompanyFacadeValidTrafficDoesNotConsumeAuthorizationAttemptBudget(t *testing.T) {
	f, _ := facadeHTTP(t)
	// All users behind an installation share a server peer. Ordinary profile
	// traffic must not inherit the authorization-attempt 120/min peer ceiling.
	for i := 0; i < 121; i++ {
		response := facadeRequest(t, f, "GET", "/v1/me", "", "", true, nil)
		if response.Code != 200 {
			t.Fatalf("valid request %d incorrectly limited: %d", i+1, response.Code)
		}
	}
}
