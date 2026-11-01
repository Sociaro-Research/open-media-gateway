package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

type templateLibraryAuth struct{ browserAuthStub }

func (a templateLibraryAuth) Authenticate(r *http.Request) (identity.User, error) {
	if cookie, e := r.Cookie("test_session"); e == nil && cookie.Value == "carol" {
		return identity.User{ID: "carol"}, nil
	}
	return a.browserAuthStub.Authenticate(r)
}

func TestTemplateLibraryCreatorScopeAndConsumerBoundaries(t *testing.T) {
	s, repo, _, store := templateFixture(t)
	referencesRepo := &referenceMemory{items: map[string]references.Image{}}
	s.references = references.New(referencesRepo, store)
	s.system.(*systemFixture).admins["carol"] = true
	s.auth = templateLibraryAuth{}
	h := s.handler()
	upload := func(actor string, data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		multi := multipart.NewWriter(&body)
		f, _ := multi.CreateFormFile("file", "face.png")
		f.Write(data)
		multi.Close()
		r := httptest.NewRequest("POST", "/v1/system/template-references", &body)
		r.Header.Set("Content-Type", multi.FormDataContentType())
		r.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{"POST", "DELETE", "PATCH"} {
		path := "/v1/system/template-references"
		if method == "DELETE" || method == "PATCH" {
			path += "/" + uuid.NewString()
		}
		w := sendTest(h, method, path, `{}`, "bob", "")
		if w.Code != 403 {
			t.Fatalf("consumer %s: %d %s", method, w.Code, w.Body.String())
		}
	}
	// Private content is not automatically promoted to the creator collection.
	privateCtx := identity.WithPersonalData(identity.WithOwnerID(context.Background(), "alice"))
	private, e := s.references.Upload(privateCtx, "private.png", bytes.NewReader(referencePNG(t)))
	if e != nil {
		t.Fatal(e)
	}
	w := sendTest(h, "GET", "/v1/system/template-references", "", "alice", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), private.ID) {
		t.Fatalf("private isolation: %d %s", w.Code, w.Body.String())
	}
	w = upload("alice", referencePNG(t))
	if w.Code != 201 {
		t.Fatalf("shared upload: %d %s", w.Code, w.Body.String())
	}
	var source references.Image
	if e = json.Unmarshal(w.Body.Bytes(), &source); e != nil {
		t.Fatal(e)
	}
	if source.ID == private.ID {
		t.Fatal("private object promoted")
	}
	w = sendTest(h, "GET", "/v1/system/template-references", "", "carol", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), source.ID) || strings.Contains(w.Body.String(), private.ID) {
		t.Fatalf("second creator: %d %s", w.Code, w.Body.String())
	}
	for _, actor := range []string{"alice", "carol", "bob"} {
		w = sendTest(h, "GET", "/v1/system/template-references/"+source.ID+"/content", "", actor, "")
		want := 200

		if w.Code != want {
			t.Fatalf("source read %s: %d", actor, w.Code)
		}
	}
	w = sendTest(h, "GET", "/v1/references", "", "bob", "")
	if strings.Contains(w.Body.String(), source.ID) {
		t.Fatal("consumer can list shared source")
	}
	w = upload("alice", []byte("invalid image"))
	if w.Code != 400 {
		t.Fatalf("malformed: %d", w.Code)
	}
	id := uuid.NewString()
	repo.items[id] = templates.Record{ID: id, Version: 1}
	w = sendTest(h, "POST", "/v1/system/templates/"+id+"/assets", `{"library_reference_id":"`+private.ID+`"}`, "carol", "")
	if w.Code != 404 {
		t.Fatalf("private source forged as shared: %d %s", w.Code, w.Body.String())
	}

	storedSource := referencesRepo.items[templates.LibraryOwnerID+"/"+source.ID]
	tampered := storedSource
	tampered.SHA256 = "incorrect"
	referencesRepo.items[templates.LibraryOwnerID+"/"+source.ID] = tampered
	badCopy := sendTest(h, "POST", "/v1/system/templates/"+id+"/assets", `{"library_reference_id":"`+source.ID+`"}`, "carol", "")
	if badCopy.Code != 400 || !strings.Contains(badCopy.Body.String(), "checksum") {
		t.Fatal("changed source accepted", badCopy.Code, badCopy.Body.String())
	}
	referencesRepo.items[templates.LibraryOwnerID+"/"+source.ID] = storedSource
	w = sendTest(h, "POST", "/v1/system/templates/"+id+"/assets", `{"library_reference_id":"`+source.ID+`"}`, "carol", "")
	if w.Code != 201 {
		t.Fatalf("shared binding: %d %s", w.Code, w.Body.String())
	}
	var a templates.Asset
	if e = json.Unmarshal(w.Body.Bytes(), &a); e != nil {
		t.Fatal(e)
	}
	if a.ID != source.ID {
		t.Fatal("binding must preserve shared asset identity")
	}
	// Uploading a source must never itself assign a recipe slot.
	if len(repo.items[id].Draft.Slots) != 0 {
		t.Fatal("upload assigned a slot")
	}
}

