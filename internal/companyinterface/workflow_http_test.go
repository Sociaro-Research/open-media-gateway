package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

type allWorkflowProvider struct{ templateWorkflowProvider }

func (p allWorkflowProvider) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	native, e := p.Adapter.ListModels(ctx)
	if e != nil {
		return nil, e
	}
	custom, e := p.templateWorkflowProvider.ListModels(ctx)
	return append(native, custom...), e
}

func generationWorkflowFixture(t *testing.T) (*activeHTTPFixture, *references.Library, context.Context) {
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
	// Reuse the free, media-capable workflow fixture model; no worker/provider I/O.
	provider := allWorkflowProvider{}
	provider.Adapter = mock.New(0, "https://unused.fixture.test")
	service := core.NewTaskServiceWithProviders(postgres.NewTaskRepository(f.centralDB), templateWorkflowQueue{}, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "workflow-image"}, "mock")
	service.EnableAccounting()
	if _, e = f.centralDB.Exec(`UPDATE organizations SET credit_limit_micros=1000000 WHERE id=$1`, v.Tokens.Company); e != nil {
		t.Fatal(e)
	}
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET weekly_limit_micros=NULL WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	f.central, e = api.WithPublicAPI(api.NewGateway(api.Dependencies{Service: service, CompanyInterface: lifecycle, CompanyCustomer: postgres.NewCompanyInterfaceAuthority(f.centralDB), CompanyCentralOrigin: fixtureCentral, Auth: a, System: postgres.NewSystemRepository(f.centralDB), Keys: postgres.NewAPIKeyRepository(f.centralDB), Storage: store, SigningSecret: fixtureMaster, AssetPublicURL: fixtureCentral, MediaURLTTL: time.Hour, References: library}), "", fixtureCentral, "")
	if e != nil {
		t.Fatal(e)
	}
	ctx = identity.WithOrganizationRole(identity.WithUser(ctx, identity.User{ID: v.Tokens.Actor}), v.Tokens.Company, "supermanager")
	ctx = identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
	return f, library, ctx
}

