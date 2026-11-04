package companyinterface_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
)

type revokeDuringBody struct {
	io.Reader
	once   sync.Once
	revoke func()
}

func (b *revokeDuringBody) Read(p []byte) (int, error) { b.once.Do(b.revoke); return b.Reader.Read(p) }
func (*revokeDuringBody) Close() error                 { return nil }
func TestCompanyCustomerRevocationDuringBodyCannotWriteProfile(t *testing.T) {
	f := activeHTTP(t)
	v := f.current(t)
	repo, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	h := customerHandler(t, f, postgres.NewCompanyInterfaceAuthority(f.centralDB))
	var before string
	if e = f.centralDB.QueryRow(`SELECT display_name FROM users WHERE id=$1`, v.Tokens.Actor).Scan(&before); e != nil {
		t.Fatal(e)
	}
	body := `{"name":"Must not be written after logout"}`
	response := customerRequest(t, h, v, true, "PATCH", "/v1/me", body, "", func(r *http.Request) {
		r.Body = &revokeDuringBody{Reader: strings.NewReader(body), revoke: func() {
			if e := repo.Logout(installation.WithContext(context.Background(), installation.Metadata{Role: "central", CentralOrigin: fixtureCentral}), ci.Credentials{Installation: v.Tokens.InstallationCredential, Session: v.Tokens.AccessToken, LocalSessionHash: v.Tokens.LocalSessionHash}); e != nil {
				t.Fatal(e)
			}
		}}
	})
	var after string
	if e = f.centralDB.QueryRow(`SELECT display_name FROM users WHERE id=$1`, v.Tokens.Actor).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if after != before {
		t.Fatalf("revoked profile write committed despite response%d", response.Code)
	}
	if response.Code != 403 && response.Code != 409 {
		t.Fatalf("revoked mutation status%d", response.Code)
	}
}

// These wrappers run INSIDE the real inner handler, after all adapter checks,
// immediately before the actual PostgreSQL mutation transaction begins.
type mutationProfileRepo struct {
	*postgres.AuthRepository
	before, after func(context.Context)
}

func (r mutationProfileRepo) UpdateProfile(ctx context.Context, id string, in auth.ProfileUpdate) (identity.User, error) {
	if r.before != nil {
		r.before(ctx)
	}
	u, e := r.AuthRepository.UpdateProfile(ctx, id, in)
	if e == nil && r.after != nil {
		r.after(ctx)
	}
	return u, e
}

type mutationKeysRepo struct {
	*postgres.APIKeyRepository
	before func(context.Context)
}

func (r mutationKeysRepo) Create(ctx context.Context, owner string, k apikey.Key, hash []byte) error {
	r.before(ctx)
	return r.APIKeyRepository.Create(ctx, owner, k, hash)
}

type mutationSystemRepo struct {
	*postgres.SystemRepository
	before func(context.Context)
}

