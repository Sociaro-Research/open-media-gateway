package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

type resourcePresetCatalog struct{ api.Service }

func (s resourcePresetCatalog) ListProviders(context.Context) []core.ProviderDescriptor {
	return []core.ProviderDescriptor{{ID: "kling", Models: []core.MediaModel{{ID: "preset-consumer", MediaType: "audio", ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "voice", MaxCount: 1, SupportsPresets: true}}}}}}
}

type resourcePresetDirectory struct {
	core.ProviderDirectory
	provider core.MediaProvider
}

func (d resourcePresetDirectory) Provider(context.Context, string) (core.MediaProvider, error) {
	return d.provider, nil
}

type resourcePresetProvider struct {
	core.MediaProvider
	core.ResourceProvider
	calls atomic.Int32
}

func (p *resourcePresetProvider) ListResources(ctx context.Context, kind string, preset bool, page, size int) (core.ResourcePage, error) {
	p.calls.Add(1)
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || scope.OwnerID != identity.ProviderOwnerID(ctx) || scope.Revision != core.ProviderRevision(ctx) || !preset {
		return core.ResourcePage{}, fmt.Errorf("unscoped preset")
	}
	return core.ResourcePage{Page: page, PageSize: size, Resources: []core.ProviderResource{{RemoteID: "private-official-voice", Name: "Official voice", Kind: kind, Subtype: "preset", Status: core.ProviderSucceeded, IsPreset: true}}}, nil
}
func resourceCoworkerKey(t *testing.T, f *activeHTTPFixture, role string) (string, string) {
	t.Helper()
	v := f.current(t)
	_, u, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	if _, e := f.centralDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,$3)`, v.Tokens.Company, u.ID, role); e != nil {
		t.Fatal(e)
	}
	key, secret, e := apikey.Generate(apikey.CreateInput{Name: "resource coworker", UserID: u.ID, OrganizationID: v.Tokens.Company, ResourceMode: "shared", StorageMode: "shared"}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = postgres.NewAPIKeyRepository(f.centralDB).Create(context.Background(), v.Tokens.Actor, key, apikey.Hash(secret)); e != nil {
		t.Fatal(e)
	}
	return u.ID, secret
}
func TestCompanyFacadePresetCustodyAndRecipientSemantics(t *testing.T) {
	native := &resourcePresetProvider{}
	var revision atomic.Value
	revision.Store(resourceFixtureRevision)
	f, _, _ := companyResourceFixture(t, func(d *api.Dependencies, f *activeHTTPFixture) {
		d.Service = resourcePresetCatalog{d.Service}
		d.ResourceDirectory = resourcePresetDirectory{provider: native}
		d.ResourceScopes = func(ctx context.Context, _ string) (core.ProviderConnectionScope, error) {
			return core.ProviderConnectionScope{ID: "kling", OwnerID: identity.ProviderOwnerID(ctx), Revision: revision.Load().(string)}, nil
		}
	})
	v := f.current(t)
	path := "/v1/provider-resource-presets?provider=kling&workflow_model=preset-consumer&kind=voice&page=1&size=10"
	discovery := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, discovery, 200)
	for _, secret := range []string{"private-official-voice", resourceFixtureRevision, "remote_id", "provider_owner_id"} {
		if strings.Contains(discovery.Body.String(), secret) {
			t.Fatal("private preset descriptor leaked")
		}
	}
	var list struct {
		Data []struct {
			Ticket string `json:"import_ticket"`
		}
	}
	if json.Unmarshal(discovery.Body.Bytes(), &list) != nil || len(list.Data) != 1 || list.Data[0].Ticket == "" {
		t.Fatal("missing opaque preset ticket")
	}
	ticket := list.Data[0].Ticket
	body := fmt.Sprintf(`{"provider":"kling","workflow_model":"preset-consumer","ticket":%q}`, ticket)
	// Company custody deliberately permits another valid content recipient to use
	// the ticket. It is not an individual-importer grant.
	_, memberKey := resourceCoworkerKey(t, f, "member")
	imported := facadeRequest(t, f, "POST", "/v1/provider-resource-presets/import", body, memberKey, true, nil)
	expectFixture(t, imported, 200)
	var binding providerresources.Binding
	if json.Unmarshal(imported.Body.Bytes(), &binding) != nil || binding.ID == "" || !binding.IsPreset {
		t.Fatal("missing persisted official binding")
	}
	repeat := resourceDirect(f, "POST", "/v1/provider-resource-presets/import", body, v.Tokens.PlaygroundKey, "")
	expectFixture(t, repeat, 200)
	var second providerresources.Binding
	json.Unmarshal(repeat.Body.Bytes(), &second)
	if second.ID != binding.ID {
		t.Fatal("company preset import duplicated by recipient")
	}
	_, otherKey := resourceCoworkerKey(t, f, "member")
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/provider-resources/"+binding.ID, "", otherKey, true, nil), 200)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/provider-resources?provider=kling&kind=voice", "", otherKey, true, nil), 200)
	bad := fmt.Sprintf(`{"provider":"kling","workflow_model":"preset-consumer","ticket":%q}`, "!"+ticket[1:])
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/provider-resource-presets/import", bad, memberKey, true, nil), 400)
	foreign, _, _ := companyResourceFixture(t, func(d *api.Dependencies, f *activeHTTPFixture) {
		d.Service = resourcePresetCatalog{d.Service}
		d.ResourceDirectory = resourcePresetDirectory{provider: native}
	})
	fv := foreign.current(t)
	expectFixture(t, facadeRequest(t, foreign, "POST", "/v1/provider-resource-presets/import", body, fv.Tokens.PlaygroundKey, true, nil), 400)
	revision.Store("changed-resource-connection")
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/provider-resource-presets/import", body, memberKey, true, nil), 404)
	if countFixture(t, f.centralDB, "provider_resources") != 1 {
		t.Fatal("invalid/replayed ticket created extra bindings")
	}
}

type companyAssetDirectory struct{ client *companyAssetClient }

func (d companyAssetDirectory) AssetLibrary(context.Context, string) (providerassets.Client, error) {
	return d.client, nil
}

type companyAssetClient struct {
	mu                          sync.Mutex
	assets                      map[string]providerassets.Asset
	created, deleted, inventory atomic.Int32
	t                           *testing.T
}

func (*companyAssetClient) Scope() string { return "company-asset-fixture" }
func (c *companyAssetClient) Groups(context.Context) ([]providerassets.Group, error) {
	c.inventory.Add(1)
	return nil, fmt.Errorf("global inventory forbidden")
}
func (c *companyAssetClient) List(context.Context, string, string, string) (providerassets.Page, error) {
	c.inventory.Add(1)
	return providerassets.Page{}, fmt.Errorf("global inventory forbidden")
}
func (*companyAssetClient) CreateGroup(_ context.Context, name string) (providerassets.Group, error) {
	return providerassets.Group{ID: "group-fixture", Name: name}, nil
}
func (c *companyAssetClient) Create(_ context.Context, group, source, name, kind string) (providerassets.Asset, error) {
	u, e := url.Parse(source)
	if e != nil || u.Scheme != "https" || u.Host != "console.sociaro.ai" || !strings.HasPrefix(u.Path, "/references/") || u.Query().Get("signature") == "" {
		c.t.Fatal("registration did not use central inspected reference URL")
	}
	c.created.Add(1)
	a := providerassets.Asset{ID: uuid.NewString(), GroupID: group, Name: name, Type: kind, Status: "Active"}
	c.mu.Lock()
	c.assets[a.ID] = a
	c.mu.Unlock()
	return a, nil
}
func (c *companyAssetClient) Get(_ context.Context, id string) (providerassets.Asset, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.assets[id]
	if !ok {
		return a, providerassets.ErrNotFound
	}
	return a, nil
}
func (c *companyAssetClient) Delete(_ context.Context, id string) error {
	c.deleted.Add(1)
	c.mu.Lock()
	delete(c.assets, id)
	c.mu.Unlock()
	return nil
}
func TestCompanyFacadeOrdinaryAssetsKeepInventoryDenied(t *testing.T) {
	client := &companyAssetClient{assets: map[string]providerassets.Asset{}, t: t}
	f, library, ctx := companyResourceFixture(t, func(d *api.Dependencies, f *activeHTTPFixture) {
		d.ProviderAssets = providerassets.New(postgres.NewProviderAssetRepository(f.centralDB), companyAssetDirectory{client})
	})
	v := f.current(t)
	base := "/v1/references/providers/bytedance"
	created := facadeRequest(t, f, "POST", base+"/groups", `{"name":"Characters"}`, v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, created, 201)
	for _, w := range []*httptest.ResponseRecorder{resourceDirect(f, "GET", base+"/groups", "", v.Tokens.PlaygroundKey, ""), facadeRequest(t, f, "GET", base+"/groups", "", v.Tokens.PlaygroundKey, true, nil)} {
		expectFixture(t, w, 200)
		if !strings.Contains(w.Body.String(), "Characters") || !strings.Contains(w.Body.String(), `"can_import":false`) {
			t.Fatal("ordinary group missing or inventory privilege leaked")
		}
	}
	ref, e := library.Upload(ctx, "asset.png", bytes.NewReader(testmedia.PNG512("asset fixture")))
	if e != nil {
		t.Fatal(e)
	}
	body := fmt.Sprintf(`{"reference_id":%q,"group_id":"group-fixture"}`, ref.ID)
	registered := facadeRequest(t, f, "POST", base+"/assets", body, v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, registered, 202)
	var record providerassets.Record
	if json.Unmarshal(registered.Body.Bytes(), &record) != nil || record.ID == "" || record.Asset.ID == "" {
		t.Fatal("missing actual persisted asset")
	}
	for _, path := range []string{base + "/assets?offset=0", base + "/assets/" + record.ID} {
		for _, w := range []*httptest.ResponseRecorder{resourceDirect(f, "GET", path, "", v.Tokens.PlaygroundKey, ""), facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)} {
			expectFixture(t, w, 200)
			if !strings.Contains(w.Body.String(), record.ID) {
				t.Fatal("nonempty asset missing")
			}
		}
	}
	expectFixture(t, facadeRequest(t, f, "POST", base+"/assets", `{"reference_id":"`+ref.ID+`","group_id":"group-fixture","url":"https://foreign.invalid/input"}`, v.Tokens.PlaygroundKey, true, nil), 403)
	for _, path := range []string{base + "/inventory", base + "/groups?inventory=1"} {
		expectFixture(t, facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil), 403)
	}
	expectFixture(t, facadeRequest(t, f, "POST", base+"/import", `{"asset_id":"foreign"}`, v.Tokens.PlaygroundKey, true, nil), 403)
	if client.inventory.Load() != 0 || client.created.Load() != 1 {
		t.Fatal("global inventory or duplicate registration invoked")
	}
	expectFixture(t, facadeRequest(t, f, "DELETE", base+"/assets/"+record.ID+"?detach=1", "", v.Tokens.PlaygroundKey, true, nil), 204)
	if client.deleted.Load() != 0 {
		t.Fatal("detach called provider deletion")
	}
	expectFixture(t, facadeRequest(t, f, "GET", base+"/assets/"+record.ID, "", v.Tokens.PlaygroundKey, true, nil), 404)
}

func TestCompanyFacadeResourceReadRolesAndCompletedLifecycle(t *testing.T) {
	f, _, ctx := companyResourceFixture(t, func(d *api.Dependencies, _ *activeHTTPFixture) {
		d.ResourceDirectory = resourcePresetDirectory{provider: &resourcePresetProvider{}}
	})
	v := f.current(t)
	repo := postgres.NewProviderResourceRepository(f.centralDB)
	job, created, e := repo.ReserveCreate(ctx, providerresources.Job{ProviderID: "kling", ConnectionID: "kling", Revision: resourceFixtureRevision, Intent: providerresources.Intent{Kind: "voice", Subtype: "custom", Name: "Completed fixture voice"}}, "completed-fixture")
	if e != nil || !created {
		t.Fatal(e)
	}
	if _, _, e = repo.StartJob(ctx, job.ID); e != nil {
		t.Fatal(e)
	}
	binding, e := repo.CompleteCreate(ctx, job.ID, "private-completed-resource")
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/v1/provider-resources/" + binding.ID, "/v1/provider-resources?provider=kling&kind=voice"} {
		for _, w := range []*httptest.ResponseRecorder{resourceDirect(f, "GET", path, "", v.Tokens.PlaygroundKey, ""), facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)} {
			expectFixture(t, w, 200)
			if !strings.Contains(w.Body.String(), binding.ID) || strings.Contains(w.Body.String(), "private-completed-resource") {
				t.Fatal("completed binding missing or private ID leaked")
			}
		}
	}
	deletion, _, e := repo.BeginDelete(ctx, binding.ID, "completed-delete")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = repo.StartJob(ctx, deletion.ID); e != nil {
		t.Fatal(e)
	}
	if e = repo.CompleteDelete(ctx, deletion.ID); e != nil {
		t.Fatal(e)
	}
	path := "/v1/provider-resource-jobs/" + deletion.ID
	for _, w := range []*httptest.ResponseRecorder{resourceDirect(f, "GET", path, "", v.Tokens.PlaygroundKey, ""), facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, false, nil)} {
		expectFixture(t, w, 200)
		if !strings.Contains(w.Body.String(), `"status":"succeeded"`) {
			t.Fatal("completed lifecycle job missing")
		}
	}
	_, member := resourceCoworkerKey(t, f, "member")
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/provider-resources/"+binding.ID, "", member, true, nil), 404)
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET role='content_officer' WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	list := "/v1/provider-resources?provider=kling"
	expectFixture(t, resourceDirect(f, "GET", list, "", v.Tokens.PlaygroundKey, ""), 200)
	expectFixture(t, facadeRequest(t, f, "GET", list, "", v.Tokens.PlaygroundKey, true, nil), 200)
	expectFixture(t, resourceDirect(f, "HEAD", list, "", v.Tokens.PlaygroundKey, ""), 403)
	expectFixture(t, facadeRequest(t, f, "HEAD", list, "", v.Tokens.PlaygroundKey, true, nil), 403)
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/provider-resources/estimate", `{"provider":"kling","workflow_model":"x","kind":"voice","name":"x"}`, v.Tokens.PlaygroundKey, true, nil), 403)
	expectFixture(t, facadeRequest(t, f, "GET", list, "", "", true, nil), 403)
}
