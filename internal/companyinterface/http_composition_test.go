package companyinterface_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

const fixtureCentral = installation.DefaultCentralOrigin
const fixtureLocal = "https://local.fixture.test"
const fixtureMaster = "isolated HTTP composition master key"

func fixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	base, e := postgres.Open(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "ci_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = base.Exec(`CREATE SCHEMA ` + schema); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, e := postgres.Open(context.Background(), u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close(); base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); base.Close() })
	if e = postgres.Migrate(db); e != nil {
		t.Fatal(e)
	}
	return db
}
func fixtureAuth(t *testing.T, db *sql.DB, origin string, admin bool) (*auth.Manager, identity.User, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	repo := postgres.NewAuthRepository(db)
	u, e := repo.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Fixture", "")
	if e != nil {
		t.Fatal(e)
	}
	if admin {
		if e = postgres.NewSystemRepository(db).SetAdmin(ctx, "", u.ID, true, true); e != nil {
			t.Fatal(e)
		}
	}
	token, _ := ci.NewSecret()
	hash := ci.Digest(token)
	if e = repo.CreateSession(ctx, hash[:], u.ID, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	manager, e := auth.New(ctx, repo, auth.Config{SessionSecret: fixtureMaster, PublicURL: origin})
	if e != nil {
		t.Fatal(e)
	}
	return manager, u, &http.Cookie{Name: "media_gateway_session", Value: token}
}

type fixtureWire struct {
	base           http.RoundTripper
	target         *url.URL
	lose           atomic.Bool
	loseCleanup    atomic.Bool
	loseRefresh    atomic.Bool
	loseLogout     atomic.Bool
	pauseRefresh   chan struct{}
	releaseRefresh chan struct{}
	calls          atomic.Int32
}

func (f *fixtureWire) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "console.sociaro.ai" {
		return nil, errors.New("unexpected destination")
	}
	if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Origin") != "" {
		return nil, errors.New("browser authority leaked")
	}
	f.calls.Add(1)
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = f.target.Scheme
	u.Host = f.target.Host
	copy.URL = &u
	response, e := f.base.RoundTrip(copy)
	if e == nil && r.URL.Path == ci.ProtocolBase+"/redeem" && response.StatusCode == 200 && f.lose.Swap(false) {
		response.Body.Close()
		return nil, errors.New("committed response lost")
	}
	if e == nil && r.URL.Path == ci.ProtocolBase+"/revoke-issued-family" && response.StatusCode == 200 && f.loseCleanup.Swap(false) {
		response.Body.Close()
		return nil, errors.New("committed cleanup response lost")
	}
	if e == nil && response.StatusCode == 200 && r.URL.Path == ci.ProtocolBase+"/refresh" {
		if f.pauseRefresh != nil {
			select {
			case f.pauseRefresh <- struct{}{}:
				<-f.releaseRefresh
			default:
			}
		}
		if f.loseRefresh.Swap(false) {
			response.Body.Close()
			return nil, errors.New("committed refresh response lost")
		}
	}
	if e == nil && response.StatusCode == 200 && r.URL.Path == ci.ProtocolBase+"/revoke-family" && f.loseLogout.Swap(false) {
		response.Body.Close()
		return nil, errors.New("committed logout response lost")
	}
	return response, e
}
func callFixture(t *testing.T, h http.Handler, origin, path, raw string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", origin+path, strings.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func expectFixture(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
}
func countFixture(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestCompanyHTTPCompositionLostReplyAndCapabilityGates(t *testing.T) {
	centralDB, localDB := fixtureDB(t), fixtureDB(t)
	centralAuth, _, centralCookie := fixtureAuth(t, centralDB, fixtureCentral, false)
	localAuth, owner, localCookie := fixtureAuth(t, localDB, fixtureLocal, true)
	centralRepo, e := postgres.NewCompanyInterfaceLifecycle(centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	localRepo, e := postgres.NewCompanyInterfaceLifecycle(localDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	centralHandler, e := api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: centralRepo, CompanyCentralOrigin: fixtureCentral, Auth: centralAuth}), "", fixtureCentral, "")
	if e != nil {
		t.Fatal(e)
	}
	var complete, old atomic.Bool
	centralHandler = ci.FixtureServerReadiness(centralHandler, complete.Load)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ci.ProtocolBase+"/capabilities" {
			if old.Load() {
				http.NotFound(w, r)
				return
			}
			if complete.Load() {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(ci.Protocol{Purpose: ci.Purpose, Version: ci.Version, Capabilities: ci.RequiredCapabilities()})
				return
			}
		}
		centralHandler.ServeHTTP(w, r)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	wire := &fixtureWire{base: server.Client().Transport, target: target}
	client := ci.NewFixtureClient(fixtureCentral, &http.Client{Transport: wire, Timeout: ci.ProtocolTimeout})
	coordinator, e := ci.NewCoordinator(localRepo, client, fixtureLocal)
	if e != nil {
		t.Fatal(e)
	}
	localHandler, e := api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: localRepo, CompanyCoordinator: coordinator, CompanyCentralOrigin: fixtureCentral, Auth: localAuth}), "", fixtureLocal, "")
	if e != nil {
		t.Fatal(e)
	}
	session := callFixture(t, localHandler, fixtureLocal, "/auth/company-interface/local/session", "{}")
	expectFixture(t, session, 200)
	browser := session.Result().Cookies()[0]
	if !browser.HttpOnly || !browser.Secure || browser.SameSite != http.SameSiteLaxMode {
		t.Fatal("weak browser cookie")
	}
	begin := func() *httptest.ResponseRecorder {
		return callFixture(t, localHandler, fixtureLocal, "/auth/company-interface/local/begin", `{"mode":"connect","consent":true}`, localCookie, browser)
	}
	// Real old and partial central HTTP replies, then complete central/local partial.
	for _, state := range []string{"old", "partial", "local_partial"} {
		t.Run(state, func(t *testing.T) {
			old.Store(state == "old")
			complete.Store(state == "local_partial")
			w := begin()
			expectFixture(t, w, 409)
			if !strings.Contains(w.Body.String(), "company_interface_update_required") {
				t.Fatal(w.Body.String())
			}
			for _, table := range []string{"remote_installation_identities", "company_interface_local_contexts", "company_interface_local_recovery", "company_interface_grants"} {
				if countFixture(t, localDB, table) != 0 {
					t.Fatal("capability rejection mutated", table)
				}
			}
			if countFixture(t, centralDB, "company_interface_attempts") != 0 {
				t.Fatal("central pending created")
			}
		})
	}
	old.Store(false)
	complete.Store(true)
	ci.FixtureCompleteProtocol(coordinator) // Test-only simulated future full implementation.

	// Reject malformed, unexpected, oversized and browser-origin proof requests
	// before a central pending row can be persisted.
	for _, raw := range []string{`{`, `{"Unexpected":"secret"}`, `{}` + `{}`, strings.Repeat("x", ci.MaxProtocolBody+1)} {
		request := httptest.NewRequest("POST", fixtureCentral+ci.ProtocolBase+"/start", strings.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		centralHandler.ServeHTTP(response, request)
		expectFixture(t, response, 403)
	}
	if countFixture(t, centralDB, "company_interface_attempts") != 0 {
		t.Fatal("malformed proof created attempt")
	}
	badOrigin := httptest.NewRequest("POST", fixtureLocal+"/auth/company-interface/local/begin", strings.NewReader(`{"mode":"connect","consent":true}`))
	badOrigin.Header.Set("Origin", "https://attacker.example")
	badOrigin.Header.Set("Content-Type", "application/json")
	badOrigin.AddCookie(localCookie)
	badOrigin.AddCookie(browser)
	denied := httptest.NewRecorder()
	localHandler.ServeHTTP(denied, badOrigin)
	expectFixture(t, denied, 403)
	w := begin()
	expectFixture(t, w, 200)
	var challenge struct {
		ID   string `json:"attempt_id"`
		Code string `json:"user_code"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &challenge); e != nil {
		t.Fatal(e)
	}
	// Repeated Begin recovers the same exact pending operation.
	again := begin()
	expectFixture(t, again, 200)
	if !bytes.Equal(w.Body.Bytes(), again.Body.Bytes()) {
		t.Fatal("pending attempt changed")
	}
	in, _, e := localRepo.LocalPending(context.Background(), browser.Value)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{in.Proof, in.InstallationProof, in.LocalSession} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("secret in browser response")
		}
	}
	inspect := `{"user_code":"` + challenge.Code + `"}`
	expectFixture(t, callFixture(t, centralHandler, fixtureCentral, "/v1/company-interface/authorizations/inspect", inspect, centralCookie), 200)
	// A distinct verified central account cannot approve an inspected attempt.
	_, _, otherCookie := fixtureAuth(t, centralDB, fixtureCentral, false)
	switchAttempt := `{"user_code":"` + challenge.Code + `","operation_id":"` + uuid.NewString() + `","consent":true}`
	switched := callFixture(t, centralHandler, fixtureCentral, "/v1/company-interface/authorizations/approve", switchAttempt, otherCookie)
	expectFixture(t, switched, 409)
	if countFixture(t, centralDB, "company_interface_grants") != 0 || countFixture(t, centralDB, "company_interface_sessions") != 0 {
		t.Fatal("account switch issued authority")
	}
	for _, secret := range []string{in.Proof, in.InstallationProof, in.LocalSession} {
		if strings.Contains(switched.Body.String(), secret) {
			t.Fatal("account switch disclosed secret")
		}
	}
	var pinnedActor, state string
	if e = centralDB.QueryRow(`SELECT actor_id::text,state FROM company_interface_attempts WHERE id=$1`, challenge.ID).Scan(&pinnedActor, &state); e != nil || state != "pending" {
		t.Fatal("account switch changed pending state", e)
	}
	expectedHash := ci.Digest(centralCookie.Value)
	expectedUser, e := postgres.NewAuthRepository(centralDB).UserBySession(context.Background(), expectedHash[:], time.Now())
	if e != nil || pinnedActor != expectedUser.ID {
		t.Fatal("account switch rebound actor", e)
	}

	approval := `{"user_code":"` + challenge.Code + `","operation_id":"` + uuid.NewString() + `","consent":true}`
	expectFixture(t, callFixture(t, centralHandler, fixtureCentral, "/v1/company-interface/authorizations/approve", approval, centralCookie), 200)
	poll := `{"attempt_id":"` + challenge.ID + `"}`
	wire.lose.Store(true)
	expectFixture(t, callFixture(t, localHandler, fixtureLocal, "/auth/company-interface/local/poll", poll, browser), 503)
	if countFixture(t, centralDB, "company_interface_grants") != 1 || countFixture(t, localDB, "company_interface_local_recovery") != 1 {
		t.Fatal("lost reply custody")
	}
	// Reconstruct persistence and coordinator, as after a process restart.
	localRepo, e = postgres.NewCompanyInterfaceLifecycle(localDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	restarted, e := ci.NewCoordinator(localRepo, client, fixtureLocal)
	if e != nil {
		t.Fatal(e)
	}
	ci.FixtureCompleteProtocol(restarted)
	if e = restarted.Poll(context.Background(), browser.Value, challenge.ID); e != nil {
		t.Fatal("restart redemption", e)
	}
	if countFixture(t, centralDB, "company_interface_grants") != 1 || countFixture(t, centralDB, "company_interface_sessions") != 1 {
		t.Fatal("duplicate grant/family")
	}
	stored, e := localRepo.LocalInstallation(context.Background())
	if e != nil || stored.Company == "" {
		t.Fatal("permanent custody", e)
	}
	if owner.ID == stored.Company {
		t.Fatal("local owner became company identity")
	}

	// New approved grant cancelled before local activation: retain installation
	// custody, revoke only its user family, and never create a different company.
	newOrigin := "https://replacement.fixture.test"
	reboundCoordinator, e := ci.NewCoordinator(localRepo, client, newOrigin)
	if e != nil {
		t.Fatal(e)
	}
	ci.FixtureCompleteProtocol(reboundCoordinator)
	reboundHandler, e := api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: localRepo, CompanyCoordinator: reboundCoordinator, CompanyCentralOrigin: fixtureCentral, Auth: localAuth}), "", newOrigin, "")
	if e != nil {
		t.Fatal(e)
	}
	nextSession := callFixture(t, reboundHandler, newOrigin, "/auth/company-interface/local/session", "{}")
	expectFixture(t, nextSession, 200)
	nextBrowser := nextSession.Result().Cookies()[0]
	rebind := callFixture(t, reboundHandler, newOrigin, "/auth/company-interface/local/begin", `{"mode":"rebind","consent":true}`, localCookie, nextBrowser)
	expectFixture(t, rebind, 200)
	var next struct {
		ID   string `json:"attempt_id"`
		Code string `json:"user_code"`
	}
	json.Unmarshal(rebind.Body.Bytes(), &next)
	expectFixture(t, callFixture(t, centralHandler, fixtureCentral, "/v1/company-interface/authorizations/inspect", `{"user_code":"`+next.Code+`"}`, centralCookie), 200)
	expectFixture(t, callFixture(t, centralHandler, fixtureCentral, "/v1/company-interface/authorizations/approve", `{"user_code":"`+next.Code+`","operation_id":"`+uuid.NewString()+`","consent":true}`, centralCookie), 200)
	cancelBody := `{"attempt_id":"` + next.ID + `"}`
	wire.loseCleanup.Store(true)
	expectFixture(t, callFixture(t, reboundHandler, newOrigin, "/auth/company-interface/local/cancel", cancelBody, nextBrowser), 503)
	recovery, e := localRepo.RecoverLocalAttempt(context.Background(), next.ID)
	if e != nil || recovery.State != "quarantined" {
		t.Fatal("uncertain cancel discarded proof", e)
	}
	if e = reboundCoordinator.Cancel(context.Background(), nextBrowser.Value, next.ID); e != nil {
		t.Fatal("cancel retry", e)
	}
	recovered, e := localRepo.LocalInstallation(context.Background())
	if e != nil || recovered.GrantID == stored.GrantID || recovered.Company != stored.Company {
		t.Fatal("permanent rebind custody", e)
	}
	var liveSessions int
	if e = centralDB.QueryRow(`SELECT count(*) FROM company_interface_sessions WHERE grant_id=$1 AND revoked_at IS NULL`, recovered.GrantID).Scan(&liveSessions); e != nil || liveSessions != 0 {
		t.Fatal("family not revoked", e)
	}
	if _, _, e = localRepo.LocalPending(context.Background(), nextBrowser.Value); e == nil {
		t.Fatal("cancelled pending still active")
	}
}

// Exercise the actual legacy creator, including its proof encryption, then use
// the new persistence adapter after restart. No manually seeded proof ciphertext.
func TestCompanyHTTPLegacyCreatorProofReuse(t *testing.T) {
	db := fixtureDB(t)
	_, owner, _ := fixtureAuth(t, db, fixtureLocal, true)
	var observed string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Device string `json:"device_code"`
			Proof  string `json:"installation_proof"`
		}
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			t.Error("legacy request")
		}
		observed = v.Proof
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"user_code": wa.UserCode(v.Device)})
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	wire := &fixtureWire{base: server.Client().Transport, target: target}
	cipher, _ := providerconfig.NewCipher(fixtureMaster)
	manager := remote.New(postgres.NewSystemRepository(db), cipher, &http.Client{Transport: wire})
	ctx := identity.WithUser(context.Background(), owner)
	if _, e := manager.BeginAuthorization(ctx, owner.ID, fixtureCentral, "Fixture", fixtureLocal, 0); e != nil {
		t.Fatal(e)
	}
	repo, e := postgres.NewCompanyInterfaceLifecycle(db, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	first, e := repo.LocalPreparation(ctx, owner.ID, "legacy-upgrade")
	if e != nil {
		t.Fatal(e)
	}
	repo, e = postgres.NewCompanyInterfaceLifecycle(db, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	second, e := repo.LocalPreparation(ctx, owner.ID, "legacy-upgrade")
	if e != nil {
		t.Fatal(e)
	}
	if observed == "" || first.InstallationProof != observed || second.InstallationProof != observed || countFixture(t, db, "remote_installation_identities") != 1 {
		t.Fatal("legacy identity was replaced")
	}
}

func TestCompanyHTTPCancelApprovedWithoutLiveUserAuthority(t *testing.T) {
	for _, scenario := range []string{"login", "login_custody_mismatch", "expired_access", "removed_member", "expired_recovery"} {
		t.Run(scenario, func(t *testing.T) {
			centralDB, localDB := fixtureDB(t), fixtureDB(t)
			centralAuth, actor, centralCookie := fixtureAuth(t, centralDB, fixtureCentral, false)
			localAuth, _, localCookie := fixtureAuth(t, localDB, fixtureLocal, true)
			centralRepo, e := postgres.NewCompanyInterfaceLifecycle(centralDB, fixtureMaster, fixtureCentral)
			if e != nil {
				t.Fatal(e)
			}
			localRepo, e := postgres.NewCompanyInterfaceLifecycle(localDB, fixtureMaster, fixtureCentral)
			if e != nil {
				t.Fatal(e)
			}
			central, e := api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: centralRepo, CompanyCentralOrigin: fixtureCentral, Auth: centralAuth}), "", fixtureCentral, "")
			if e != nil {
				t.Fatal(e)
			}
			central = fixtureReadyCentral(central)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == ci.ProtocolBase+"/capabilities" {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(ci.Protocol{Purpose: ci.Purpose, Version: ci.Version, Capabilities: ci.RequiredCapabilities()})
					return
				}
				central.ServeHTTP(w, r)
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			wire := &fixtureWire{base: server.Client().Transport, target: target}
			client := ci.NewFixtureClient(fixtureCentral, &http.Client{Transport: wire, Timeout: ci.ProtocolTimeout})
			coordinator, e := ci.NewCoordinator(localRepo, client, fixtureLocal)
			if e != nil {
				t.Fatal(e)
			}
			ci.FixtureCompleteProtocol(coordinator)
			local, e := api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: localRepo, CompanyCoordinator: coordinator, CompanyCentralOrigin: fixtureCentral, Auth: localAuth}), "", fixtureLocal, "")
			if e != nil {
				t.Fatal(e)
			}
			begin := func(mode string) (string, *http.Cookie) {
				t.Helper()
				session := callFixture(t, local, fixtureLocal, "/auth/company-interface/local/session", "{}")
				expectFixture(t, session, 200)
				browser := session.Result().Cookies()[0]
				response := callFixture(t, local, fixtureLocal, "/auth/company-interface/local/begin", `{"mode":"`+mode+`","consent":true}`, browser, localCookie)
				expectFixture(t, response, 200)
				var v struct {
					ID   string `json:"attempt_id"`
					Code string `json:"user_code"`
				}
				if e := json.Unmarshal(response.Body.Bytes(), &v); e != nil {
					t.Fatal(e)
				}
				expectFixture(t, callFixture(t, central, fixtureCentral, "/v1/company-interface/authorizations/inspect", `{"user_code":"`+v.Code+`"}`, centralCookie), 200)
				expectFixture(t, callFixture(t, central, fixtureCentral, "/v1/company-interface/authorizations/approve", `{"user_code":"`+v.Code+`","operation_id":"`+uuid.NewString()+`","consent":true}`, centralCookie), 200)
				return v.ID, browser
			}
			attempt, browser := begin("connect")
			var unrelatedFamily string
			if strings.HasPrefix(scenario, "login") {
				expectFixture(t, callFixture(t, local, fixtureLocal, "/auth/company-interface/local/poll", `{"attempt_id":"`+attempt+`"}`, browser), 200)
				if e = centralDB.QueryRow(`SELECT session_id::text FROM company_interface_attempts WHERE id=$1`, attempt).Scan(&unrelatedFamily); e != nil {
					t.Fatal(e)
				}
				attempt, browser = begin("login")
			}
			var family, grant, company string
			if e = centralDB.QueryRow(`SELECT a.session_id::text,a.grant_id::text,g.organization_id::text FROM company_interface_attempts a JOIN company_interface_grants g ON g.id=a.grant_id WHERE a.id=$1`, attempt).Scan(&family, &grant, &company); e != nil {
				t.Fatal(e)
			}
			if scenario == "expired_access" {
				if _, e = centralDB.Exec(`UPDATE company_interface_sessions SET access_expires_at=clock_timestamp()-INTERVAL '1 minute' WHERE id=$1`, family); e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "removed_member" {
				if _, e = centralDB.Exec(`UPDATE organization_members SET removed_at=clock_timestamp() WHERE organization_id=$1 AND user_id=$2`, company, actor.ID); e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "expired_recovery" {
				if _, e = centralDB.Exec(`UPDATE company_interface_attempts SET expires_at=clock_timestamp()-INTERVAL '2 hours',recovery_expires_at=clock_timestamp()-INTERVAL '1 hour' WHERE id=$1`, attempt); e != nil {
					t.Fatal(e)
				}
			}
			pending, _, e := localRepo.LocalPending(context.Background(), browser.Value)
			if e != nil {
				t.Fatal(e)
			}
			proof := ci.Redemption{AttemptID: pending.AttemptID, OperationID: uuid.NewString(), InstallationProof: pending.InstallationProof, Proof: pending.Proof, LocalSession: pending.LocalSession, LocalOrigin: pending.LocalOrigin, ConnectionRevision: pending.ConnectionRevision}
			for _, field := range []string{"attempt", "installation", "proof", "browser", "origin", "revision"} {
				bad := proof
				switch field {
				case "attempt":
					bad.AttemptID = uuid.NewString()
				case "installation":
					bad.InstallationProof, _ = ci.NewSecret()
				case "proof":
					bad.Proof, _ = ci.NewSecret()
				case "browser":
					bad.LocalSession, _ = ci.NewSecret()
				case "origin":
					bad.LocalOrigin = "https://other.fixture.test"
				case "revision":
					bad.ConnectionRevision++
				}
				if err := client.RevokeIssuedFamily(context.Background(), bad); !errors.Is(err, ci.ErrDenied) {
					t.Fatal("substituted cleanup admitted", field, err)
				}
			}
			var untouched bool
			if e = centralDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, family).Scan(&untouched); e != nil || untouched {
				t.Fatal("negative cleanup revoked family", e)
			}
			var originalCipher []byte
			if scenario == "login_custody_mismatch" {
				if e = localDB.QueryRow(`SELECT grant_ciphertext FROM company_interface_local_control WHERE singleton=TRUE`).Scan(&originalCipher); e != nil {
					t.Fatal(e)
				}
				if _, e = localDB.Exec(`UPDATE company_interface_local_control SET grant_ciphertext=$1 WHERE singleton=TRUE`, []byte("tampered fixture custody")); e != nil {
					t.Fatal(e)
				}
			}
			var keySnapshot string
			if e = centralDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(k) ORDER BY id)::text,'[]') FROM user_api_keys k`).Scan(&keySnapshot); e != nil {
				t.Fatal(e)
			}
			beforeKeys := countFixture(t, centralDB, "user_api_keys")
			payload := `{"attempt_id":"` + attempt + `"}`
			if scenario == "expired_recovery" {
				response := callFixture(t, local, fixtureLocal, "/auth/company-interface/local/cancel", payload, browser)
				expectFixture(t, response, 403)
				recovery, err := localRepo.RecoverLocalAttempt(context.Background(), attempt)
				if err != nil || recovery.State != "quarantined" {
					t.Fatal("expired recovery discarded custody", err)
				}
				if e = centralDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, family).Scan(&untouched); e != nil || untouched {
					t.Fatal("expired proof revoked family", e)
				}
				return
			}
			wire.loseCleanup.Store(true)
			lost := callFixture(t, local, fixtureLocal, "/auth/company-interface/local/cancel", payload, browser)
			expectFixture(t, lost, 503)
			recovery, err := localRepo.RecoverLocalAttempt(context.Background(), attempt)
			if err != nil || recovery.State != "quarantined" {
				t.Fatal("lost cleanup discarded proof", err)
			}
			// Restart the server-side coordinator after the uncertain commit.
			restartedRepo, err := postgres.NewCompanyInterfaceLifecycle(localDB, fixtureMaster, fixtureCentral)
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := ci.NewCoordinator(restartedRepo, client, fixtureLocal)
			if err != nil {
				t.Fatal(err)
			}
			restartedHandler, err := api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: restartedRepo, CompanyCoordinator: restarted, CompanyCentralOrigin: fixtureCentral, Auth: localAuth}), "", fixtureLocal, "")
			if err != nil {
				t.Fatal(err)
			}
			response := callFixture(t, restartedHandler, fixtureLocal, "/auth/company-interface/local/cancel", payload, browser)
			if scenario == "login_custody_mismatch" {
				expectFixture(t, response, 403)
				recovery, err := localRepo.RecoverLocalAttempt(context.Background(), attempt)
				if err != nil || recovery.State != "quarantined" {
					t.Fatal("mismatched login custody discarded proof", err)
				}
				if _, e = localDB.Exec(`UPDATE company_interface_local_control SET grant_ciphertext=$1 WHERE singleton=TRUE`, originalCipher); e != nil {
					t.Fatal(e)
				}
				response = callFixture(t, restartedHandler, fixtureLocal, "/auth/company-interface/local/cancel", payload, browser)
			}
			expectFixture(t, response, 200)
			// Same proof cleanup is idempotent; its repeated reply cannot advance
			// a revoked family's epoch or affect any other issued family.
			var terminalEpoch int64
			if e = centralDB.QueryRow(`SELECT session_epoch FROM company_interface_sessions WHERE id=$1`, family).Scan(&terminalEpoch); e != nil {
				t.Fatal(e)
			}
			if e = client.RevokeIssuedFamily(context.Background(), proof); e != nil {
				t.Fatal("exact cleanup replay", e)
			}
			var replayEpoch int64
			if e = centralDB.QueryRow(`SELECT session_epoch FROM company_interface_sessions WHERE id=$1`, family).Scan(&replayEpoch); e != nil || replayEpoch != terminalEpoch {
				t.Fatal("cleanup replay changed epoch", e)
			}

			var revoked bool
			if e = centralDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, family).Scan(&revoked); e != nil || !revoked {
				t.Fatal("issued family not revoked", e)
			}
			if e = centralDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_grants WHERE id=$1`, grant).Scan(&revoked); e != nil || revoked {
				t.Fatal("permanent grant revoked", e)
			}
			if unrelatedFamily != "" {
				if e = centralDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, unrelatedFamily).Scan(&revoked); e != nil || revoked {
					t.Fatal("unrelated family revoked", e)
				}
			}
			stored, e := localRepo.LocalInstallation(context.Background())
			if e != nil || stored.GrantID != grant || stored.Company != company {
				t.Fatal("permanent custody missing", e)
			}
			if countFixture(t, centralDB, "user_api_keys") != beforeKeys {
				t.Fatal("cleanup altered keys")
			}
			var afterKeys string
			if e = centralDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(k) ORDER BY id)::text,'[]') FROM user_api_keys k`).Scan(&afterKeys); e != nil || afterKeys != keySnapshot {
				t.Fatal("cleanup changed independent API key state", e)
			}
			if strings.Contains(response.Body.String(), stored.Credential) {
				t.Fatal("credential exposed")
			}
		})
	}
}

func fixtureReadyCentral(h http.Handler) http.Handler {
	return ci.FixtureServerReadiness(h, func() bool { return true })
}
