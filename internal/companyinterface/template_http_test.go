package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

type templateWorkflowProvider struct{ *mock.Adapter }

func (p templateWorkflowProvider) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{{ID: "workflow-image", MediaType: "image", SupportsImageInput: true, MaxImageInputs: 2, ReferencePromptSyntax: "numbered", ReferencePromptTypes: []string{"image"}, ReferenceModes: []core.ReferenceMode{{ID: "references", MaxImages: 2}}, ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 2, Image: core.MediaFileLimits{ContentTypes: []string{"image/png"}, MaxBytes: 1 << 20, MinDimension: 1}}, Resolutions: []string{"1024*1024"}, Ratios: []string{"1:1"}, Pricing: core.MediaModelPricing{Known: true}}}, nil
}

type templateWorkflowQueue struct{ core.TaskQueue }

func (templateWorkflowQueue) Enqueue(context.Context, string) error { return nil }

func TestCompanyFacadePublishedTemplateWorkflow(t *testing.T) {
	f, _ := facadeHTTP(t)
	v := f.current(t)
	ctx := context.Background()
	sys := postgres.NewSystemRepository(f.centralDB)
	if e := sys.SetAdmin(ctx, "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
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
	repo := postgres.NewTemplateRepository(f.centralDB)
	t.Cleanup(func() { repo.Close() })
	provider := templateWorkflowProvider{mock.New(0, "https://unused.fixture.test")}
	service := core.NewTaskServiceWithProviders(postgres.NewTaskRepository(f.centralDB), templateWorkflowQueue{}, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "workflow-image"}, "mock")
	service.EnableAccounting()
	// Mock-money fixture only: ordinary shared-key admission requires positive
	// available company budget even for a zero-price mock request.
	if _, e = f.centralDB.Exec(`UPDATE organizations SET credit_limit_micros=1000000 WHERE id=$1`, v.Tokens.Company); e != nil {
		t.Fatal(e)
	}
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET weekly_limit_micros=NULL WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	f.central, e = api.WithPublicAPI(api.NewGateway(api.Dependencies{Service: service, CompanyInterface: lifecycle, CompanyCustomer: postgres.NewCompanyInterfaceAuthority(f.centralDB), CompanyCentralOrigin: fixtureCentral, Auth: a, System: sys, Keys: postgres.NewAPIKeyRepository(f.centralDB), Templates: repo, Storage: store, SigningSecret: fixtureMaster, AssetPublicURL: fixtureCentral, MediaURLTTL: time.Hour, References: references.New(postgres.NewReferenceRepository(f.centralDB), store)}), "", fixtureCentral, "")
	if e != nil {
		t.Fatal(e)
	}
	direct := func(method, path string, value any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(value)
		r := httptest.NewRequest(method, fixtureCentral+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(f.centralCookie)
		out := httptest.NewRecorder()
		f.central.ServeHTTP(out, r)
		return out
	}
	id := uuid.NewString()
	base := "/v1/templates/" + id
	d := templates.Definition{Title: "Central published workflow", MediaType: "image", Provider: "mock", Model: "workflow-image", Syntax: "numbered", Prompt: "Image 1", Options: core.MediaOptions{ReferenceMode: "references", Resolution: "1024*1024", Ratio: "1:1"}, Slots: []templates.Slot{{ID: "image:1", Type: "image", Index: 1, Role: "reference", Label: "Subject"}}}
	created := direct("POST", "/v1/system/templates", map[string]any{"id": id, "draft": d})
	expectFixture(t, created, 201)
	var multipartBody bytes.Buffer
	multi := multipart.NewWriter(&multipartBody)
	file, _ := multi.CreateFormFile("file", "fixture.png")
	file.Write(testmedia.PNG512("company template"))
	multi.Close()
	upload := httptest.NewRequest("POST", fixtureCentral+"/v1/system/templates/"+id+"/assets", &multipartBody)
	upload.Header.Set("Content-Type", multi.FormDataContentType())
	upload.AddCookie(f.centralCookie)
	uploaded := httptest.NewRecorder()
	f.central.ServeHTTP(uploaded, upload)
	expectFixture(t, uploaded, 201)
	var asset templates.Asset
	if e = json.Unmarshal(uploaded.Body.Bytes(), &asset); e != nil || asset.ID == "" {
		t.Fatal("asset missing", e)
	}
	d.CoverID = asset.ID
	d.Slots[0].AssetID = asset.ID
	published := direct("PUT", "/v1/system/templates/"+id, map[string]any{"version": 1, "action": "publish", "draft": d})
	expectFixture(t, published, 200)
	for _, path := range []string{"/v1/templates", base, base + "?revision=2", base + "/metadata", "/v1/system/template-references?offset=0"} {
		out := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, out, 200)
	}
	for _, method := range []string{"GET", "HEAD"} {
		content := facadeRequest(t, f, method, "/v1/system/template-references/"+asset.ID+"/content", "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, content, 200)
		if content.Header().Get("Content-Type") != "image/png" || method == "GET" && !bytes.Equal(content.Body.Bytes(), testmedia.PNG512("company template")) || method == "HEAD" && content.Body.Len() != 0 {
			t.Fatal("shared media response changed")
		}
	}
	// Even the real central Administrator cannot carry authorship through the grant.
	expectFixture(t, facadeRequest(t, f, "PUT", "/v1/system/templates/"+id, `{"version":2,"action":"unpublish"}`, "", true, nil), 403)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/system/templates/library", "", "", true, nil), 403)
	input := fmt.Sprintf(`{"revision":2,"provider":"mock","model":"workflow-image","inputs":{},"max_expected_cost_usd":0}`)
	// Whitespace is legitimate JSON and proves the existing 16KiB limit survives both hops.
	padded := strings.Repeat(" ", 9000) + input
	editing := facadeRequest(t, f, "POST", base+"/editing", padded, v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, editing, 200)
	if !strings.Contains(editing.Body.String(), asset.ID) {
		t.Fatal("fixed asset missing from editable snapshot")
	}
	expectFixture(t, facadeRequest(t, f, "POST", base+"/validate-media", input, v.Tokens.PlaygroundKey, true, nil), 200)
	expectFixture(t, facadeRequest(t, f, "POST", base+"/estimate", input, v.Tokens.PlaygroundKey, true, nil), 200)
	expectFixture(t, facadeRequest(t, f, "POST", base+"/editing", strings.Repeat(" ", 16384)+input, v.Tokens.PlaygroundKey, true, nil), 403)
	generation := facadeRequest(t, f, "POST", base+"/generations", input, v.Tokens.PlaygroundKey, true, func(r *http.Request) { r.Header.Set("Idempotency-Key", "template-admission-one") })
	expectFixture(t, generation, 202)
	var result struct{ ID string }
	json.Unmarshal(generation.Body.Bytes(), &result)
	if result.ID == "" {
		t.Fatal("missing task id")
	}
	replay := facadeRequest(t, f, "POST", base+"/generations", input, v.Tokens.PlaygroundKey, true, func(r *http.Request) { r.Header.Set("Idempotency-Key", "template-admission-one") })
	expectFixture(t, replay, 202)
	var same struct{ ID string }
	json.Unmarshal(replay.Body.Bytes(), &same)
	if same.ID != result.ID {
		t.Fatal("idempotency key lost")
	}
	// Direct central edits are immediately visible; previous revision is rejected.
	d.Title = "Updated centrally"
	d.Prompt = "Image 1 transformed"
	expectFixture(t, direct("PUT", "/v1/system/templates/"+id, map[string]any{"version": 2, "draft": d}), 200)
	metadata := facadeRequest(t, f, "GET", base+"/metadata", "", v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, metadata, 200)
	if !strings.Contains(metadata.Body.String(), d.Title) {
		t.Fatal("central edit hidden")
	}
	expectFixture(t, facadeRequest(t, f, "GET", base+"?revision=2", "", v.Tokens.PlaygroundKey, true, nil), 409)
	expectFixture(t, direct("PUT", "/v1/system/templates/"+id, map[string]any{"version": 3, "action": "unpublish"}), 200)
	expectFixture(t, facadeRequest(t, f, "GET", base+"/metadata", "", v.Tokens.PlaygroundKey, true, nil), 404)
	var snapshot []byte
	e = f.centralDB.QueryRow(`SELECT template_snapshot FROM tasks WHERE id=$1`, result.ID).Scan(&snapshot)
	var saved struct {
		ID       string
		Revision int64
		Prompt   string
	}
	if e != nil || json.Unmarshal(snapshot, &saved) != nil || saved.ID != id || saved.Revision != 2 || saved.Prompt != "Image 1" || !strings.Contains(string(snapshot), asset.ID) {
		t.Fatal("admitted snapshot lost", e)
	}
	if countFixture(t, f.localDB, "generation_templates") != 0 || countFixture(t, f.localDB, "tasks") != 0 {
		t.Fatal("local shadow business data")
	}
}
