package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

const resourceFixtureRevision = "company-resource-fixture-revision"

type noResourceNetwork struct{ t *testing.T }

func (p noResourceNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	p.t.Fatal("resource fixture attempted external provider I/O")
	return nil, fmt.Errorf("forbidden fixture network")
}

type companyResourceProvider struct{ core.MediaProvider }

func (p companyResourceProvider) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	return p.MediaProvider.(core.ModelCatalog).ListModels(ctx)
}
func (p companyResourceProvider) PreparationModels(ctx context.Context) ([]core.MediaModel, error) {
	return p.MediaProvider.(core.MediaPreparationCatalog).PreparationModels(ctx)
}
func resourceFixtureScope(ctx context.Context, _ string) (core.ProviderConnectionScope, error) {
	return core.ProviderConnectionScope{ID: "kling", OwnerID: identity.ProviderOwnerID(ctx), Revision: resourceFixtureRevision}, nil
}
func resourceFixturePinned(ctx context.Context) context.Context {
	scope, _ := resourceFixtureScope(ctx, "kling")
	return core.WithProviderConnectionScope(ctx, scope)
}
func (p companyResourceProvider) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	return p.MediaProvider.(core.RequestValidator).ValidateRequest(resourceFixturePinned(ctx), r)
}
func (p companyResourceProvider) Quote(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	q, _, e := p.MediaProvider.(interface {
		Quote(context.Context, core.MediaModel, core.MediaRequest) (core.TaskCost, string, error)
	}).Quote(resourceFixturePinned(ctx), m, r)
	return q, resourceFixtureRevision, e
}