func TestCompanyFacadeGenerationJSONAndReferenceWorkflow(t *testing.T) {
	f, library, ctx := generationWorkflowFixture(t)
	v := f.current(t)
	image, e := library.Upload(ctx, "workflow.png", bytes.NewReader(testmedia.PNG512("workflow inputs")))
	if e != nil {
		t.Fatal(e)
	}
	list := facadeRequest(t, f, "GET", "/v1/references?offset=0", "", v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, list, 200)
	var refs struct{ Data []struct{ ID, URL string } }
	if json.Unmarshal(list.Body.Bytes(), &refs) != nil || len(refs.Data) != 1 || refs.Data[0].ID != image.ID {
		t.Fatal("central reference absent")
	}
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/organizations/"+v.Tokens.Company+"/references?offset=0", "", "", true, nil), 200)
	validate := fmt.Sprintf(`{"validation":{"provider":"mock","model":"workflow-image","options":{"reference_mode":"references"}},"reference_ids":["%s"]}`, image.ID)
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/references/validate", strings.Repeat(" ", 50000)+validate, v.Tokens.PlaygroundKey, true, nil), 200)
	bodies := []struct{ path, body string }{
		{"/v1/videos", `{"model":"mock/mock-video-v1","prompt":"Video fixture","max_expected_cost_usd":0}`},
		{"/v1/audio/speech", `{"model":"mock/mock-speech-v1","input":"Speech fixture","voice":"Mock","max_expected_cost_usd":0}`},
		{"/v1/audio/generations", `{"model":"mock/mock-music-v1","prompt":"Music fixture","max_expected_cost_usd":0}`},
		{"/v1/generations", `{"model":"mock/workflow-image","prompt":"Image 1","image":"` + fixtureCentral + refs.Data[0].URL + `","options":{"reference_mode":"references"},"max_expected_cost_usd":0}`},
		{"/v1/images/generations", `{"model":"mock/workflow-image","prompt":"Image 1","image":"` + fixtureCentral + refs.Data[0].URL + `","options":{"reference_mode":"references"},"max_expected_cost_usd":0}`},
		{"/v1/images/edits", `{"model":"mock/workflow-image","prompt":"Image 1","image":"` + fixtureCentral + refs.Data[0].URL + `","options":{"reference_mode":"references"},"max_expected_cost_usd":0}`},
	}
	for i, tc := range bodies {
		t.Run(tc.path, func(t *testing.T) {
			key := fmt.Sprintf("workflow-admission-%d", i)
			headers := func(r *http.Request) { r.Header.Set("Idempotency-Key", key) }
			if tc.path != "/v1/audio/speech" {
				expectFixture(t, facadeRequest(t, f, "POST", "/v1/generations/estimate", tc.body, v.Tokens.PlaygroundKey, true, nil), 200)
			}
			created := facadeRequest(t, f, "POST", tc.path, strings.Repeat(" ", 50000)+tc.body, v.Tokens.PlaygroundKey, true, headers)
			expectFixture(t, created, 202)
			var task struct {
				ID, StatusURL string `json:"-"`
			}
			var fields map[string]json.RawMessage
			json.Unmarshal(created.Body.Bytes(), &fields)
			json.Unmarshal(fields["id"], &task.ID)
			json.Unmarshal(fields["status_url"], &task.StatusURL)
			statusPath := "/v1/generations/" + task.ID
			if tc.path == "/v1/videos" {
				statusPath = "/v1/videos/" + task.ID
			}
			want := "/auth/company-interface/local/customer" + statusPath
			if task.ID == "" || created.Header().Get("Location") != want || task.StatusURL != want {
				t.Fatalf("status route lost: Location=%q status=%q", created.Header().Get("Location"), task.StatusURL)
			}
			expectFixture(t, facadeRequest(t, f, "GET", statusPath, "", v.Tokens.PlaygroundKey, false, nil), 200)
			replay := facadeRequest(t, f, "POST", tc.path, tc.body, v.Tokens.PlaygroundKey, true, headers)
			expectFixture(t, replay, 202)
			var repeated struct{ ID string }
			json.Unmarshal(replay.Body.Bytes(), &repeated)
			if repeated.ID != task.ID {
				t.Fatal("mutation replay duplicated task")
			}
			changed := strings.Replace(tc.body, "fixture", "changed fixture", 1)
			if changed == tc.body {
				changed = strings.Replace(tc.body, "Image 1", "Image 1 changed", 1)
			}
			expectFixture(t, facadeRequest(t, f, "POST", tc.path, changed, v.Tokens.PlaygroundKey, true, headers), 409)
		})
	}
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/generations", strings.Repeat(" ", 65536)+bodies[0].body, v.Tokens.PlaygroundKey, true, nil), 403)
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/images/edits", "multipart is not a JSON contract", v.Tokens.PlaygroundKey, true, func(r *http.Request) { r.Header.Set("Content-Type", "multipart/form-data; boundary=fixture") }), 403)
	unused, e := library.Upload(ctx, "unused.png", bytes.NewReader(testmedia.PNG512("unused")))
	if e != nil {
		t.Fatal(e)
	}
	expectFixture(t, facadeRequest(t, f, "DELETE", "/v1/references/"+unused.ID, "", v.Tokens.PlaygroundKey, true, nil), 204)
	sys := postgres.NewSystemRepository(f.centralDB)
	if e := sys.SetAdmin(ctx, "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	foreignCompany, e := sys.CreateOrganization(context.Background(), v.Tokens.Actor, "Foreign references", v.Tokens.Actor, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	foreignContext := identity.WithOrganizationRole(ctx, foreignCompany.ID, "supermanager")
	foreign, e := library.Upload(foreignContext, "foreign.png", bytes.NewReader(testmedia.PNG512("foreign")))
	if e != nil {
		t.Fatal(e)
	}
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/references/validate", fmt.Sprintf(`{"reference_ids":["%s"]}`, foreign.ID), v.Tokens.PlaygroundKey, true, nil), 404)
	expectFixture(t, facadeRequest(t, f, "DELETE", "/v1/references/"+foreign.ID, "", v.Tokens.PlaygroundKey, true, nil), 404)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/organizations/"+foreignCompany.ID+"/references", "", "", true, nil), 403)
	if _, e = library.Get(foreignContext, foreign.ID); e != nil {
		t.Fatal("foreign reference changed", e)
	}
	unused, e = library.Upload(ctx, "manager-delete.png", bytes.NewReader(testmedia.PNG512("manager delete")))
	if e != nil {
		t.Fatal(e)
	}
	expectFixture(t, facadeRequest(t, f, "DELETE", "/v1/organizations/"+v.Tokens.Company+"/references/"+unused.ID, "", "", true, nil), 204)
	if countFixture(t, f.localDB, "tasks") != 0 || countFixture(t, f.localDB, "reference_images") != 0 {
		t.Fatal("local shadow content")
	}
}

func TestCompanyFacadeGenerationHEADMatchesCentral(t *testing.T) {
	f, _, _ := generationWorkflowFixture(t)
	v := f.current(t)
	for _, tc := range []struct{ path, model string }{{"/v1/generations", "mock/workflow-image"}, {"/v1/videos", "mock/mock-video-v1"}} {
		t.Run(tc.path, func(t *testing.T) {
			created := facadeRequest(t, f, "POST", tc.path, fmt.Sprintf(`{"model":%q,"prompt":"HEAD fixture","max_expected_cost_usd":0}`, tc.model), v.Tokens.PlaygroundKey, false, nil)
			expectFixture(t, created, 202)
			var task struct{ ID string }
			if json.Unmarshal(created.Body.Bytes(), &task) != nil || task.ID == "" {
				t.Fatal("missing central object")
			}
			path := tc.path + "/" + task.ID
			direct := httptest.NewRequest("HEAD", fixtureCentral+path, nil)
			direct.Header.Set("Authorization", "Bearer "+v.Tokens.PlaygroundKey)
			dw := httptest.NewRecorder()
			f.central.ServeHTTP(dw, direct)
			expectFixture(t, dw, 200)
			local := facadeRequest(t, f, "HEAD", path, "", v.Tokens.PlaygroundKey, false, nil)
			if local.Code != dw.Code {
				t.Errorf("HEAD parity: central=%d facade=%d", dw.Code, local.Code)
			}
			if dw.Body.Len() != 0 || local.Body.Len() != 0 {
				t.Error("HEAD must remain bodyless")
			}
			for _, name := range []string{"Content-Type", "ETag", "Last-Modified", "Retry-After"} {
				if local.Header().Get(name) != dw.Header().Get(name) {
					t.Errorf("HEAD %s: central=%q facade=%q", name, dw.Header().Get(name), local.Header().Get(name))
				}
			}
		})
	}
}
