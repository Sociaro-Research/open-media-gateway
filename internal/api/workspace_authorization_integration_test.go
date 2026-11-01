package api

import (
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

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

func workspaceDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "workspace_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); base.Close() })
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

type workspaceMailbox struct{ link string }

func (m *workspaceMailbox) Send(_ context.Context, _, _, link, _ string) error {
	m.link = link
	return nil
}

type failedWorkspaceBody struct{}

func (failedWorkspaceBody) Read([]byte) (int, error) { return 0, errors.New("synthetic lost response") }
func (failedWorkspaceBody) Close() error             { return nil }

type loseWorkspaceReply struct {
	base http.RoundTripper
	lost atomic.Bool
}

func (t *loseWorkspaceReply) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" && strings.HasPrefix(r.URL.Path, "/auth/workspace/") {
		return nil, errors.New("local credential leaked")
	}
	response, err := t.base.RoundTrip(r)
	if err == nil && r.URL.Path == "/auth/workspace/poll" && response.StatusCode == 200 && t.lost.CompareAndSwap(false, true) {
		response.Body.Close()
		response.Body = failedWorkspaceBody{}
	}
	return response, err
}
func TestWorkspaceSignupCompanyAndInstallationEndToEnd(t *testing.T) {
	ctx := context.Background()
	hostDB, localDB := workspaceDatabase(t), workspaceDatabase(t)
	hostPeople, hostSystem := postgres.NewAuthRepository(hostDB), postgres.NewSystemRepository(hostDB)
	localPeople, localSystem := postgres.NewAuthRepository(localDB), postgres.NewSystemRepository(localDB)
	seed := func(people *postgres.AuthRepository, name string) identity.User {
		t.Helper()
		u, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	hostAdmin, localOwner := seed(hostPeople, "Host admin"), seed(localPeople, "Local owner")
	if err := hostSystem.SetAdmin(ctx, "", hostAdmin.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if err := localSystem.SetAdmin(ctx, "", localOwner.ID, true, true); err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("a", 32))
	mailbox := &workspaceMailbox{}
	hostAuth, err := auth.New(ctx, hostPeople, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://remote.fixture.test", EmailSender: mailbox})
	if err != nil {
		t.Fatal(err)
	}
	localAuth, err := auth.New(ctx, localPeople, auth.Config{SessionSecret: strings.Repeat("l", 32), PublicURL: "https://local.fixture.test"})
	if err != nil {
		t.Fatal(err)
	}
	hostHandler := NewGateway(Dependencies{Service: &accountService{}, Health: healthStub{}, Auth: hostAuth, System: hostSystem, Keys: postgres.NewAPIKeyRepository(hostDB), WorkspaceAuthorization: wa.New(hostSystem, cipher)})
	host := httptest.NewTLSServer(hostHandler)
	defer host.Close()
	transport := &loseWorkspaceReply{base: host.Client().Transport}
	client := *host.Client()
	client.Transport = transport
	manager := remote.New(localSystem, cipher, &client)
	localHandler := NewGateway(Dependencies{Service: &accountService{}, Health: healthStub{}, Auth: localAuth, System: localSystem, Keys: postgres.NewAPIKeyRepository(localDB), RemoteWorkspace: manager})
	localToken, _ := wa.DeviceCode()
	if err = localPeople.CreateSession(ctx, wa.Digest(localToken), localOwner.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	localCookie := &http.Cookie{Name: "media_gateway_session", Value: localToken}
	call := func(h http.Handler, method, path string, value any, cookie *http.Cookie, bearer string, cross bool) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(value)
		request := httptest.NewRequest(method, "https://fixture.test"+path, strings.NewReader(string(data)))
		request.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cross {
			request.Header.Set("Origin", "https://attacker.invalid")
			request.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d wanted %d: %s", w.Code, status, w.Body)
		}
	}
	decode := func(w *httptest.ResponseRecorder, target any) {
		t.Helper()
		if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
			t.Fatal(err)
		}
	}
	count := func(db *sql.DB, table string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	begin := map[string]any{"origin": host.URL, "client_name": "My private installation", "client_origin": "http://192.168.1.8:8088", "version": 0, "consent": false}
	expect(call(localHandler, "POST", "/v1/system/remote-workspace/authorization", begin, localCookie, "", false), 400)
	if count(hostDB, "workspace_authorizations") != 0 || count(localDB, "remote_workspace_authorizations") != 0 {
		t.Fatal("preview/absent consent wrote authorization")
	}
	begin["consent"] = true
	expect(call(localHandler, "POST", "/v1/system/remote-workspace/authorization", begin, localCookie, "", false), 403)
	settings, _ := hostSystem.Settings(ctx)
	settings.CustomerWorkspaces = true
	settings.SharedResources = true
	if _, err = hostSystem.SaveSettings(ctx, hostAdmin.ID, settings); err != nil {
		t.Fatal(err)
	}
	w := call(localHandler, "POST", "/v1/system/remote-workspace/authorization", begin, localCookie, "", false)
	expect(w, 200)
	var view remote.AuthorizationView
	decode(w, &view)
	if view.Code == "" || view.ID == "" || strings.Contains(w.Body.String(), "device_code") || strings.Contains(w.Body.String(), "ciphertext") {
		t.Fatal("unsafe authorization view")
	}
	again := call(localHandler, "POST", "/v1/system/remote-workspace/authorization", begin, localCookie, "", false)
	expect(again, 200)
	var retried remote.AuthorizationView
	decode(again, &retried)
	if retried.ID != view.ID || retried.Code != view.Code || count(hostDB, "workspace_authorizations") != 1 {
		t.Fatal("start retry changed authorization")
	}
	inspect := map[string]any{"user_code": view.Code}
	expect(call(hostHandler, "POST", "/v1/workspace-authorizations/inspect", inspect, nil, "", false), 401)
	// Reuse actual email-first registration. No user or permission before verification.
	beforeUsers := count(hostDB, "users")
	registration := call(hostHandler, "POST", "/auth/email/register", map[string]string{"email": "customer@fixture.test"}, nil, "", false)
	expect(registration, 202)
	if count(hostDB, "users") != beforeUsers || mailbox.link == "" {
		t.Fatal("unverified registration acquired an account")
	}
	parsed, _ := url.Parse(mailbox.link)
	emailToken := strings.TrimPrefix(parsed.Fragment, "email_verify=")
	verified := call(hostHandler, "POST", "/auth/email/verify", map[string]string{"token": emailToken}, nil, "", false)
	expect(verified, 200)
	var registrationCookie *http.Cookie
	for _, cookie := range verified.Result().Cookies() {
		if cookie.Name == "media_gateway_registration" {
			registrationCookie = cookie
		}
	}
	if registrationCookie == nil {
		t.Fatal("no restricted registration cookie")
	}
	password := "Synthetic private passphrase 123!"
	completed := call(hostHandler, "POST", "/auth/email/complete", map[string]string{"password": password}, registrationCookie, "", false)
	expect(completed, 200)
	expect(call(hostHandler, "POST", "/auth/email/login", map[string]string{"email": "customer@fixture.test", "password": "incorrect"}, nil, "", false), 401)
	logged := call(hostHandler, "POST", "/auth/email/login", map[string]string{"email": "customer@fixture.test", "password": password}, nil, "", false)
	expect(logged, 200)
	var customer *http.Cookie
	for _, cookie := range logged.Result().Cookies() {
		if cookie.Name == "media_gateway_session" {
			customer = cookie
		}
	}
	if customer == nil || !customer.HttpOnly || !customer.Secure {
		t.Fatal("sign-in did not reuse secure session")
	}
	me := call(hostHandler, "GET", "/v1/me", nil, customer, "", false)
	expect(me, 200)
	var user identity.User
	decode(me, &user)
	inspected := call(hostHandler, "POST", "/v1/workspace-authorizations/inspect", inspect, customer, "", false)
	expect(inspected, 200)
	if strings.Contains(inspected.Body.String(), localToken) {
		t.Fatal("local token leaked")
	}
	approval := map[string]any{"user_code": view.Code, "company_name": "Customer company", "company_website": "https://customer.fixture.test", "consent": false}
	expect(call(hostHandler, "POST", "/v1/workspace-authorizations/approve", approval, customer, "", false), 403)
	approval["consent"] = true
	expect(call(hostHandler, "POST", "/v1/workspace-authorizations/approve", approval, customer, "", true), 403)
	approved := call(hostHandler, "POST", "/v1/workspace-authorizations/approve", approval, customer, "", false)
	expect(approved, 200)
	expect(call(hostHandler, "POST", "/v1/workspace-authorizations/approve", approval, customer, "", false), 200)
	if count(hostDB, "organizations") != 1 || count(hostDB, "user_api_keys") != 0 || count(hostDB, "organization_credits") != 0 {
		t.Fatal("consent duplicated/funded company or issued key before device proof")
	}
	var accepted wa.Challenge
	decode(approved, &accepted)
	// The remote key commits, but the first device response is lost. Retrying must
	// recover that same credential, rather than issuing another installation key.
	poll := map[string]string{"id": view.ID}
	expect(call(localHandler, "POST", "/v1/system/remote-workspace/authorization/poll", poll, localCookie, "", false), 502)
	if count(hostDB, "user_api_keys") != 1 || count(localDB, "remote_workspaces") != 0 {
		t.Fatal("lost reply state is wrong")
	}
	if _, err = hostDB.Exec(`UPDATE workspace_authorizations SET next_poll_at=NOW()-INTERVAL '1 second'`); err != nil {
		t.Fatal(err)
	}
	connected := call(localHandler, "POST", "/v1/system/remote-workspace/authorization/poll", poll, localCookie, "", false)
	expect(connected, 200)
	if count(hostDB, "user_api_keys") != 1 || count(localDB, "tasks") != 0 || count(localDB, "organizations") != 0 || count(localDB, "organization_credits") != 0 {
		t.Fatal("duplicate grant or local tenant/billing mirror")
	}
	var record remote.Record
	decode(connected, &record)
	if !record.Active || record.OrganizationID != accepted.Company || strings.Contains(connected.Body.String(), "gw_") {
		t.Fatal("connection not verified or token exposed")
	}
	saved, err := localSystem.RemoteWorkspace(ctx, localOwner.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cipher.Decrypt(saved.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	credential := string(raw)
	keys, err := postgres.NewAPIKeyRepository(hostDB).List(ctx, user.ID)
	if err != nil || len(keys) != 1 || len(keys[0].Scopes) != 1 || keys[0].Scopes[0] != apikey.Generation || keys[0].ResourceMode != "shared" || keys[0].ExpiresAt == nil {
		t.Fatal("wrong installation authority", err)
	}
	expect(call(hostHandler, "POST", "/v1/workspace-authorizations/inspect", inspect, nil, credential, false), 403)
	expect(call(hostHandler, "POST", "/v1/organizations", map[string]string{"name": "Escalation"}, nil, credential, false), 403)
	list := call(hostHandler, "GET", "/v1/workspace-installations", nil, customer, "", false)
	expect(list, 200)
	if strings.Contains(list.Body.String(), credential) {
		t.Fatal("grant list returned secret")
	}
	stranger := seed(hostPeople, "Stranger")
	strangerToken, _ := wa.DeviceCode()
	if err = hostPeople.CreateSession(ctx, wa.Digest(strangerToken), stranger.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	strangerCookie := &http.Cookie{Name: "media_gateway_session", Value: strangerToken}
	expect(call(hostHandler, "DELETE", "/v1/workspace-installations/"+saved.KeyID, nil, strangerCookie, "", false), 410)
	expect(call(hostHandler, "DELETE", "/v1/workspace-installations/"+saved.KeyID, nil, customer, "", false), 200)
	expect(call(localHandler, "GET", "/v1/system/remote-workspace/proxy/v1/key?workspace_context="+url.QueryEscape(saved.SnapshotID()), nil, localCookie, "", false), 401)
	if count(hostDB, "organizations") != 1 {
		t.Fatal("revocation deleted company")
	}
	// The normal grant key is usable only for its actual remote company.
	var valid bool
	if err = hostDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_api_keys WHERE id=$1 AND user_id=$2 AND organization_id=$3 AND revoked_at IS NOT NULL)`, saved.KeyID, user.ID, accepted.Company).Scan(&valid); err != nil || !valid {
		t.Fatal("tenant binding/revocation lost", err)
	}
}
