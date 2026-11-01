package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

// The composition contract: real disposable PostgreSQL repositories and local
// storage, through authenticated HTTP handlers; synthetic media, no provider calls.
func TestTemplateSharedValidationPersistedLifecycle(t *testing.T) {
	db := workspaceDatabase(t)
	s, _, svc, _ := templateFixture(t)
	people := postgres.NewAuthRepository(db)
	alice, err := people.UpsertGoogleUser(context.Background(), uuid.NewString(), "template-owner@example.test", "Creator", "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := people.UpsertGoogleUser(context.Background(), uuid.NewString(), "template-consumer@example.test", "Consumer", "")
	if err != nil {
		t.Fatal(err)
	}
	s.auth = playgroundBrowserAuth{users: map[string]identity.User{"alice": alice, "bob": bob}}
	s.system.(*systemFixture).admins = map[string]bool{alice.ID: true}
	repo := postgres.NewTemplateRepository(db)
	defer repo.Close()
	s.templates = repo
	local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 300<<20)
	if err != nil {
		t.Fatal(err)
	}
	s.storage = local
	s.references = references.New(postgres.NewReferenceRepository(db), local)
	s.keys = postgres.NewAPIKeyRepository(db)
	h := s.handler()
	_, ownerKey := issueTestKey(t, h, "alice", `{"name":"Owner generation"}`)
	_, consumerKey := issueTestKey(t, h, "bob", `{"name":"Consumer generation"}`)
	call := func(method, path string, value any, actor, key string) *httpResult {
		raw, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		if actor != "" {
			r.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if strings.HasSuffix(path, "/generations") {
			r.Header.Set("Idempotency-Key", "synthetic-confirmed")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return &httpResult{w.Code, w.Body.String()}
	}
	count := func(table string) int {
		var n int
		if e := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	target := &referencevalidation.Target{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references"}}
	bad := testmedia.MP4(125956, 12288, 244)
	upload := validationUpload(t, h, "/v1/system/template-references", "", "alice", "timing.mp4", bad, target)
	if upload.Code != 400 || !strings.Contains(upload.Body.String(), "23.8041220744") || !strings.Contains(upload.Body.String(), "24–60 FPS") || count("reference_images") != 0 {
		t.Fatal("bad selected upload retained", upload.Code, upload.Body.String())
	}
	upload = validationUpload(t, h, "/v1/system/template-references", "", "alice", "malformed.mp4", []byte("malformed"), nil)
	if upload.Code != 400 || count("reference_images") != 0 {
		t.Fatal("bad generic upload retained", upload.Code)
	}
	// The same timing is valid for a model-neutral source library, but not for this model binding.
	upload = validationUpload(t, h, "/v1/system/template-references", "", "alice", "timing.mp4", bad, nil)
	if upload.Code != 201 {
		t.Fatal(upload.Code, upload.Body.String())
	}
	var incompatible references.Image
	json.Unmarshal(upload.Body.Bytes(), &incompatible)
	id := uuid.NewString()
	d := templates.Definition{Title: "Synthetic shared recipe", Provider: "mock", Model: "dance", MediaType: "video", Prompt: "Image 1 follows Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5, Resolution: "720p", Ratio: "16:9"}}
	d.Slots, err = d.InferSlots(templateTestModel())
	if err != nil {
		t.Fatal(err)
	}
	created := call("POST", "/v1/system/templates", map[string]any{"id": id, "version": 0, "draft": d}, "alice", "")
	if created.code != 201 {
		t.Fatal(created)
	}
	before := count("template_media")
	rejected := call("POST", "/v1/system/templates/"+id+"/assets", map[string]any{"library_reference_id": incompatible.ID, "validation": target}, "alice", "")
	if rejected.code != 400 || !strings.Contains(rejected.body, "23.8041220744") || count("template_media") != before {
		t.Fatal("incompatible source copied", rejected)
	}
	upload = validationUpload(t, h, "/v1/system/template-references", "", "alice", "valid.mp4", testmedia.MP4(5000, 1000, 150), target)
	if upload.Code != 201 {
		t.Fatal(upload.Code, upload.Body.String())
	}
	var source references.Image
	json.Unmarshal(upload.Body.Bytes(), &source)
	copied := call("POST", "/v1/system/templates/"+id+"/assets", map[string]any{"library_reference_id": source.ID, "validation": target}, "alice", "")
	if copied.code != 201 {
		t.Fatal(copied)
	}
	var asset templates.Asset
	json.Unmarshal([]byte(copied.body), &asset)
	if asset.LibraryReferenceID != source.ID || asset.ID != source.ID || asset.Name != "valid.mp4" {
		t.Fatal(asset)
	}
	for i := range d.Slots {
		if d.Slots[i].Type == "video" {
			d.Slots[i].AssetID = asset.ID
		}
	}
	d.CoverID = asset.ID
	checked := call("POST", "/v1/system/templates/"+id+"/validate-media", map[string]any{"draft": d}, "alice", "")
	if checked.code != 200 {
		t.Fatal(checked)
	}
	saved := call("PUT", "/v1/system/templates/"+id, map[string]any{"version": 1, "draft": d}, "alice", "")
	if saved.code != 200 {
		t.Fatal(saved)
	}
	published := call("PUT", "/v1/system/templates/"+id, map[string]any{"version": 2, "draft": d, "action": "publish"}, "alice", "")
	if published.code != 200 {
		t.Fatal(published)
	}
	persisted, e := repo.Get(context.Background(), id)
	if e != nil || persisted.Published == nil || persisted.Revision != 3 {
		t.Fatal(persisted, e)
	}
	// Consumers can read shared assets; authoring metadata remains restricted.
	for _, path := range []string{"/v1/system/template-references", "/v1/system/template-references/" + source.ID + "/content", "/v1/system/templates/" + id + "/assets/" + asset.ID + "/metadata"} {
		want := 200
		if strings.Contains(path, "/assets/") {
			want = 403
		}
		if w := sendTest(h, "GET", path, "", "", consumerKey); w.Code != want {
			t.Fatal("consumer source access", path, w.Code)
		}
	}
	if w := sendTest(h, "GET", "/v1/references", "", "", consumerKey); w.Code != 200 || strings.Contains(w.Body.String(), source.ID) {
		t.Fatal("source in consumer library", w.Code)
	}
	if w := sendTest(h, "DELETE", "/v1/system/template-references/"+source.ID, "", "alice", ""); w.Code != 409 {
		t.Fatal("retained source deleted", w.Code, w.Body.String())
	}
	// A corrupted shared checksum blocks selection; restoring the fixture restores every binding.
	if _, e = db.Exec(`UPDATE reference_images SET content_sha256=$1 WHERE id=$2`, strings.Repeat("0", 64), source.ID); e != nil {
		t.Fatal(e)
	}
	before = count("template_media")
	tampered := call("POST", "/v1/system/templates/"+id+"/assets", map[string]any{"library_reference_id": source.ID, "validation": target}, "alice", "")
	if tampered.code != 400 || count("template_media") != before {
		t.Fatal("tampered source copied", tampered)
	}
	if _, e = db.Exec(`UPDATE reference_images SET content_sha256=$1 WHERE id=$2`, source.SHA256, source.ID); e != nil {
		t.Fatal(e)
	}
	// Private variable inputs remain scoped to the consuming user.
	face := validationUpload(t, h, "/v1/references", consumerKey, "", "face.png", testmedia.PNG512("consumer"), nil)
	if face.Code != 201 {
		t.Fatal(face.Code, face.Body.String())
	}
	var own references.Image
	json.Unmarshal(face.Body.Bytes(), &own)
	inputs := map[string]string{"image:1": own.ID}
	request := map[string]any{"revision": 3, "provider": "mock", "model": "dance", "inputs": inputs, "max_expected_cost_usd": 0}
	checked = call("POST", "/v1/templates/"+id+"/validate-media", request, "", consumerKey)
	if checked.code != 200 {
		t.Fatal(checked)
	}
	if rejected = call("POST", "/v1/templates/"+id+"/validate-media", request, "", ownerKey); rejected.code != 404 {
		t.Fatal("other user's variable accepted", rejected)
	}
	inputs["video:1"] = own.ID
	if rejected = call("POST", "/v1/templates/"+id+"/validate-media", request, "", consumerKey); rejected.code != 400 || !strings.Contains(rejected.body, "Fixed references cannot be replaced") {
		t.Fatal(rejected)
	}
	delete(inputs, "video:1")
	inputs["image:1"] = source.ID
	if rejected = call("POST", "/v1/templates/"+id+"/validate-media", request, "", consumerKey); rejected.code != 400 {
		t.Fatal("wrong shared media type accepted as variable", rejected)
	}
	inputs["image:1"] = own.ID
	accepted := call("POST", "/v1/templates/"+id+"/generations", request, "", consumerKey)
	if accepted.code != 202 || svc.calls != 1 {
		t.Fatal("valid immutable recipe not admitted", accepted, svc.calls)
	}
	if !strings.Contains(string(svc.input.TemplateSnapshot), id) || !strings.Contains(string(svc.input.TemplateSnapshot), `"revision":3`) || len(svc.input.Options.ImageURLs) != 1 || len(svc.input.Options.VideoURLs) != 1 {
		t.Fatal("recipe assembly changed", svc.input)
	}
	// Published Save is validated before the real repository changes anything.
	beforeRecord, _ := json.Marshal(persisted)
	invalid := d
	invalid.CoverID = ""
	rejected = call("PUT", "/v1/system/templates/"+id, map[string]any{"version": persisted.Version, "content": invalid}, "alice", "")
	if rejected.code < 400 {
		t.Fatal("missing cover accepted on published Save", rejected)
	}
	unchanged, e := repo.Get(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	afterRecord, _ := json.Marshal(unchanged)
	if !bytes.Equal(beforeRecord, afterRecord) {
		t.Fatal("failed published Save mutated persisted content")
	}
	acceptedSnapshot := append([]byte(nil), svc.input.TemplateSnapshot...)
	d.Prompt = "Image 1 now follows Video 1"
	updated := call("PUT", "/v1/system/templates/"+id, map[string]any{"version": persisted.Version, "content": d}, "alice", "")
	if updated.code != 200 {
		t.Fatal(updated)
	}
	current, e := repo.Get(context.Background(), id)
	if e != nil || current.Revision <= persisted.Revision || current.Draft.Prompt != d.Prompt || current.Published == nil || current.Published.Prompt != d.Prompt || current.Draft.CoverID != asset.ID || current.Draft.Slots[1].AssetID != asset.ID {
		t.Fatal("published Save not current", current, e)
	}
	for _, endpoint := range []string{"generations", "estimate", "validate-media", "editing"} {
		stale := call("POST", "/v1/templates/"+id+"/"+endpoint, request, "", consumerKey)
		if stale.code != 409 {
			t.Fatal("stale persisted content request accepted", endpoint, stale)
		}
	}
	if !bytes.Equal(acceptedSnapshot, svc.input.TemplateSnapshot) || svc.calls != 1 {
		t.Fatal("stale requests changed an admitted snapshot")
	}
	request["revision"] = current.Revision
	accepted = call("POST", "/v1/templates/"+id+"/generations", request, "", consumerKey)
	if accepted.code != 202 || svc.calls != 2 || svc.input.Prompt != d.Prompt {
		t.Fatal("new admission used old published copy", accepted, svc.calls)
	}
	// Shared-source quota is enforced before bytes are received/persisted.
	limit := int64(1)
	s.storageAdmin = &quotaStorage{usage: storage.Usage{LimitBytes: &limit, UsedBytes: 2}}
	before = count("reference_images")
	upload = validationUpload(t, h, "/v1/system/template-references", "", "alice", "quota.png", testmedia.PNG512("quota"), nil)
	if upload.Code != 403 || count("reference_images") != before {
		t.Fatal("shared quota bypass", upload.Code)
	}
}

type httpResult struct {
	code int
	body string
}

func TestSharedSourceUploadUsesCurrentFilteredModelCatalog(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	s.modelAccess = &modelAccessMemory{models: []modelaccess.Model{{ID: uuid.NewString(), Ref: modelaccess.Ref{Provider: "mock", Model: "dance"}, EnabledByDefault: false}}, org: map[string]map[string]bool{}}
	h := s.handler()
	target := &referencevalidation.Target{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references"}}
	data := testmedia.MP4(5000, 1000, 150)
	w := validationUpload(t, h, "/v1/system/template-references", "", "alice", "model.mp4", data, target)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unavailable") {
		t.Fatal("shared-source upload bypassed model policy", w.Code, w.Body.String())
	}
	items, err := s.templateReferences.List(templateLibraryContext(context.Background()), 10, 0)
	if err != nil || len(items) != 0 {
		t.Fatal("unauthorized-model upload retained", items, err)
	}
	w = validationUpload(t, h, "/v1/system/template-references", "", "alice", "generic.mp4", data, nil)
	if w.Code != 201 {
		t.Fatal("generic source acquired a model requirement", w.Code, w.Body.String())
	}
}
