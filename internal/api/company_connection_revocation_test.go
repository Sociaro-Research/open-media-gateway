package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

func TestCompanyRevocationRequiresTypedBoundedUnauthorizedJSON(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		contentType, body string
		want              bool
	}{
		{"typed", 401, "application/json", `{"error":{"code":"api_key_revoked"}}`, true},
		{"text not evidence", 401, "application/json", `{"error":{"message":"This API key has been revoked"}}`, false},
		{"generic", 401, "application/json", `{"error":{"code":"unauthorized"}}`, false},
		{"forbidden", 403, "application/json", `{"error":{"code":"api_key_revoked"}}`, false},
		{"server failure", 503, "application/json", `{"error":{"code":"api_key_revoked"}}`, false},
		{"redirect", 302, "application/json", `{"error":{"code":"api_key_revoked"}}`, false},
		{"wrong media type", 401, "text/html", `{"error":{"code":"api_key_revoked"}}`, false},
		{"malformed", 401, "application/json", `{"error":{"code":"api_key_revoked"}`, false},
		{"oversized", 401, "application/json", `{"error":{"code":"api_key_revoked"},"padding":"` + strings.Repeat("x", 4096) + `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			if got := confirmedCompanyKeyRevocation(response); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			restored, _ := io.ReadAll(response.Body)
			if string(restored) != tc.body {
				t.Fatal("consumed unrelated response bytes")
			}
		})
	}
}

// Both application databases and every HTTP destination are disposable. Real
// central authentication and the local proxy run unchanged through their routes.
func TestCompanyRevocationRealRoutesPersistenceAndReconnect(t *testing.T) {
	ctx := context.Background()
	hostDB, localDB := workspaceDatabase(t), workspaceDatabase(t)
	hostPeople, localPeople := postgres.NewAuthRepository(hostDB), postgres.NewAuthRepository(localDB)
	hostSystem, localSystem := postgres.NewSystemRepository(hostDB), postgres.NewSystemRepository(localDB)
	seed := func(repo *postgres.AuthRepository, name string) identity.User {
		t.Helper()
		u, e := repo.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	customer, owner := seed(hostPeople, "Central customer"), seed(localPeople, "Local owner")
	if e := localSystem.SetAdmin(ctx, "", owner.ID, true, true); e != nil {
		t.Fatal(e)
	}
	company, localCompany := uuid.NewString(), uuid.NewString()
	for _, item := range []struct {
		local    bool
		id, name string
	}{{false, company, "Kept central company"}, {true, localCompany, "Kept local company"}} {
		db := hostDB
		if item.local {
			db = localDB
		}
		if _, e := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,$2)`, item.id, item.name); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := hostDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, company, customer.ID); e != nil {
		t.Fatal(e)
	}
	mint := func() (apikey.Key, string) {
		t.Helper()
		k, secret, e := apikey.Generate(apikey.CreateInput{Name: "Synthetic installation", OrganizationID: company, ResourceMode: "shared", Scopes: []string{apikey.Generation, apikey.OrganizationsManage, apikey.KeysManage}}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		scopes, _ := json.Marshal(k.Scopes)
		if _, e = hostDB.Exec(`INSERT INTO user_api_keys(id,user_id,name,prefix,token_hash,scopes,resource_mode,storage_mode,organization_id,created_by,purpose) VALUES($1,$2,$3,$4,$5,$6,'shared','shared',$7,$2,'')`, k.ID, customer.ID, k.Name, k.Prefix, apikey.Hash(secret), scopes, company); e != nil {
			t.Fatal(e)
		}
		return k, secret
	}
	key, secret := mint()
	key2, secret2 := mint()
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	host := NewGateway(Dependencies{Service: &accountService{}, Health: healthStub{}, Keys: postgres.NewAPIKeyRepository(hostDB), System: hostSystem})
	var mode atomic.Value
	mode.Store("")
	held, release := make(chan struct{}), make(chan struct{})
	central := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "outage":
			writeError(w, 503, "service_unavailable", "Temporary fixture outage")
			return
		case "network":
			panic(http.ErrAbortHandler)
		case "forbidden":
			writeError(w, 403, "insufficient_scope", "Not allowed")
			return
		case "held":
			recorded := httptest.NewRecorder()
			host.ServeHTTP(recorded, r)
			close(held)
			<-release
			for name, values := range recorded.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorded.Code)
			w.Write(recorded.Body.Bytes())
			return
		}
		host.ServeHTTP(w, r)
	}))
	defer central.Close()
	target, _ := url.Parse(central.URL)
	client := *central.Client()
	client.Transport = canonicalWorkspaceTransport{base: central.Client().Transport, target: target}
	manager := remote.New(localSystem, cipher, &client)
	localAuth, e := auth.New(ctx, localPeople, auth.Config{SessionSecret: strings.Repeat("l", 32), PublicURL: "https://local.fixture.test"})
	if e != nil {
		t.Fatal(e)
	}
	token, _ := wa.DeviceCode()
	if e = localPeople.CreateSession(ctx, wa.Digest(token), owner.ID, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	gateway := NewGateway(Dependencies{Service: &accountService{}, Health: healthStub{}, Auth: localAuth, System: localSystem, Keys: postgres.NewAPIKeyRepository(localDB), RemoteWorkspace: manager})
	handler, e := WithPublicAPI(gateway, "", "https://local.fixture.test", "")
	if e != nil {
		t.Fatal(e)
	}
	local := httptest.NewTLSServer(handler)
	defer local.Close()
	save := func(k apikey.Key, secret string, version int64) remote.Record {
		t.Helper()
		encrypted, e := cipher.Encrypt([]byte(secret))
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256([]byte(secret))
		r, e := localSystem.SaveRemoteWorkspace(ctx, remote.Record{Owner: owner.ID, Active: true, Access: wa.CompanyAccess, Provisioning: "dedicated", Origin: installation.DefaultCentralOrigin, OrganizationID: company, OrganizationName: "Kept central company", UserID: customer.ID, KeyID: k.ID, TokenHash: sum[:], Ciphertext: encrypted}, version)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	saved := save(key, secret, 0)
	call := func(path, secret, contextID string, session bool) (int, []byte) {
		t.Helper()
		request, _ := http.NewRequest("GET", local.URL+path, nil)
		if session {
			request.AddCookie(&http.Cookie{Name: "media_gateway_session", Value: token})
		}
		if secret != "" {
			request.Header.Set("Authorization", "Bearer "+secret)
		}
		if contextID != "" {
			request.Header.Set("X-Company-Context", contextID)
		}
		response, e := local.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, body
	}
	proxy := func(r remote.Record, secret string) (int, []byte) {
		return call("/auth/company-api/v1/key", secret, r.SnapshotID(), true)
	}
	beforeSettings, _ := localSystem.Settings(ctx)
	task := uuid.NewString()
	if _, e = localDB.Exec(`INSERT INTO tasks(id,status,provider,model,prompt,owner_id,provider_owner_id,storage_owner_id) VALUES($1,'succeeded','mock','mock-image-v1','Retained result',$2,$2,$2)`, task, owner.ID); e != nil {
		t.Fatal(e)
	}
	if status, body := proxy(saved, secret); status != 200 {
		t.Fatalf("connected %d %s", status, body)
	}
	for _, failure := range []string{"outage", "network", "forbidden"} {
		t.Run(failure, func(t *testing.T) {
			mode.Store(failure)
			status, _ := proxy(saved, secret)
			if status < 400 || status == 410 {
				t.Fatal(status)
			}
			current, _ := localSystem.RemoteWorkspace(ctx, owner.ID)
			if !current.Active || current.SnapshotID() != saved.SnapshotID() {
				t.Fatal("temporary failure disconnected")
			}
		})
	}
	mode.Store("")
	if _, e = hostDB.Exec(`UPDATE user_api_keys SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, key.ID); e != nil {
		t.Fatal(e)
	}
	if status, body := proxy(saved, secret); status != 401 || strings.Contains(string(body), "api_key_revoked") {
		t.Fatalf("expired %d %s", status, body)
	}
	if current, _ := localSystem.RemoteWorkspace(ctx, owner.ID); !current.Active {
		t.Fatal("expired-only disconnected")
	}
	hostDB.Exec(`UPDATE user_api_keys SET expires_at=NULL WHERE id=$1`, key.ID)
	hostDB.Exec(`UPDATE organization_members SET removed_at=NOW() WHERE organization_id=$1 AND user_id=$2`, company, customer.ID)
	if status, body := proxy(saved, secret); status != 401 || strings.Contains(string(body), "api_key_revoked") {
		t.Fatalf("membership-only %d %s", status, body)
	}
	hostDB.Exec(`UPDATE organization_members SET removed_at=NULL WHERE organization_id=$1 AND user_id=$2`, company, customer.ID)
	if status, _ := call("/auth/company-api/v1/key", secret, saved.SnapshotID(), false); status != 401 {
		t.Fatal("local session", status)
	}
	if current, _ := localSystem.RemoteWorkspace(ctx, owner.ID); !current.Active {
		t.Fatal("local-login failure disconnected")
	}
	if _, e = hostDB.Exec(`UPDATE user_api_keys SET revoked_at=NOW() WHERE id=$1`, key.ID); e != nil {
		t.Fatal(e)
	}
	if status, body := proxy(saved, secret); status != 410 || !strings.Contains(string(body), "company_connection_revoked") {
		t.Fatalf("revoked %d %s", status, body)
	}
	disconnected, e := postgres.NewSystemRepository(localDB).RemoteWorkspace(ctx, owner.ID)
	if e != nil || disconnected.Active || disconnected.Ciphertext != nil || len(disconnected.TokenHash) != 0 || disconnected.Version != saved.Version+1 {
		t.Fatal("restart state", disconnected, e)
	}
	if status, body := call("/auth/company-connection", "", "", true); status != 200 || !strings.Contains(string(body), `"connected":false`) {
		t.Fatalf("reload %d %s", status, body)
	}
	if status, body := call("/v1/system/settings", "", "", true); status != 200 {
		t.Fatalf("local modes %d %s", status, body)
	}
	afterSettings, _ := localSystem.Settings(ctx)
	if beforeSettings.SharedResources != afterSettings.SharedResources || beforeSettings.PersonalProviders != afterSettings.PersonalProviders || beforeSettings.Version != afterSettings.Version {
		t.Fatal("changed local settings")
	}
	var name, prompt string
	if e = localDB.QueryRow(`SELECT name FROM organizations WHERE id=$1`, localCompany).Scan(&name); e != nil || name != "Kept local company" {
		t.Fatal("local company", name, e)
	}
	if e = localDB.QueryRow(`SELECT prompt FROM tasks WHERE id=$1`, task).Scan(&prompt); e != nil || prompt != "Retained result" {
		t.Fatal("local history", prompt, e)
	}
	if e = hostDB.QueryRow(`SELECT name FROM organizations WHERE id=$1`, company).Scan(&name); e != nil || name != "Kept central company" {
		t.Fatal("central history", e)
	}
	// Reconnect through the existing versioned persistence contract, then ensure
	// an old in-flight revoked response cannot clear a replacement credential.
	renewed := save(key, secret, disconnected.Version)
	mode.Store("held")
	result := make(chan int, 1)
	go func() { status, _ := proxy(renewed, secret); result <- status }()
	<-held
	replacement := save(key2, secret2, renewed.Version)
	mode.Store("")
	close(release)
	if status := <-result; status != 409 {
		t.Fatal("stale revocation", status)
	}
	latest, _ := localSystem.RemoteWorkspace(ctx, owner.ID)
	if !latest.Active || latest.SnapshotID() != replacement.SnapshotID() {
		t.Fatal("stale response cleared new connection")
	}
	if status, body := proxy(replacement, secret2); status != 200 {
		t.Fatalf("reconnected %d %s", status, body)
	}
	// Actual central middleware returns typed evidence only for the exact revoked
	// secret; absent/unknown credentials still use the normal anonymous denial.
	for _, tc := range []struct{ secret, code string }{{secret, "api_key_revoked"}, {"gw_" + strings.Repeat("x", 43), "unauthorized"}, {"", "unauthorized"}} {
		request := httptest.NewRequest("GET", "https://central.fixture.test/v1/key", nil)
		if tc.secret != "" {
			request.Header.Set("Authorization", "Bearer "+tc.secret)
		}
		w := httptest.NewRecorder()
		host.ServeHTTP(w, request)
		if w.Code != 401 || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatal("central credential distinction", w.Code, w.Body)
		}
	}
	// Revoked is positive terminal evidence even when other denial reasons
	// also apply; expiration or membership loss alone above remains generic.
	hostDB.Exec(`UPDATE user_api_keys SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, key.ID)
	hostDB.Exec(`UPDATE organization_members SET removed_at=NOW() WHERE organization_id=$1 AND user_id=$2`, company, customer.ID)
	request := httptest.NewRequest("GET", "https://central.fixture.test/v1/key", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	host.ServeHTTP(recorder, request)
	if recorder.Code != 401 || !strings.Contains(recorder.Body.String(), `"code":"api_key_revoked"`) {
		t.Fatal("revoked precedence", recorder.Code, recorder.Body)
	}
	if _, _, e = postgres.NewAPIKeyRepository(hostDB).Authenticate(ctx, apikey.Hash(secret)); !errors.Is(e, apikey.ErrRevoked) || !errors.Is(e, apikey.ErrNotFound) {
		t.Fatal("typed compatibility", e)
	}
}

// Inject the race specifically between the local snapshot read and its CAS,
// separately from the earlier in-flight HTTP response race exercised above.
type revokedCASFixture struct {
	*remoteFixture
	reads    int
	conflict bool
	failure  bool
}

func (r *revokedCASFixture) RemoteWorkspace(ctx context.Context, owner string) (remote.Record, error) {
	record, err := r.remoteFixture.RemoteWorkspace(ctx, owner)
	r.reads++
	if r.conflict && r.reads == 2 {
		next := record
		next.Version++
		next.KeyID = "replacement"
		r.records[owner] = next
	}
	return record, err
}
func (r *revokedCASFixture) DisconnectRemoteWorkspace(ctx context.Context, owner string, version int64) (remote.Record, error) {
	if r.failure {
		return remote.Record{}, remote.ErrUnavailable
	}
	return r.remoteFixture.DisconnectRemoteWorkspace(ctx, owner, version)
}
func TestCompanyRevocationFailedDisconnectNeverClaimsRecovery(t *testing.T) {
	for _, scenario := range []string{"cas-after-read", "storage-failure", "timeout", "transport-failure"} {
		t.Run(scenario, func(t *testing.T) {
			cipher, _ := providerconfig.NewCipher(strings.Repeat("d", 32))
			const secret = "synthetic-only-secret"
			encrypted, _ := cipher.Encrypt([]byte(secret))
			sum := sha256.Sum256([]byte(secret))
			saved := remote.Record{Owner: "alice", Version: 1, Active: true, Access: wa.CompanyAccess, Origin: installation.DefaultCentralOrigin, KeyID: "old", OrganizationID: "fixture", TokenHash: sum[:], Ciphertext: encrypted}
			repo := &revokedCASFixture{remoteFixture: &remoteFixture{records: map[string]remote.Record{"alice": saved}}, conflict: scenario == "cas-after-read", failure: scenario == "storage-failure"}
			upstream := &http.Client{Transport: companyRoundTrip(func(r *http.Request) (*http.Response, error) {
				if scenario == "timeout" {
					return nil, context.DeadlineExceeded
				}
				if scenario == "transport-failure" {
					return nil, io.ErrUnexpectedEOF
				}
				return &http.Response{StatusCode: 401, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"api_key_revoked"}}`))}, nil
			})}
			s, _ := newPersonalTestServer()
			s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
			s.remoteWorkspaceManager = remote.New(repo, cipher, upstream)
			handler, err := WithPublicAPI(s.handler(), "", "https://local.fixture.test", "")
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "https://local.fixture.test/auth/company-api/v1/key", nil)
			req.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
			req.Header.Set("Authorization", "Bearer "+secret)
			req.Header.Set("X-Company-Context", saved.SnapshotID())
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			want := 409
			if repo.failure || scenario == "timeout" || scenario == "transport-failure" {
				want = 502
			}
			if w.Code != want || strings.Contains(w.Body.String(), "company_connection_revoked") {
				t.Fatal("claimed recovery", w.Code, w.Body)
			}
			current := repo.records["alice"]
			if !current.Active {
				t.Fatal("failed CAS/storage disconnected")
			}
			if repo.conflict && current.KeyID != "replacement" {
				t.Fatal("overwrote replacement")
			}
		})
	}
}