func (r mutationSystemRepo) SetOrganizationMember(ctx context.Context, actor, id, target, role string, limit *money.USD, provided, remove bool) error {
	r.before(ctx)
	return r.SystemRepository.SetOrganizationMember(ctx, actor, id, target, role, limit, provided, remove)
}
func mutationGateway(t *testing.T, f *activeHTTPFixture, configure func(*api.Dependencies)) http.Handler {
	t.Helper()
	a, e := auth.New(context.Background(), postgres.NewAuthRepository(f.centralDB), auth.Config{SessionSecret: fixtureMaster, PublicURL: fixtureCentral})
	if e != nil {
		t.Fatal(e)
	}
	lifecycle, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	d := api.Dependencies{CompanyInterface: lifecycle, CompanyCustomer: postgres.NewCompanyInterfaceAuthority(f.centralDB), CompanyCentralOrigin: fixtureCentral, Auth: a, System: postgres.NewSystemRepository(f.centralDB), Keys: postgres.NewAPIKeyRepository(f.centralDB)}
	if configure != nil {
		configure(&d)
	}
	h, e := api.WithPublicAPI(api.NewGateway(d), "", fixtureCentral, "")
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func mutationLogout(t *testing.T, f *activeHTTPFixture, v ci.ActiveContext) func(context.Context) {
	t.Helper()
	repo, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	return func(ctx context.Context) {
		if _, ok := ci.MutationAuthorityFromContext(ctx); !ok {
			t.Fatal("callback did not reach trusted inner repository boundary")
		}
		trusted := installation.WithContext(context.Background(), installation.Metadata{Role: "central", CentralOrigin: fixtureCentral})
		if e := repo.Logout(trusted, ci.Credentials{Installation: v.Tokens.InstallationCredential, Session: v.Tokens.AccessToken, LocalSessionHash: v.Tokens.LocalSessionHash}); e != nil {
			t.Fatal(e)
		}
	}
}
func TestCompanyMutationProfileRepositoryOrdering(t *testing.T) {
	for _, when := range []string{"family_before", "source_before", "admission_before"} {
		t.Run(when, func(t *testing.T) {
			f := activeHTTP(t)
			v := f.current(t)
			calls := 0
			action := mutationLogout(t, f, v)
			if when == "source_before" {
				action = func(ctx context.Context) {
					if _, ok := ci.MutationAuthorityFromContext(ctx); !ok {
						t.Fatal("no authority descriptor")
					}
					sum := sha256.Sum256([]byte(f.centralCookie.Value))
					if e := postgres.NewAuthRepository(f.centralDB).DeleteSession(context.Background(), sum[:]); e != nil {
						t.Fatal(e)
					}
				}
			}
			invoke := func(ctx context.Context) { calls++; action(ctx) }
			h := mutationGateway(t, f, func(d *api.Dependencies) {
				r := mutationProfileRepo{AuthRepository: postgres.NewAuthRepository(f.centralDB)}
				if when == "admission_before" {
					r.after = invoke
				} else {
					r.before = invoke
				}
				a, e := auth.New(context.Background(), r, auth.Config{SessionSecret: fixtureMaster, PublicURL: fixtureCentral})
				if e != nil {
					t.Fatal(e)
				}
				d.Auth = a
			})
			var before string
			if e := f.centralDB.QueryRow(`SELECT display_name FROM users WHERE id=$1`, v.Tokens.Actor).Scan(&before); e != nil {
				t.Fatal(e)
			}
			w := customerRequest(t, h, v, true, "PATCH", "/v1/me", `{"name":"Repository admission marker"}`, "", nil)
			var after string
			if e := f.centralDB.QueryRow(`SELECT display_name FROM users WHERE id=$1`, v.Tokens.Actor).Scan(&after); e != nil {
				t.Fatal(e)
			}
			if calls != 1 {
				t.Fatalf("repository entry calls%d status%d", calls, w.Code)
			}
			if when == "admission_before" {
				if after != "Repository admission marker" {
					t.Fatal("admitted write lost")
				}
			} else if after != before {
				t.Fatalf("revoked write persisted status%d", w.Code)
			}
			if w.Code != 409 {
				t.Fatalf("post-admission epoch fence status%d %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestCompanyMutationKeyAndMemberRepositoryRevocation(t *testing.T) {
	for _, family := range []string{"key", "member"} {
		t.Run(family, func(t *testing.T) {
			f := activeHTTP(t)
			v := f.current(t)
			target, _ := resourceCoworkerKey(t, f, "member")
			calls := 0
			revoke := mutationLogout(t, f, v)
			hook := func(ctx context.Context) { calls++; revoke(ctx) }
			h := mutationGateway(t, f, func(d *api.Dependencies) {
				if family == "key" {
					d.Keys = mutationKeysRepo{postgres.NewAPIKeyRepository(f.centralDB), hook}
				} else {
					d.System = mutationSystemRepo{postgres.NewSystemRepository(f.centralDB), hook}
				}
			})
			keys := countFixture(t, f.centralDB, "user_api_keys")
			method, path, body := "POST", "/v1/api-keys", fmt.Sprintf(`{"name":"Must not issue","organization_id":%q,"user_id":%q}`, v.Tokens.Company, target)
			if family == "member" {
				method = "PUT"
				path = "/v1/organizations/" + v.Tokens.Company + "/members/" + target
				body = `{"role":"admin","weekly_limit_usd":null}`
			}
			w := customerRequest(t, h, v, true, method, path, body, "", nil)
			if calls != 1 || w.Code != 409 {
				t.Fatalf("boundary not reached calls%d status%d %s", calls, w.Code, w.Body.String())
			}
			if countFixture(t, f.centralDB, "user_api_keys") != keys {
				t.Fatal("key issued after revoke")
			}
			var role string
			if e := f.centralDB.QueryRow(`SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, target).Scan(&role); e != nil || role != "member" {
				t.Fatal("member changed after revoke", role, e)
			}
		})
	}
}
func TestCompanyMutationHistoricalSalesTargetAndDenialParity(t *testing.T) {
	f := activeHTTP(t)
	v := f.current(t)
	target, secret := resourceCoworkerKey(t, f, "member")
	sys := postgres.NewSystemRepository(f.centralDB)
	if e := sys.SetAdmin(context.Background(), "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	structure, e := sys.SalesStructure(context.Background(), v.Tokens.Actor)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = sys.PlaceSalesMember(context.Background(), v.Tokens.Actor, target, system.SalesPlacement{Role: system.ClientManager, Version: structure.Version}); e != nil {
		t.Fatal(e)
	}
	var keyID string
	if e = f.centralDB.QueryRow(`SELECT id::text FROM user_api_keys WHERE token_hash=$1`, apikey.Hash(secret)).Scan(&keyID); e != nil {
		t.Fatal(e)
	}
	h := mutationGateway(t, f, nil)
	f.central = h
	direct := resourceDirect(f, "DELETE", "/v1/api-keys/"+keyID, "", v.Tokens.PlaygroundKey, "")
	// The original Playground key lacks management scope; use the real browser
	// session for direct cleanup, matching delegated browser management authority.
	_ = direct
	r := httptest.NewRequest("DELETE", fixtureCentral+"/v1/api-keys/"+keyID, nil)
	r.AddCookie(f.centralCookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("direct historical cleanup%d %s", w.Code, w.Body.String())
	}
	delegated := customerRequest(t, h, v, true, "DELETE", "/v1/api-keys/"+keyID, "", "", nil)
	if delegated.Code != w.Code {
		t.Errorf("historical target direct%d delegated%d %s", w.Code, delegated.Code, delegated.Body.String())
	}
	_, foreign, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	body := fmt.Sprintf(`{"name":"Nonmember","organization_id":%q,"user_id":%q}`, v.Tokens.Company, foreign.ID)
	r = httptest.NewRequest("POST", fixtureCentral+"/v1/api-keys", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(f.centralCookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	delegated = customerRequest(t, h, v, true, "POST", "/v1/api-keys", body, "", nil)
	if w.Code != 403 || delegated.Code != w.Code {
		t.Errorf("nonmember direct%d delegated%d %s", w.Code, delegated.Code, delegated.Body.String())
	}
}

type mutationTaskRepo struct {
	*postgres.TaskRepository
	before func(context.Context)
}

func (r mutationTaskRepo) CreateOrGet(ctx context.Context, task core.Task, key, hash string) (core.Task, bool, error) {
	r.before(ctx)
	return r.TaskRepository.CreateOrGet(ctx, task, key, hash)
}
func TestCompanyMutationTaskAndResourceRepositoryRevocation(t *testing.T) {
	for _, kind := range []string{"generation", "resource"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			var hook func(context.Context)
			configure := func(d *api.Dependencies, f *activeHTTPFixture) {
				repo := mutationTaskRepo{postgres.NewTaskRepository(f.centralDB), func(ctx context.Context) { calls++; hook(ctx) }}
				if kind == "resource" {
					native, e := kling.Definition().Factory(providerkit.Credentials{APIKey: "synthetic-resource-fixture", Extra: map[string]string{"selection_add_price": "0.01"}}, nil, &http.Client{Transport: noResourceNetwork{t}})
					if e != nil {
						t.Fatal(e)
					}
					service := core.NewTaskServiceWithProviders(repo, templateWorkflowQueue{}, map[string]core.MediaProvider{"kling": companyResourceProvider{native}}, map[string]string{"kling": kling.TextToSpeech}, "kling")
					service.EnableAccounting()
					d.Service = service
				} else {
					provider := allWorkflowProvider{}
					provider.Adapter = mock.New(0, "https://unused.fixture.test")
					service := core.NewTaskServiceWithProviders(repo, templateWorkflowQueue{}, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "workflow-image"}, "mock")
					service.EnableAccounting()
					d.Service = service
				}
			}
			f, library, ctx := companyResourceFixture(t, configure)
			v := f.current(t)
			hook = mutationLogout(t, f, v)
			path, body := "/v1/videos", `{"model":"mock/mock-video-v1","prompt":"Must not admit","max_expected_cost_usd":0}`
			if kind == "resource" {
				wav := make([]byte, 44+8000*6)
				copy(wav, testmedia.WAV()[:44])
				binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
				binary.LittleEndian.PutUint32(wav[40:], uint32(len(wav)-44))
				ref, e := library.Upload(ctx, "voice.wav", bytes.NewReader(wav))
				if e != nil {
					t.Fatal(e)
				}
				path = "/v1/provider-resources"
				body = fmt.Sprintf(`{"provider":"kling","workflow_model":%q,"kind":"voice","name":"Must not admit","reference_ids":[%q],"max_expected_usd":0.007}`, kling.TextToSpeech, ref.ID)
			}
			tasks := countFixture(t, f.centralDB, "tasks")
			outbox := countFixture(t, f.centralDB, "task_queue_outbox")
			w := customerRequest(t, f.central, v, true, "POST", path, body, v.Tokens.PlaygroundKey, nil)
			if calls != 1 || w.Code != 409 {
				t.Fatalf("not actual repository boundary calls%d status%d %s", calls, w.Code, w.Body.String())
			}
			if countFixture(t, f.centralDB, "tasks") != tasks || countFixture(t, f.centralDB, "task_queue_outbox") != outbox {
				t.Fatal("revoked task/outbox persisted")
			}
		})
	}
}
func waitMutationCondition(t *testing.T, condition func() bool) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("database interleaving condition timed out")
}
func TestCompanyMutationCommitSerializesConcurrentFamilyRevocation(t *testing.T) {
	f := activeHTTP(t)
	v := f.current(t)
	h := mutationGateway(t, f, nil)
	const lockID = 282092
	// A fixture-only trigger pauses after the production guard holds its locks but
	// before the real UPDATE is durable. No production admission hook is bypassed.
	_, e := f.centralDB.Exec(`CREATE FUNCTION fixture_pause_profile() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.display_name='Held local commit' THEN PERFORM pg_advisory_xact_lock(282092); END IF; RETURN NEW; END $$; CREATE TRIGGER fixture_pause_profile BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION fixture_pause_profile()`)
	if e != nil {
		t.Fatal(e)
	}
	blocker, e := f.centralDB.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Close()
	if _, e = blocker.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, lockID); e != nil {
		t.Fatal(e)
	}
	released := false
	defer func() {
		if !released {
			blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID)
		}
	}()
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response <- customerRequest(t, h, v, true, "PATCH", "/v1/me", `{"name":"Held local commit"}`, "", nil)
	}()
	var admissionPID int
	waitMutationCondition(t, func() bool {
		return f.centralDB.QueryRow(`SELECT pid FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted LIMIT 1`, lockID).Scan(&admissionPID) == nil
	})
	repo, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	revoked := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ctx = installation.WithContext(ctx, installation.Metadata{Role: "central", CentralOrigin: fixtureCentral})
		revoked <- repo.Logout(ctx, ci.Credentials{Installation: v.Tokens.InstallationCredential, Session: v.Tokens.AccessToken, LocalSessionHash: v.Tokens.LocalSessionHash})
	}()
	waitMutationCondition(t, func() bool {
		var waiting bool
		e := f.centralDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, admissionPID).Scan(&waiting)
		return e == nil && waiting
	})
	select {
	case e := <-revoked:
		t.Fatalf("revocation passed held admission: %v", e)
	default:
	}
	if _, e = blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID); e != nil {
		t.Fatal(e)
	}
	released = true
	select {
	case e = <-revoked:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revocation stuck after local commit")
	}
	select {
	case w := <-response:
		if w.Code != 200 && w.Code != 409 {
			t.Fatalf("commit response%d", w.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mutation stuck")
	}
	var name string
	if e = f.centralDB.QueryRow(`SELECT display_name FROM users WHERE id=$1`, v.Tokens.Actor).Scan(&name); e != nil || name != "Held local commit" {
		t.Fatal("admitted update missing", e)
	}
	expectFixture(t, customerRequest(t, h, v, true, "PATCH", "/v1/me", `{"name":"Late mutation"}`, "", nil), 403)
	if e = f.centralDB.QueryRow(`SELECT display_name FROM users WHERE id=$1`, v.Tokens.Actor).Scan(&name); e != nil || name != "Held local commit" {
		t.Fatal("post-revocation write", e)
	}
}

func TestCompanyMutationCurrentAuthoritiesAndCreatorIndependence(t *testing.T) {
	for _, change := range []string{"grant", "key", "scopes", "recipient_role", "browser_role", "browser_removed"} {
		t.Run(change, func(t *testing.T) {
			f := activeHTTP(t)
			v := f.current(t)
			recipient, key := resourceCoworkerKey(t, f, "member")
			_, other, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
			if _, e := f.centralDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role,weekly_limit_micros) VALUES($1,$2,'supermanager',NULL)`, v.Tokens.Company, other.ID); e != nil {
				t.Fatal(e)
			}
			if _, e := f.centralDB.Exec(`UPDATE organizations SET credit_limit_micros=1000000 WHERE id=$1`, v.Tokens.Company); e != nil {
				t.Fatal(e)
			}
			if _, e := f.centralDB.Exec(`UPDATE organization_members SET weekly_limit_micros=NULL WHERE organization_id=$1`, v.Tokens.Company); e != nil {
				t.Fatal(e)
			}
			var keyID string
			if e := f.centralDB.QueryRow(`SELECT id::text FROM user_api_keys WHERE token_hash=$1`, apikey.Hash(key)).Scan(&keyID); e != nil {
				t.Fatal(e)
			}
			calls := 0
			sys := postgres.NewSystemRepository(f.centralDB)
			before := func(ctx context.Context) {
				calls++
				if _, ok := ci.MutationAuthorityFromContext(ctx); !ok {
					t.Fatal("not inside handler repository boundary")
				}
				var e error
				switch change {
				case "grant":
					revokeMutationFixture(t, f, v, true)
				case "key":
					e = postgres.NewAPIKeyRepository(f.centralDB).RevokeForActor(context.Background(), v.Tokens.Actor, keyID)
				case "scopes":
					_, e = f.centralDB.Exec(`UPDATE user_api_keys SET scopes='["organizations:manage"]'::jsonb WHERE id=$1`, keyID) // isolated authority-state transition; no key PATCH API is claimed
				case "recipient_role":
					e = sys.SetOrganizationMember(context.Background(), v.Tokens.Actor, v.Tokens.Company, recipient, "content_officer", nil, false, false)
				case "browser_role":
					e = sys.SetOrganizationMember(context.Background(), other.ID, v.Tokens.Company, v.Tokens.Actor, "developer", nil, false, false)
				case "browser_removed":
					e = sys.SetOrganizationMember(context.Background(), other.ID, v.Tokens.Company, v.Tokens.Actor, "supermanager", nil, false, true)
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			provider := allWorkflowProvider{}
			provider.Adapter = mock.New(0, "https://unused.fixture.test")
			repo := mutationTaskRepo{postgres.NewTaskRepository(f.centralDB), before}
			service := core.NewTaskServiceWithProviders(repo, templateWorkflowQueue{}, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "workflow-image"}, "mock")
			service.EnableAccounting()
			f.central = mutationGateway(t, f, func(d *api.Dependencies) { d.Service = service })
			body := `{"model":"mock/mock-video-v1","prompt":"Authority change","max_expected_cost_usd":0}`
			w := customerRequest(t, f.central, v, true, "POST", "/v1/videos", body, key, nil)
			if calls != 1 || w.Code != 409 {
				t.Fatalf("authority change not fenced calls%d status%d %s", calls, w.Code, w.Body.String())
			}
			if countFixture(t, f.centralDB, "tasks") != 0 || countFixture(t, f.centralDB, "task_queue_outbox") != 0 {
				t.Fatal("changed authority admitted work")
			}
			if change == "browser_removed" {
				// The creator/browser membership is gone, but the independently valid key's
				// actual recipient remains a member. SDK/direct use must not inherit creator checks.
				cleanService := core.NewTaskServiceWithProviders(postgres.NewTaskRepository(f.centralDB), templateWorkflowQueue{}, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "workflow-image"}, "mock")
				cleanService.EnableAccounting()
				f.central = mutationGateway(t, f, func(d *api.Dependencies) { d.Service = cleanService })
				expectFixture(t, customerRequest(t, f.central, v, false, "POST", "/v1/videos", body, key, nil), 202)
				expectFixture(t, resourceDirect(f, "POST", "/v1/videos", body, key, ""), 202)
				if countFixture(t, f.centralDB, "tasks") != 2 {
					t.Fatal("valid recipient key did not survive creator removal")
				}
				var wrong int
				if e := f.centralDB.QueryRow(`SELECT count(*) FROM tasks WHERE actor_id<>$1 OR owner_id<>$2`, recipient, v.Tokens.Company).Scan(&wrong); e != nil || wrong != 0 {
					t.Fatal("creator impersonation", e)
				}
			}
		})
	}
}
