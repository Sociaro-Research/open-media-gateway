package companyinterface_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

func TestCompanyMediaValidForeignCompanySignatureCannotReleaseBytes(t *testing.T) {
	f, store, _, _ := mediaHTTPFixture(t)
	v := f.current(t)
	ctx := context.Background()
	sys := postgres.NewSystemRepository(f.centralDB)
	if e := sys.SetAdmin(ctx, "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	foreign, e := sys.CreateOrganization(ctx, v.Tokens.Actor, "Foreign media company", v.Tokens.Actor, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	key, secret, e := apikey.Generate(apikey.CreateInput{Name: "foreign media", UserID: v.Tokens.Actor, OrganizationID: foreign.ID, ResourceMode: "shared", StorageMode: "shared"}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = postgres.NewAPIKeyRepository(f.centralDB).Create(ctx, v.Tokens.Actor, key, apikey.Hash(secret)); e != nil {
		t.Fatal(e)
	}
	// Disposable fixture allowances only, matching the ordinary central admission prerequisites.
	if _, e = f.centralDB.Exec(`UPDATE organizations SET credit_limit_micros=1000000 WHERE id=$1`, foreign.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET weekly_limit_micros=NULL WHERE organization_id=$1 AND user_id=$2`, foreign.ID, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	admitted := resourceDirect(f, "POST", "/v1/generations", `{"model":"mock/workflow-image","prompt":"foreign completed object","max_expected_cost_usd":0}`, secret, "")
	expectFixture(t, admitted, 202)
	var view struct{ ID string }
	if json.Unmarshal(admitted.Body.Bytes(), &view) != nil || view.ID == "" {
		t.Fatal("foreign task missing")
	}
	repo := postgres.NewTaskRepository(f.centralDB)
	worker := core.WithClaimToken(ctx, uuid.NewString())
	if ok, e := repo.AcquireTask(worker, view.ID, time.Minute); e != nil || !ok {
		t.Fatal("claim", e)
	}
	if e = repo.MarkRunning(worker, view.ID); e != nil {
		t.Fatal(e)
	}
	foreignContext := identity.WithOrganizationRole(identity.WithUser(ctx, identity.User{ID: v.Tokens.Actor}), foreign.ID, "supermanager")
	data := testmedia.PNG512("foreign company bytes must never reach secondary")
	stored, e := store.Archive(foreignContext, view.ID, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data))
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.MarkSucceeded(worker, view.ID, stored, ""); e != nil {
		t.Fatal(e)
	}
	signed := resourceDirect(f, "GET", "/v1/generations/"+view.ID+"/media-access", "", secret, "")
	expectFixture(t, signed, 200)
	var links struct {
		Preview  string `json:"preview_url"`
		Download string `json:"download_url"`
	}
	if json.Unmarshal(signed.Body.Bytes(), &links) != nil || !strings.HasPrefix(links.Preview, "/media/"+view.ID+"?") {
		t.Fatal("central signature not issued")
	}
	// Positive direct capability control proves the URL and completed foreign bytes
	// are real; rejection below cannot pass merely because the signature is invalid.
	for _, path := range []string{links.Preview, links.Download} {
		direct := httptest.NewRecorder()
		f.central.ServeHTTP(direct, httptest.NewRequest("GET", fixtureCentral+path, nil))
		expectFixture(t, direct, 200)
		if !bytes.Equal(direct.Body.Bytes(), data) {
			t.Fatal("foreign capability control failed")
		}
		for _, browser := range []bool{true, false} {
			for _, method := range []string{"GET", "HEAD"} {
				denied := facadeRequest(t, f, method, path, "", v.Tokens.PlaygroundKey, browser, nil)
				expectFixture(t, denied, 404)
				if bytes.Contains(denied.Body.Bytes(), data) || denied.Header().Get("Location") != "" || method == "HEAD" && denied.Body.Len() != 0 {
					t.Fatal("foreign signed media released")
				}
			}
		}
	}
}

func TestCompanyMediaValidNeverPublishedTemplateSignatureIsDenied(t *testing.T) {
	var service api.Service
	f, store, library, _ := mediaHTTPFixture(t, func(d *api.Dependencies) { service = d.Service })
	v := f.current(t)
	ctx := context.Background()
	sys := postgres.NewSystemRepository(f.centralDB)
	if e := sys.SetAdmin(ctx, "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	repo := postgres.NewTemplateRepository(f.centralDB)
	t.Cleanup(func() { repo.Close() })
	f.central = mutationGateway(t, f, func(d *api.Dependencies) {
		d.Service = service
		d.Storage = store
		d.References = library
		d.Templates = repo
		d.SigningSecret = fixtureMaster
		d.AssetPublicURL = fixtureCentral
		d.MediaURLTTL = time.Hour
	})
	direct := func(method, path string, value any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(value)
		r := httptest.NewRequest(method, fixtureCentral+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(f.centralCookie)
		w := httptest.NewRecorder()
		f.central.ServeHTTP(w, r)
		return w
	}
	id := uuid.NewString()
	definition := templates.Definition{Title: "Never published media fixture", MediaType: "image", Provider: "mock", Model: "workflow-image", Syntax: "numbered", Prompt: "Image 1", Options: core.MediaOptions{ReferenceMode: "references", Resolution: "1024*1024", Ratio: "1:1"}, Slots: []templates.Slot{{ID: "image:1", Type: "image", Index: 1, Role: "reference", Label: "Subject"}}}
	expectFixture(t, direct("POST", "/v1/system/templates", map[string]any{"id": id, "draft": definition}), 201)
	data := testmedia.PNG512("never-published asset bytes")
	var body bytes.Buffer
	multi := multipart.NewWriter(&body)
	file, e := multi.CreateFormFile("file", "private-draft.png")
	if e != nil {
		t.Fatal(e)
	}
	file.Write(data)
	multi.Close()
	upload := httptest.NewRequest("POST", fixtureCentral+"/v1/system/templates/"+id+"/assets", &body)
	upload.Header.Set("Content-Type", multi.FormDataContentType())
	upload.AddCookie(f.centralCookie)
	uploaded := httptest.NewRecorder()
	f.central.ServeHTTP(uploaded, upload)
	expectFixture(t, uploaded, 201)
	var asset templates.Asset
	if json.Unmarshal(uploaded.Body.Bytes(), &asset) != nil || asset.ID == "" {
		t.Fatal("asset missing")
	}
	persisted, e := repo.Asset(ctx, asset.ID)
	if e != nil || persisted.Published || persisted.SizeBytes != int64(len(data)) {
		t.Fatal("fixture must be existing never-published asset", persisted, e)
	}
	preview := direct("GET", "/v1/system/templates/"+id+"/assets/"+asset.ID+"?preview=1", nil)
	expectFixture(t, preview, 200)
	if !bytes.Equal(preview.Body.Bytes(), data) {
		t.Fatal("draft bytes absent")
	}
	// No public endpoint issues never-published capabilities. Use the exact central
	// signer domain with the disposable secret, then prove this SAME URL equals the
	// actual central gallery-issued URL after a later authorized publication.
	mac := hmac.New(sha256.New, []byte(fixtureMaster))
	fmt.Fprintf(mac, "template-media:%s\n", asset.ID)
	path := "/template-media/" + asset.ID + "?signature=" + hex.EncodeToString(mac.Sum(nil))
	for _, method := range []string{"GET", "HEAD"} {
		original := httptest.NewRecorder()
		f.central.ServeHTTP(original, httptest.NewRequest(method, fixtureCentral+path, nil))
		expectFixture(t, original, 404)
		for _, browser := range []bool{true, false} {
			got := facadeRequest(t, f, method, path, "", v.Tokens.PlaygroundKey, browser, nil)
			expectFixture(t, got, 404)
			if bytes.Contains(got.Body.Bytes(), data) || method == "HEAD" && got.Body.Len() != 0 {
				t.Fatal("unpublished signed asset leaked")
			}
		}
	}
	definition.CoverID = asset.ID
	definition.Slots[0].AssetID = asset.ID
	expectFixture(t, direct("PUT", "/v1/system/templates/"+id, map[string]any{"version": 1, "action": "publish", "draft": definition}), 200)
	gallery := resourceDirect(f, "GET", "/v1/templates", "", v.Tokens.PlaygroundKey, "")
	expectFixture(t, gallery, 200)
	var rows struct {
		Data []struct {
			ID    string
			Cover string `json:"cover_url"`
		}
	}
	if json.Unmarshal(gallery.Body.Bytes(), &rows) != nil {
		t.Fatal("gallery JSON")
	}
	issued := ""
	for _, row := range rows.Data {
		if row.ID == id {
			issued = row.Cover
		}
	}
	if issued != path {
		t.Fatal("fixture signature differs from actual central issuer", issued, path)
	}
	positive := httptest.NewRecorder()
	f.central.ServeHTTP(positive, httptest.NewRequest("GET", fixtureCentral+issued, nil))
	expectFixture(t, positive, 200)
	if !bytes.Equal(positive.Body.Bytes(), data) {
		t.Fatal("positive signature control bytes")
	}
	expectFixture(t, facadeRequest(t, f, "GET", issued, "", v.Tokens.PlaygroundKey, true, nil), 200)
}
