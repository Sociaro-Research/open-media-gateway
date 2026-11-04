package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type mutationDeleteStore struct {
	*storage.Local
	mu        sync.Mutex
	calls     []string
	fail      bool
	hook      func(context.Context, string)
	failAfter bool
}

func (s *mutationDeleteStore) DeleteReference(ctx context.Context, key string) error {
	s.mu.Lock()
	s.calls = append(s.calls, key)
	fail, after, hook := s.fail, s.failAfter, s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(ctx, key)
	}
	if fail {
		return errors.New("fixture lost storage reply")
	}
	e := s.Local.DeleteReference(ctx, key)
	if e == nil && after {
		return errors.New("fixture reply lost after physical delete")
	}
	return e
}
func (s *mutationDeleteStore) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.calls) }
func mutationReferenceFixture(t *testing.T) (*activeHTTPFixture, *references.Library, *mutationDeleteStore, context.Context) {
	t.Helper()
	f := activeHTTP(t)
	v := f.current(t)
	local, e := storage.NewLocal(t.TempDir(), http.DefaultClient, 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	store := &mutationDeleteStore{Local: local}
	library := references.New(postgres.NewReferenceRepository(f.centralDB), store)
	f.central = mutationGateway(t, f, func(d *api.Dependencies) {
		d.Storage = store
		d.References = library
		d.SigningSecret = fixtureMaster
		d.AssetPublicURL = fixtureCentral
		d.MediaURLTTL = time.Hour
	})
	ctx := identity.WithOrganizationRole(identity.WithUser(context.Background(), identity.User{ID: v.Tokens.Actor}), v.Tokens.Company, "supermanager")
	return f, library, store, ctx
}
func revokeMutationFixture(t *testing.T, f *activeHTTPFixture, v ci.ActiveContext, grant bool) {
	t.Helper()
	repo, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	ctx := installation.WithContext(context.Background(), installation.Metadata{Role: "central", CentralOrigin: fixtureCentral})
	if grant {
		a, e := auth.New(ctx, postgres.NewAuthRepository(f.centralDB), auth.Config{SessionSecret: fixtureMaster, PublicURL: fixtureCentral})
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("GET", fixtureCentral+"/auth/session", nil)
		r.AddCookie(f.centralCookie)
		u, e := a.Authenticate(r)
		if e != nil {
			t.Fatal(e)
		}
		e = repo.Revoke(identity.WithUser(ctx, u), v.Tokens.GrantID)
		if e != nil {
			t.Fatal(e)
		}
	} else {
		if e = repo.Logout(ctx, ci.Credentials{Installation: v.Tokens.InstallationCredential, Session: v.Tokens.AccessToken, LocalSessionHash: v.Tokens.LocalSessionHash}); e != nil {
			t.Fatal(e)
		}
	}
}
func TestCompanyMutationReferenceCleanupSurvivesRestartAndRevocation(t *testing.T) {
	for _, grant := range []bool{false, true} {
		name := "family"
		if grant {
			name = "grant"
		}
		t.Run(name, func(t *testing.T) {
			f, library, store, ctx := mutationReferenceFixture(t)
			v := f.current(t)
			data := testmedia.PNG512("admitted old reference")
			original, e := library.Upload(ctx, "old.png", bytes.NewReader(data))
			if e != nil {
				t.Fatal(e)
			}
			unrelated, e := library.Upload(ctx, "other.png", bytes.NewReader(testmedia.PNG512("unrelated bytes")))
			if e != nil {
				t.Fatal(e)
			}
			store.fail = true
			failed := customerRequest(t, f.central, v, true, "DELETE", "/v1/references/"+original.ID, "", v.Tokens.PlaygroundKey, nil)
			if failed.Code < 400 || store.count() != 1 {
				t.Fatalf("cleanup failure not reached%d calls%d", failed.Code, store.count())
			}
			var pending bool
			var savedKey string
			if e = f.centralDB.QueryRow(`SELECT r.deleting,d.storage_key FROM reference_images r JOIN reference_deletion_admissions d ON d.owner_id=r.owner_id AND d.reference_id=r.id WHERE r.id=$1 AND d.state='pending'`, original.ID).Scan(&pending, &savedKey); e != nil || !pending || savedKey != original.StorageKey {
				t.Fatal("durable original custody absent", e)
			}
			if _, e = f.centralDB.Exec(`UPDATE reference_images SET storage_key=$2 WHERE id=$1`, original.ID, unrelated.StorageKey); e == nil {
				t.Fatal("deleting destination rebound")
			}
			revokeMutationFixture(t, f, v, grant)
			expectFixture(t, customerRequest(t, f.central, v, true, "DELETE", "/v1/references/"+original.ID, "", v.Tokens.PlaygroundKey, nil), 403)
			if store.count() != 1 {
				t.Fatal("revoked caller gained cleanup authority")
			}
			// Recreate both repository and library as on worker restart; no live user or
			// grant proof is supplied to the completion-only worker.
			restarted := references.New(postgres.NewReferenceRepository(f.centralDB), store)
			store.fail = false
			if e = restarted.CleanupDeletions(context.Background()); e != nil {
				t.Fatal(e)
			}
			if store.count() != 2 {
				t.Fatal("wrong physical operation count", store.count())
			}
			if _, e = store.Open(ctx, original.StorageKey); !errors.Is(e, storage.ErrObjectNotFound) {
				t.Fatal("old bytes retained", e)
			}
			object, e := store.Open(ctx, unrelated.StorageKey)
			if e != nil {
				t.Fatal("unrelated bytes removed", e)
			}
			object.Reader.Close()
			var state string
			if e = f.centralDB.QueryRow(`SELECT state FROM reference_deletion_admissions WHERE reference_id=$1`, original.ID).Scan(&state); e != nil || state != "completed" {
				t.Fatal("terminal custody lost", e)
			}
			// A later same-content upload receives a new ID/key; old cleanup cannot touch it.
			successor, e := library.Upload(ctx, "new.png", bytes.NewReader(data))
			if e != nil {
				t.Fatal(e)
			}
			if successor.ID == original.ID || successor.StorageKey == original.StorageKey {
				t.Fatal("old identity reused")
			}
			if e = restarted.CleanupDeletions(context.Background()); e != nil {
				t.Fatal(e)
			}
			if store.count() != 2 {
				t.Fatal("terminal intent replayed provider call")
			}
			object, e = store.Open(ctx, successor.StorageKey)
			if e != nil {
				t.Fatal("successor removed", e)
			}
			object.Reader.Close()
			raw, _ := json.Marshal(references.Deletion{Owner: v.Tokens.Company, Item: original, Lease: "private"})
			if string(raw) != "{}" {
				t.Fatal("cleanup custody exposed", string(raw))
			}
		})
	}
}
func TestCompanyMutationDirectDeleteDoesNotHoldDelegatedAuthorityAcrossStorage(t *testing.T) {
	f, library, store, ctx := mutationReferenceFixture(t)
	v := f.current(t)
	item, e := library.Upload(ctx, "blocked.png", bytes.NewReader(testmedia.PNG512("blocked storage")))
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store.hook = func(context.Context, string) { once.Do(func() { close(entered) }); <-release }
	direct := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		direct <- resourceDirect(f, "DELETE", "/v1/references/"+item.ID, "", v.Tokens.PlaygroundKey, "")
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("direct storage call not reached")
	}
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	delegated := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		delegated <- customerRequest(t, f.central, v, true, "DELETE", "/v1/references/"+item.ID, "", v.Tokens.PlaygroundKey, nil)
	}()
	select {
	case w := <-delegated:
		if w.Code != 409 {
			t.Fatalf("active deletion lease status%d %s", w.Code, w.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delegated authority stuck behind direct storage I/O")
	}
	// This must finish while the direct storage callback remains blocked.
	done := make(chan struct{})
	go func() { revokeMutationFixture(t, f, v, false); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("revocation blocked by storage I/O")
	}
	close(release)
	released = true
	select {
	case w := <-direct:
		if w.Code != 204 {
			t.Fatalf("admitted direct cleanup%d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("direct cleanup stuck")
	}
	if store.count() != 1 {
		t.Fatal("concurrent cleanup duplicated external call")
	}
}

func TestCompanyMutationDeletionLostPhysicalReplyAndExactLeaseRecovery(t *testing.T) {
	f, library, store, ctx := mutationReferenceFixture(t)
	v := f.current(t)
	item, e := library.Upload(ctx, "lost.png", bytes.NewReader(testmedia.PNG512("lost physical reply")))
	if e != nil {
		t.Fatal(e)
	}
	store.failAfter = true
	w := customerRequest(t, f.central, v, true, "DELETE", "/v1/references/"+item.ID, "", v.Tokens.PlaygroundKey, nil)
	if w.Code < 400 || store.count() != 1 {
		t.Fatalf("lost physical reply not reached%d", w.Code)
	}
	if _, e = store.Open(ctx, item.StorageKey); !errors.Is(e, storage.ErrObjectNotFound) {
		t.Fatal("fixture did not remove physical bytes", e)
	}
	revokeMutationFixture(t, f, v, true)
	restarted := postgres.NewReferenceRepository(f.centralDB)
	first, e := restarted.ClaimDeletions(context.Background(), 1)
	if e != nil || len(first) != 1 {
		t.Fatal("lost reply custody missing", e)
	}
	forged := first[0]
	forged.Item.StorageKey = "other/reference/object"
	if e = restarted.FinishDeletion(context.Background(), forged, true); e == nil {
		t.Fatal("substituted completion accepted")
	}
	if e = restarted.FinishDeletion(context.Background(), first[0], false); e != nil {
		t.Fatal(e)
	}
	next, e := postgres.NewReferenceRepository(f.centralDB).ClaimDeletions(context.Background(), 1)
	if e != nil || len(next) != 1 || next[0].Lease == first[0].Lease {
		t.Fatal("new cleanup claim missing", e)
	}
	if e = restarted.FinishDeletion(context.Background(), first[0], true); e == nil {
		t.Fatal("stale claimant settled newer lease")
	}
	store.failAfter = false
	if e = store.DeleteReference(references.StorageContext(identity.WithOwnerID(context.Background(), next[0].Owner), next[0].Item), next[0].Item.StorageKey); e != nil && !errors.Is(e, storage.ErrObjectNotFound) {
		t.Fatal(e)
	}
	if e = restarted.FinishDeletion(context.Background(), next[0], true); e != nil {
		t.Fatal(e)
	}
	// Simulate the Finish response being lost: restart and replay the exact claim.
	if e = postgres.NewReferenceRepository(f.centralDB).FinishDeletion(context.Background(), next[0], true); e != nil {
		t.Fatal("exact terminal replay failed", e)
	}
	if e = references.New(postgres.NewReferenceRepository(f.centralDB), store).CleanupDeletions(context.Background()); e != nil {
		t.Fatal(e)
	}
	if store.count() != 2 {
		t.Fatal("settled cleanup repeated external deletion")
	}
	var state, key string
	if e = f.centralDB.QueryRow(`SELECT state,storage_key FROM reference_deletion_admissions WHERE reference_id=$1`, item.ID).Scan(&state, &key); e != nil || state != "completed" || key != item.StorageKey {
		t.Fatal("original history lost", e)
	}
}

type mutationGroupRepo struct {
	*postgres.ProviderAssetRepository
	before func(context.Context)
}

func (r mutationGroupRepo) AdmitGroup(ctx context.Context, id, p, scope, name string) error {
	if r.before != nil {
		r.before(ctx)
	}
	return r.ProviderAssetRepository.AdmitGroup(ctx, id, p, scope, name)
}

type mutationGroupClient struct {
	*companyAssetClient
	call  func()
	calls int
	fail  bool
}

func (c *mutationGroupClient) CreateGroup(ctx context.Context, name string) (providerassets.Group, error) {
	c.calls++
	if c.call != nil {
		c.call()
	}
	if c.fail {
		return providerassets.Group{}, errors.New("unknown group result")
	}
	return c.companyAssetClient.CreateGroup(ctx, name)
}

type mutationGroupDirectory struct{ client providerassets.Client }

func (d mutationGroupDirectory) AssetLibrary(context.Context, string) (providerassets.Client, error) {
	return d.client, nil
}
func TestCompanyMutationGroupAdmissionAndCompletionOrdering(t *testing.T) {
	for _, mode := range []string{"revoke_before", "admission_before", "unknown_result"} {
		t.Run(mode, func(t *testing.T) {
			f := activeHTTP(t)
			v := f.current(t)
			client := &mutationGroupClient{companyAssetClient: &companyAssetClient{t: t}, fail: mode == "unknown_result"}
			repo := mutationGroupRepo{ProviderAssetRepository: postgres.NewProviderAssetRepository(f.centralDB)}
			if mode == "revoke_before" {
				repo.before = mutationLogout(t, f, v)
			} else {
				client.call = func() { revokeMutationFixture(t, f, v, false) }
			}
			h := mutationGateway(t, f, func(d *api.Dependencies) {
				d.Service = core.NewTaskService(postgres.NewTaskRepository(f.centralDB), nil, "mock", "fixture")
				d.ProviderAssets = providerassets.New(repo, mutationGroupDirectory{client})
			})
			w := customerRequest(t, h, v, true, "POST", "/v1/references/providers/bytedance/groups", `{"name":"Admission fixture"}`, v.Tokens.PlaygroundKey, nil)
			if w.Code != 409 {
				t.Fatalf("epoch response%d %s", w.Code, w.Body.String())
			}
			count := countFixture(t, f.centralDB, "provider_asset_group_admissions")
			if mode == "revoke_before" {
				if client.calls != 0 || count != 0 {
					t.Fatal("revoked group reached provider or persistence")
				}
				return
			}
			if client.calls != 1 || count != 1 {
				t.Fatal("group admission/provider count", client.calls, count)
			}
			var state string
			if e := f.centralDB.QueryRow(`SELECT state FROM provider_asset_group_admissions`).Scan(&state); e != nil {
				t.Fatal(e)
			}
			want := "completed"
			groups := 1
			if mode == "unknown_result" {
				want = "admitted"
				groups = 0
			}
			if state != want || countFixture(t, f.centralDB, "provider_asset_groups") != groups {
				t.Fatal("admitted result/custody lost", state)
			}
			// Unknown state is durable but deliberately NOT claimed as recovered. No
			// retry/reconciler or caller operation lookup is introduced by this stage.
		})
	}
}
