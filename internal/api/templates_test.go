package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

type templateMemory struct {
	items  map[string]templates.Record
	assets map[string]templates.Asset
	saves  int
}

func (m *templateMemory) List(_ context.Context, admin bool) ([]templates.Record, error) {
	a := []templates.Record{}
	for _, v := range m.items {
		if admin || v.Published != nil {
			a = append(a, v)
		}
	}
	return a, nil
}
func (m *templateMemory) Get(_ context.Context, id string) (templates.Record, error) {
	v, ok := m.items[id]
	if !ok {
		return v, templates.ErrNotFound
	}
	return v, nil
}
func (m *templateMemory) Save(_ context.Context, v templates.Record, version int64, action string) (templates.Record, error) {
	old, exists := m.items[v.ID]
	if exists && old.Version != version || !exists && version != 0 {
		return v, templates.ErrConflict
	}
	v.Version = version + 1
	v.Revision = old.Revision
	v.Published = old.Published
	if action == "publish" {
		d := v.Draft
		v.Published = &d
		v.Revision = v.Version
		for k, a := range m.assets {
			if a.ID == d.CoverID || slices.ContainsFunc(d.Slots, func(slot templates.Slot) bool { return slot.AssetID == a.ID }) {
				a.Published = true
				m.assets[k] = a
			}
		}
	}
	if action == "unpublish" {
		v.Published = nil
	}
	m.saves++
	m.items[v.ID] = v
	return v, nil
}
func (m *templateMemory) Asset(_ context.Context, id string) (templates.Asset, error) {
	a, ok := m.assets[id]
	if !ok {
		return a, templates.ErrNotFound
	}
	return a, nil
}
func (m *templateMemory) PutAsset(_ context.Context, a templates.Asset) error {
	m.assets[a.ID] = a
	return nil
}
func (m *templateMemory) WithPublished(ctx context.Context, id string, revision int64, fn func(templates.Record) error) error {
	v, e := m.Get(ctx, id)
	if e != nil {
		return e
	}
	if v.Published == nil {
		return templates.ErrNotFound
	}
	if v.Revision != revision {
		return templates.ErrConflict
	}
	return fn(v)
}
func templateTestModel() core.MediaModel {
	return core.MediaModel{ID: "dance", MediaType: "video", ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 12, Image: core.MediaFileLimits{ContentTypes: []string{"image/png"}, MaxBytes: 1 << 20, MinDimension: 1}, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4"}, MaxBytes: 1 << 20, MinDuration: 2, MaxDuration: 12, MinFPS: 24, MaxFPS: 60}}, ReferencePromptSyntax: "numbered", ReferencePromptTypes: []string{"image", "video"}, SupportsImageInput: true, SupportsVideoInput: true, MaxImageInputs: 9, ReferenceModes: []core.ReferenceMode{{ID: "references", MaxImages: 9, MaxVideos: 1}}, Resolutions: []string{"720p"}, Ratios: []string{"16:9"}, MinDuration: 4, MaxDuration: 12, DefaultDuration: 5}
}
func templateFixture(t *testing.T) (*Server, *templateMemory, *openAIServiceFixture, *storage.Local) {
	t.Helper()
	s, _ := newPersonalTestServer()
	repo := &templateMemory{items: map[string]templates.Record{}, assets: map[string]templates.Asset{}}
	svc := openAIFixture()
	svc.catalog = []core.ProviderDescriptor{{ID: "mock", Models: []core.MediaModel{templateTestModel()}}}
	s.service = svc
	s.templates = repo
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	store, e := storage.NewLocal(t.TempDir(), http.DefaultClient, 300<<20)
	if e != nil {
		t.Fatal(e)
	}
	s.storage = store
	s.mediaSigner = newMediaURLSigner("fixture-secret", time.Hour)
	s.assetPublicURL = "https://gateway.example.test"
	s.references = references.New(&referenceMemory{items: map[string]references.Image{}}, store)
	return s, repo, svc, store
}
func templateVideo() []byte {
	atom := func(k string, d []byte) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.BigEndian, uint32(len(d)+8))
		b.WriteString(k)
		b.Write(d)
		return b.Bytes()
	}
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[76:], 640<<16)
	binary.BigEndian.PutUint32(tkhd[80:], 480<<16)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], 30000)
	binary.BigEndian.PutUint32(mdhd[16:], 150000)
	hdlr := make([]byte, 12)
	copy(hdlr[8:], "vide")
	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:], 1)
	binary.BigEndian.PutUint32(stts[8:], 150)
	binary.BigEndian.PutUint32(stts[12:], 1000)
	mdia := append(atom("mdhd", mdhd), atom("hdlr", hdlr)...)
	mdia = append(mdia, atom("minf", atom("stbl", atom("stts", stts)))...)
	return append(atom("ftyp", []byte("isom0000isom")), atom("moov", atom("trak", append(atom("tkhd", tkhd), atom("mdia", mdia)...)))...)
}
func templateCopy(t *testing.T, s *Server, id string, data []byte) templates.Asset {
	t.Helper()
	var body bytes.Buffer
	multi := multipart.NewWriter(&body)
	file, _ := multi.CreateFormFile("file", "fixture")
	_, _ = file.Write(data)
	_ = multi.Close()
	r := httptest.NewRequest("POST", "/v1/system/templates/"+id+"/assets", &body)
	r.Header.Set("Content-Type", multi.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("copy: %d %s", w.Code, w.Body.String())
	}
	var a templates.Asset
	if e := json.Unmarshal(w.Body.Bytes(), &a); e != nil {
		t.Fatal(e)
	}
	return a
}
func TestTemplatesAuthorUsePermissionsAndExactInputs(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	h := s.handler()
	id := uuid.NewString()
	record := templates.Record{ID: id, Version: 1, Draft: templates.Definition{Title: "Dance", MediaType: "video", Provider: "mock", Model: "dance", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references"}}}
	record.Draft.Slots, _ = templates.Infer(record.Draft.Prompt, "numbered", []string{"image", "video"}, templateTestModel().ReferenceModes[0])
	repo.items[id] = record
	for _, actor := range []string{"bob"} {
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE"} {
			path := "/v1/system/templates/" + id
			if method == "POST" {
				path += "/infer"
			}
			w := sendTest(h, method, path, `{}`, actor, "")
			if w.Code != 403 {
				t.Fatalf("%s %s %s: %d", actor, method, path, w.Code)
			}
		}
	}
	if repo.saves != 0 {
		t.Fatal("unauthorized mutation")
	}
	video := templateCopy(t, s, id, templateVideo())
	cover := templateCopy(t, s, id, referencePNG(t))
	record.Draft.Slots[2].AssetID = video.ID
	record.Draft.CoverID = cover.ID
	repo.items[id] = record
	body, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": record.Draft})
	w := sendTest(h, "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	record = repo.items[id]
	if record.Revision != 2 || record.Published == nil {
		t.Fatal(record)
	}
	// Service key is authorized to browse published metadata but not draft management.
	w = sendTest(h, "GET", "/v1/templates", "", "", "service-secret")
	if w.Code != 200 || strings.Contains(w.Body.String(), "Draft") || !strings.Contains(w.Body.String(), "Dance") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = sendTest(h, "GET", "/v1/templates/"+id, "", "", "service-secret")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"default_available":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	aliceCtx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
	ref, e := s.references.Upload(aliceCtx, "face.png", bytes.NewReader(referencePNG(t)))
	if e != nil {
		t.Fatal(e)
	}
	sharedSource, e := s.references.Upload(templateLibraryContext(context.Background()), "shared.png", bytes.NewReader(referencePNG(t)))
	if e != nil {
		t.Fatal(e)
	}
	_, key := issueTestKey(t, h, "alice", `{"name":"Templates"}`)
	request := map[string]any{"revision": 2, "provider": "mock", "model": "dance", "inputs": map[string]string{"image:1": ref.ID, "image:2": ref.ID}, "max_expected_cost_usd": 0}
	send := func(value any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(value)
		r := httptest.NewRequest("POST", "/v1/templates/"+id+"/generations", bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Idempotency-Key", "same-confirmation")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	w = send(request)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if svc.calls != 1 || svc.input.Prompt != record.Draft.Prompt || len(svc.input.Options.ImageURLs) != 2 || len(svc.input.Options.VideoURLs) != 1 || strings.Contains(string(svc.input.TemplateSnapshot), cover.ID) || !strings.Contains(string(svc.input.TemplateSnapshot), `"revision":2`) {
		t.Fatal(svc.input, svc.calls)
	}
	if !strings.Contains(svc.input.Options.VideoURLs[0], video.ID) || svc.input.Options.ImageURLs[0] != svc.input.Options.ImageURLs[1] {
		t.Fatal("slot order or repeated asset mapping changed", svc.input.Options)
	}
	for _, extra := range []string{"prompt", "options", "fixed_inputs", "asset_id", "slots", "reference_counts", "owner_id"} {
		bad := map[string]any{}
		for k, v := range request {
			bad[k] = v
		}
		bad[extra] = "injection"
		if out := send(bad); out.Code != 400 {
			t.Fatal(extra, out.Code, out.Body.String())
		}
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"fixed override", func(v map[string]any) {
			v["inputs"] = map[string]string{"image:1": ref.ID, "image:2": ref.ID, "video:1": video.ID}
		}},
		{"extra slot", func(v map[string]any) {
			v["inputs"] = map[string]string{"image:1": ref.ID, "image:2": ref.ID, "image:3": ref.ID}
		}},
		{"missing", func(v map[string]any) { v["inputs"] = map[string]string{"image:1": ref.ID} }},
		{"provider asset injection", func(v map[string]any) {
			v["inputs"] = map[string]string{"image:1": "asset://private", "image:2": ref.ID}
		}},
		{"model changed", func(v map[string]any) { v["model"] = "unavailable" }},
		{"revision changed", func(v map[string]any) { v["revision"] = 1 }},
	} {
		bad := map[string]any{}
		for k, v := range request {
			bad[k] = v
		}
		tc.change(bad)
		out := send(bad)
		if out.Code != 400 && out.Code != 409 {
			t.Fatal(tc.name, out.Code, out.Body.String())
		}
	}
	positive := map[string]any{}
	for k, v := range request {
		positive[k] = v
	}
	positive["inputs"] = map[string]string{"image:1": sharedSource.ID, "image:2": ref.ID}
	payload, _ := json.Marshal(positive)
	if out := sendTest(h, "POST", "/v1/templates/"+id+"/validate-media", string(payload), "", key); out.Code != 200 {
		t.Fatal("shared input rejected", out.Code, out.Body.String())
	}
	if svc.calls != 1 {
		t.Fatal("invalid attempts reached admission", svc.calls)
	}
	// Losing the default or key access never silently selects an alternative.
	svc.catalog[0].Models[0].ID = "alternate"
	w = sendTest(h, "GET", "/v1/templates/"+id, "", "", key)
	if !strings.Contains(w.Body.String(), `"default_available":false`) {
		t.Fatal(w.Body.String())
	}
	svc.catalog[0].Models = nil
	w = sendTest(h, "GET", "/v1/templates/"+id, "", "", key)
	if !strings.Contains(w.Body.String(), `"catalog":[]`) {
		t.Fatal(w.Body.String())
	}
	svc.catalog[0].Models = []core.MediaModel{templateTestModel()}
	// Concurrent edits retain the old published revision; stale writes fail.
	edit := record
	edit.Draft.Title = "New draft"
	repo.items[id] = edit
	w = sendTest(h, "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Copy remains after deleting original customer input. Never delete originals on unpublish.
	if e = s.references.Delete(aliceCtx, ref.ID); e != nil {
		t.Fatal(e)
	}
	w = send(request)
	if w.Code != 400 {
		t.Fatal("missing customer input admitted", w.Code)
	}
	current := repo.items[id]
	current.Published = nil
	repo.items[id] = current
	w = send(request)
	if w.Code != 404 {
		t.Fatal("unpublished template admitted", w.Code)
	}
	media := sendTest(h, "GET", s.templateAssetURL(video.ID), "", "", "")
	if media.Code != 200 {
		t.Fatal("published copy lost after unpublish", media.Code)
	}
}
func TestTemplateDraftFixedAssetsCoverAndUnavailableBytes(t *testing.T) {
	s, repo, _, store := templateFixture(t)
	id := uuid.NewString()
	d := templates.Definition{Title: "Plain", Prompt: "A landscape", MediaType: "video", Provider: "mock", Model: "dance", Syntax: "numbered", Slots: []templates.Slot{}, Options: core.MediaOptions{ReferenceMode: "references"}}
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	cover := templateCopy(t, s, id, referencePNG(t))
	d.CoverID = cover.ID
	if e := s.checkTemplateAssets(context.Background(), id, d); e != nil {
		t.Fatal(e)
	}
	foreign := repo.assets[cover.ID]
	foreign.ID = cover.ID
	foreign.TemplateID = uuid.NewString()
	repo.assets[foreign.ID] = foreign
	d.CoverID = foreign.ID
	if e := s.checkTemplateAssets(context.Background(), id, d); e != nil {
		t.Fatal("shared asset could not be reused", e)
	}
	d.CoverID = cover.ID
	asset := repo.assets[cover.ID]
	if e := store.DeleteReference(templateLibraryContext(context.Background()), asset.StorageKey); e != nil {
		t.Fatal(e)
	}
	if e := s.checkTemplateAssets(context.Background(), id, d); !errors.Is(e, templates.ErrNotFound) {
		t.Fatal(e)
	}
	for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
		before := repo.saves
		w := sendTest(s.handler(), method, "/v1/system/templates/"+id, "", "alice", "")
		if method == "OPTIONS" && w.Code != 405 {
			t.Fatal(w.Code)
		}
		if repo.saves != before {
			t.Fatal("read method mutated")
		}
	}
}

func TestTemplateStorageNamespaceAndCompanyRole(t *testing.T) {
	ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
	ctx = identity.WithOrganizationRole(ctx, uuid.NewString(), "admin")
	storageCtx := templateStorageContext(ctx)
	if identity.OwnerID(storageCtx) != identity.SharedOwnerID || identity.StorageOwnerID(storageCtx) != identity.SharedOwnerID || identity.ActorID(storageCtx) != "alice" {
		t.Fatal("template copy inherited private owner or lost actor", identity.OwnerID(storageCtx), identity.StorageOwnerID(storageCtx), identity.ActorID(storageCtx))
	}
	s, repo, _, _ := templateFixture(t)
	r := httptest.NewRequest("POST", "/v1/system/templates", strings.NewReader(`{"draft":{},"version":0}`))
	r.AddCookie(&http.Cookie{Name: "test_session", Value: "bob"})
	r = r.WithContext(identity.WithOrganizationRole(r.Context(), uuid.NewString(), "admin"))
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != 403 || repo.saves != 0 {
		t.Fatal("company administrator received publishing rights", w.Code, w.Body.String())
	}
}

type templateFreeProvider struct{ *mock.Adapter }

func (*templateFreeProvider) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{templateTestModel()}, nil
}
func TestTemplatesReuseCoreEstimatesIdempotencyAndPrivateOwnership(t *testing.T) {
	for _, fixedCount := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(fixedCount), func(t *testing.T) {
			s, repo, _, _ := templateFixture(t)
			admission := &admissionRepository{}
			queue := &admissionQueue{}
			service := core.NewTaskServiceWithProviders(admission, queue, map[string]core.MediaProvider{"mock": &templateFreeProvider{mock.New(time.Second, "")}}, map[string]string{"mock": "dance"}, "mock")
			service.EnableAccounting()
			s.service = service
			id := uuid.NewString()
			d := templates.Definition{Title: "Dance", MediaType: "video", Provider: "mock", Model: "dance", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references"}}
			d.Slots, _ = templates.Infer(d.Prompt, d.Syntax, []string{"image", "video"}, templateTestModel().ReferenceModes[0])
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			cover := templateCopy(t, s, id, referencePNG(t))
			d.CoverID = cover.ID
			ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
			inputs := map[string]string{}
			for i := range d.Slots {
				data := referencePNG(t)
				if d.Slots[i].Type == "video" {
					data = templateVideo()
				}
				if i < fixedCount {
					a := templateCopy(t, s, id, data)
					d.Slots[i].AssetID = a.ID
				} else {
					ref, e := s.references.Upload(ctx, d.Slots[i].ID, bytes.NewReader(data))
					if e != nil {
						t.Fatal(e)
					}
					inputs[d.Slots[i].ID] = ref.ID
				}
			}
			repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: d, Published: &d}
			for k, a := range repo.assets {
				a.Published = true
				repo.assets[k] = a
			}
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"Templates"}`)
			body, _ := json.Marshal(map[string]any{"revision": 2, "provider": "mock", "model": "dance", "inputs": inputs, "max_expected_cost_usd": 0})
			request := func(path, secret string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", path, bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+secret)
				r.Header.Set("Idempotency-Key", "one-confirmation")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := request("/v1/templates/"+id+"/estimate", key); w.Code != 200 || admission.creates != 0 {
				t.Fatal("estimate created a job", w.Code, w.Body.String())
			}
			for i := 0; i < 3; i++ {
				if w := request("/v1/templates/"+id+"/generations", key); w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			task := admission.task
			if admission.creates != 1 || len(slices.Compact(slices.Clone(queue.ids))) != 1 || task.OwnerID != identity.OwnerID(ctx) || task.ActorID != "alice" || task.Cost == nil || task.Cost.ExpectedUSD != 0 || len(task.Options.ImageURLs) != 2 || len(task.Options.Videos()) != 1 || (cover.ID != d.Slots[0].AssetID && strings.Contains(string(task.TemplateSnapshot), cover.ID)) {
				t.Fatal("template bypassed normal admission/billing or mixed cover/input ownership", task, admission.creates, queue.ids)
			}
			_, bobKey := issueTestKey(t, h, "bob", `{"name":"Other owner"}`)
			if fixedCount < 3 {
				if w := request("/v1/templates/"+id+"/generations", bobKey); w.Code != 400 {
					t.Fatal("foreign input accessed", w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestTemplateCatalogIncludesAllMatchingConnectionsRegardlessOfNames(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	id := uuid.NewString()
	native := templateTestModel()
	d := templates.Definition{Title: "Dance", MediaType: "video", Provider: "mock", Model: "dance", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references"}}
	d.Slots, _ = templates.Infer(d.Prompt, d.Syntax, native.ReferencePromptTypes, native.ReferenceModes[0])
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	cover := templateCopy(t, s, id, referencePNG(t))
	d.CoverID = cover.ID
	repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: d, Published: &d}
	alias := native
	alias.ID = "unrelated-native-alias"
	alias.DisplayName = "Operator label"
	missing := native
	missing.ID = "missing-file-metadata"
	missing.ReferenceMediaLimits = nil
	svc.catalog = []core.ProviderDescriptor{{ID: "mock", Models: []core.MediaModel{native}}, {ID: "arbitrary-connection", Name: "Unrelated provider label", Models: []core.MediaModel{alias, missing}}}
	response := sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", "service-secret")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var view struct {
		Catalog []core.ProviderDescriptor `json:"catalog"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, provider := range view.Catalog {
		for _, model := range provider.Models {
			keys = append(keys, provider.ID+"/"+model.ID)
		}
	}
	if !slices.Contains(keys, "mock/dance") || !slices.Contains(keys, "arbitrary-connection/unrelated-native-alias") || !slices.Contains(keys, "arbitrary-connection/missing-file-metadata") {
		t.Fatal("catalog used names instead of complete contracts", keys)
	}
}

func TestNativeOrderedImageTemplateInferPublishAndSubmit(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	model := core.MediaModel{ID: "native-image", MediaType: "image", SupportsImageInput: true, MaxImageInputs: 2, ReferenceModes: []core.ReferenceMode{{ID: "references", MaxImages: 2}}, Resolutions: []string{"1024x1024"}}
	svc.catalog = []core.ProviderDescriptor{{ID: "mock", Models: []core.MediaModel{model}}}
	id := uuid.NewString()
	d := templates.Definition{Title: "Native ordered images", MediaType: "image", Provider: "mock", Model: model.ID, Prompt: "Combine the two supplied images without changing their faces.", ReferenceCounts: map[string]int{"image": 2}, Options: core.MediaOptions{ReferenceMode: "references"}}
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	body, _ := json.Marshal(d)
	w := sendTest(s.handler(), "POST", "/v1/system/templates/"+id+"/infer", string(body), "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var inferred struct {
		Slots  []templates.Slot `json:"slots"`
		Syntax string           `json:"syntax"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &inferred); err != nil {
		t.Fatal(err)
	}
	if len(inferred.Slots) != 2 || inferred.Syntax != "" {
		t.Fatal(inferred)
	}
	d.Slots = inferred.Slots
	fixed := templateCopy(t, s, id, referencePNG(t))
	d.Slots[0].AssetID = fixed.ID
	d.CoverID = fixed.ID
	body, _ = json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
	w = sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
	ref, err := s.references.Upload(ctx, "input.png", bytes.NewReader(referencePNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Native inputs"}`)
	body, _ = json.Marshal(map[string]any{"revision": 2, "provider": "mock", "model": model.ID, "inputs": map[string]string{"image:2": ref.ID}, "max_expected_cost_usd": 0})
	send := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/templates/"+id+"/generations", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Idempotency-Key", uuid.NewString())
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w
	}
	w = send()
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if svc.input.Prompt != d.Prompt || len(svc.input.Options.ImageURLs) != 2 || !strings.Contains(svc.input.Options.ImageURLs[0], fixed.ID) {
		t.Fatal(svc.input)
	}
	// Even without optional provider file metadata, actual bytes are inspected.
	asset := repo.assets[fixed.ID]
	asset.SHA256 = strings.Repeat("0", 64)
	repo.assets[fixed.ID] = asset
	w = send()
	if w.Code == 202 || svc.calls != 1 {
		t.Fatal("corrupt fixed bytes bypassed baseline validation", w.Code, svc.calls)
	}
}