func TestTemplateLibraryBearerScopesAndQuota(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	h := s.handler()
	ctx := templateLibraryContext(identity.WithOwnerID(context.Background(), "alice"))
	source, e := s.templateReferences.Upload(ctx, "scope.png", bytes.NewReader(referencePNG(t)))
	if e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	repo.items[id] = templates.Record{ID: id, Version: 1}
	asset := templateCopy(t, s, id, referencePNG(t))
	_, generation := issueTestKey(t, h, "alice", `{"name":"generation"}`)
	_, manage := issueTestKey(t, h, "alice", `{"name":"creator","scopes":["generation","system:manage"]}`)
	for _, path := range []string{"/v1/system/template-references", "/v1/system/template-references/" + source.ID + "/content", "/v1/system/templates/" + id + "/assets/" + asset.ID + "/metadata"} {
		for _, method := range []string{"GET", "HEAD"} {
			for _, tc := range []struct {
				key  string
				want int
			}{{generation, 403}, {manage, 200}} {
				w := sendTest(h, method, path, "", "", tc.key)
				want := tc.want
				if !strings.Contains(path, "/assets/") {
					want = 200
				}
				if w.Code != want {
					t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
				}
				if method == "HEAD" && w.Body.Len() != 0 {
					t.Fatal("HEAD leaked body")
				}
			}
		}
	}
	for _, method := range []string{"POST", "DELETE"} {
		path := "/v1/system/template-references"
		want := 400
		if method == "DELETE" {
			path += "/" + uuid.NewString()
			want = 404
		}
		if w := sendTest(h, method, path, `{}`, "", generation); w.Code != 403 {
			t.Fatal("generation-only source mutation", method, w.Code)
		}
		if w := sendTest(h, method, path, `{}`, "", manage); w.Code != want {
			t.Fatal("creator source mutation", method, w.Code, w.Body.String())
		}
	}
	// A management scope cannot survive loss of the actual creator role.
	s.system.(*systemFixture).admins["alice"] = false
	if w := sendTest(h, "POST", "/v1/system/template-references", `{}`, "", manage); w.Code != 403 {
		t.Fatal("revoked creator retained access", w.Code)
	}
	s.system.(*systemFixture).admins["alice"] = true
	limit := int64(1)
	quota := &quotaStorage{usage: storage.Usage{LimitBytes: &limit, UsedBytes: 2}}
	s.storageAdmin = quota
	w := sendTest(h, "POST", "/v1/system/template-references", `{}`, "", manage)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "storage_quota_exceeded") {
		t.Fatal("shared source bypassed quota", w.Code, w.Body.String())
	}
	// Reads/deletes remain authorized over quota; read-only credentials never write.
	if w = sendTest(h, "GET", "/v1/system/template-references", "", "", manage); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = sendTest(h, "POST", "/v1/system/template-references", `{}`, "", generation); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