func companyResourceFixture(t *testing.T, configure ...func(*api.Dependencies, *activeHTTPFixture)) (*activeHTTPFixture, *references.Library, context.Context) {
	t.Helper()
	f, _ := facadeHTTP(t)
	v := f.current(t)
	ctx := context.Background()
	a, e := auth.New(ctx, postgres.NewAuthRepository(f.centralDB), auth.Config{SessionSecret: fixtureMaster, PublicURL: fixtureCentral})
	if e != nil {
		t.Fatal(e)
	}
	lifecycle, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	store, e := storage.NewLocal(t.TempDir(), http.DefaultClient, 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	library := references.New(postgres.NewReferenceRepository(f.centralDB), store)
	native, e := kling.Definition().Factory(providerkit.Credentials{APIKey: "synthetic-resource-fixture", Extra: map[string]string{"selection_add_price": "0.01"}}, nil, &http.Client{Transport: noResourceNetwork{t}})
	if e != nil {
		t.Fatal(e)
	}
	provider := companyResourceProvider{native}
	service := core.NewTaskServiceWithProviders(postgres.NewTaskRepository(f.centralDB), templateWorkflowQueue{}, map[string]core.MediaProvider{"kling": provider}, map[string]string{"kling": kling.TextToSpeech}, "kling")
	service.EnableAccounting()
	if _, e = f.centralDB.Exec(`UPDATE organizations SET credit_limit_micros=1000000 WHERE id=$1`, v.Tokens.Company); e != nil {
		t.Fatal(e)
	}
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET weekly_limit_micros=NULL WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	dependencies := api.Dependencies{Service: service, CompanyInterface: lifecycle, CompanyCustomer: postgres.NewCompanyInterfaceAuthority(f.centralDB), CompanyCentralOrigin: fixtureCentral, Auth: a, System: postgres.NewSystemRepository(f.centralDB), Keys: postgres.NewAPIKeyRepository(f.centralDB), Storage: store, SigningSecret: fixtureMaster, AssetPublicURL: fixtureCentral, MediaURLTTL: time.Hour, References: library, ResourceRepository: postgres.NewProviderResourceRepository(f.centralDB), ResourceScopes: resourceFixtureScope}
	for _, fn := range configure {
		fn(&dependencies, f)
	}
	f.central, e = api.WithPublicAPI(api.NewGateway(dependencies), "", fixtureCentral, "")
	if e != nil {
		t.Fatal(e)
	}
	ctx = identity.WithOrganizationRole(identity.WithUser(ctx, identity.User{ID: v.Tokens.Actor}), v.Tokens.Company, "supermanager")
	ctx = identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
	return f, library, ctx
}
func resourceDirect(f *activeHTTPFixture, method, path, body, key, idem string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, fixtureCentral+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if idem != "" {
		r.Header.Set("Idempotency-Key", idem)
	}
	w := httptest.NewRecorder()
	f.central.ServeHTTP(w, r)
	return w
}
func TestCompanyFacadeResourcePreparationAndAdmission(t *testing.T) {
	f, library, ctx := companyResourceFixture(t)
	v := f.current(t)
	path := "/v1/models/" + url.PathEscape(kling.TextToSpeech) + "/preparations?provider=kling"
	direct := resourceDirect(f, "GET", path, "", v.Tokens.PlaygroundKey, "")
	expectFixture(t, direct, 200)
	delegated := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, delegated, 200)
	var a, b struct {
		WorkflowModel string `json:"workflow_model"`
		Data          []struct{ Operation string }
	}
	if json.Unmarshal(direct.Body.Bytes(), &a) != nil || json.Unmarshal(delegated.Body.Bytes(), &b) != nil || a.WorkflowModel != kling.TextToSpeech || b.WorkflowModel != a.WorkflowModel || len(a.Data) == 0 || len(a.Data) != len(b.Data) {
		t.Fatal("actual preparation catalogue differs")
	}
	for i := range a.Data {
		if a.Data[i].Operation != b.Data[i].Operation {
			t.Fatal("wrong preparation operation")
		}
	}
	wav := make([]byte, 44+8000*6)
	copy(wav, testmedia.WAV()[:44])
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(wav)-44))
	ref, e := library.Upload(ctx, "voice.wav", bytes.NewReader(wav))
	if e != nil {
		t.Fatal(e)
	}
	body := fmt.Sprintf(`{"provider":"kling","workflow_model":%q,"kind":"voice","name":"Company voice","reference_ids":[%q],"max_expected_usd":0.007}`, kling.TextToSpeech, ref.ID)
	expectFixture(t, resourceDirect(f, "POST", "/v1/provider-resources/estimate", body, v.Tokens.PlaygroundKey, ""), 200)
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/provider-resources/estimate", body, v.Tokens.PlaygroundKey, true, nil), 200)
	before := countFixture(t, f.centralDB, "tasks")
	direct = resourceDirect(f, "POST", "/v1/provider-resources", body, v.Tokens.PlaygroundKey, "resource-admission")
	expectFixture(t, direct, 202)
	delegated = facadeRequest(t, f, "POST", "/v1/provider-resources", body, v.Tokens.PlaygroundKey, true, func(r *http.Request) { r.Header.Set("Idempotency-Key", "resource-admission") })
	expectFixture(t, delegated, 202)
	var x, y struct {
		ID        string
		StatusURL string `json:"status_url"`
	}
	if json.Unmarshal(direct.Body.Bytes(), &x) != nil || json.Unmarshal(delegated.Body.Bytes(), &y) != nil || x.ID == "" || x.ID != y.ID {
		t.Fatal("resource replay changed admitted task")
	}
	if direct.Header().Get("Location") != "" || delegated.Header().Get("Location") != "" {
		t.Fatal("resource handler must not invent Location")
	}
	if x.StatusURL != "/v1/generations/"+x.ID || y.StatusURL != ci.LocalCustomerPrefix+"v1/generations/"+x.ID {
		t.Fatalf("resource status URL not mapped: direct=%q facade=%q", x.StatusURL, y.StatusURL)
	}
	if countFixture(t, f.centralDB, "tasks") != before+1 {
		t.Fatal("resource replay duplicated admission")
	}
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/provider-resources?provider=kling&kind=voice&limit=10&offset=0", "", v.Tokens.PlaygroundKey, false, nil), 200)
}