func TestLegacyZeroReferenceRecipesRemainStoredButUnavailable(t *testing.T) {
	for _, output := range []string{"image", "video"} {
		t.Run(output, func(t *testing.T) {
			s, repo, svc, _ := templateFixture(t)
			id := uuid.NewString()
			d := templates.Definition{Title: "Landscape", MediaType: output, Provider: "source", Model: "plain", Prompt: "A calm landscape", Syntax: "numbered", Slots: []templates.Slot{}, Options: core.MediaOptions{ReferenceMode: "none"}}
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			cover := templateCopy(t, s, id, referencePNG(t))
			d.CoverID = cover.ID
			repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: d, Published: &d}
			plain := core.MediaModel{ID: "plain", MediaType: output, ReferenceModes: []core.ReferenceMode{{ID: "none"}}}
			optional := plain
			optional.ID = "optional-input-profile"
			optional.ReferencePromptSyntax = "bracket"
			optional.SupportsImageInput = true
			optional.ReferenceModes = []core.ReferenceMode{{ID: "references", MaxImages: 3}}
			required := optional
			required.ID = "requires-input"
			required.RequiresImageInput = true
			svc.catalog = []core.ProviderDescriptor{{ID: "source", Models: []core.MediaModel{plain}}, {ID: "another-provider", Models: []core.MediaModel{optional, required}}}
			response := sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", "service-secret")
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body.String())
			}
			var view struct {
				Catalog []core.ProviderDescriptor `json:"catalog"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, p := range view.Catalog {
				count += len(p.Models)
			}
			if count != 0 || !strings.Contains(response.Body.String(), `"workflow_error":"invalid template:`) {
				t.Fatal("legacy empty recipe remained available", response.Body.String())
			}
			body := `{"revision":2,"provider":"another-provider","model":"optional-input-profile","inputs":{},"max_expected_cost_usd":0}`
			request := httptest.NewRequest("POST", "/v1/templates/"+id+"/generations", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer service-secret")
			request.Header.Set("Idempotency-Key", "zero-reference")
			w := httptest.NewRecorder()
			s.handler().ServeHTTP(w, request)
			if w.Code != 400 || svc.calls != 0 || repo.items[id].Published.Options.ReferenceMode != "none" || repo.saves != 0 {
				t.Fatal("legacy recipe changed or admitted", w.Code, w.Body.String())
			}
		})
	}
}
