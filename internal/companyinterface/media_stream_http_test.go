package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

func mediaHTTPFixture(t *testing.T, configure ...func(*api.Dependencies)) (*activeHTTPFixture, *storage.Local, *references.Library, context.Context) {
	t.Helper()
	f, _ := facadeHTTP(t)
	v := f.current(t)
	store, e := storage.NewLocal(t.TempDir(), http.DefaultClient, 32<<20)
	if e != nil {
		t.Fatal(e)
	}
	library := references.New(postgres.NewReferenceRepository(f.centralDB), store)
	provider := allWorkflowProvider{}
	provider.Adapter = mock.New(0, "https://unused.fixture.test")
	service := core.NewTaskServiceWithProviders(postgres.NewTaskRepository(f.centralDB), templateWorkflowQueue{}, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "workflow-image"}, "mock")
	f.central = mutationGateway(t, f, func(d *api.Dependencies) {
		d.Service = service
		d.Storage = store
		d.References = library
		d.SigningSecret = fixtureMaster
		d.AssetPublicURL = fixtureCentral
		d.MediaURLTTL = time.Hour
		for _, apply := range configure {
			apply(d)
		}
	})
	if _, e = f.centralDB.Exec(`UPDATE organizations SET credit_limit_micros=1000000 WHERE id=$1`, v.Tokens.Company); e != nil {
		t.Fatal(e)
	}
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET weekly_limit_micros=NULL WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	ctx := identity.WithOrganizationRole(identity.WithUser(context.Background(), identity.User{ID: v.Tokens.Actor}), v.Tokens.Company, "supermanager")
	return f, store, library, ctx
}
func completeMediaTask(t *testing.T, f *activeHTTPFixture, store *storage.Local, ctx context.Context, data []byte, prepare ...func(*core.StoredMedia)) core.Task {
	t.Helper()
	v := f.current(t)
	// The actual free central handler admits the task; supported worker claim and
	// completion APIs then attach isolated bytes. No native provider call is made.
	w := resourceDirect(f, "POST", "/v1/generations", `{"model":"mock/workflow-image","prompt":"media transport fixture","max_expected_cost_usd":0}`, v.Tokens.PlaygroundKey, "")
	expectFixture(t, w, 202)
	var admitted struct{ ID string }
	if e := json.Unmarshal(w.Body.Bytes(), &admitted); e != nil || admitted.ID == "" {
		t.Fatal(e)
	}
	repo := postgres.NewTaskRepository(f.centralDB)
	worker := core.WithClaimToken(context.Background(), uuid.NewString())
	if ok, e := repo.AcquireTask(worker, admitted.ID, time.Minute); e != nil || !ok {
		t.Fatal("claim", e)
	}
	if e := repo.MarkRunning(worker, admitted.ID); e != nil {
		t.Fatal(e)
	}
	stored, e := store.Archive(ctx, admitted.ID, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data))
	if e != nil {
		t.Fatal(e)
	}
	for _, apply := range prepare {
		apply(&stored)
	}
	if e = repo.MarkSucceeded(worker, admitted.ID, stored, ""); e != nil {
		t.Fatal(e)
	}
	task, e := repo.Get(ctx, admitted.ID)
	if e != nil {
		t.Fatal(e)
	}
	return task
}
func TestCompanyMediaStreamingActualContentHeadersRange(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	data := append(testmedia.PNG512("large central media"), bytes.Repeat([]byte("isolated-media-bytes"), 550000)...)
	if len(data) <= ci.MaxCustomerResponse {
		t.Fatal("fixture below old buffer limit")
	}
	task := completeMediaTask(t, f, store, ctx, data)
	for _, path := range []string{"/v1/generations/" + task.ID + "/content", "/v1/organizations/" + v.Tokens.Company + "/generations/" + task.ID + "/content"} {
		t.Run(path, func(t *testing.T) {
			for _, method := range []string{"GET", "HEAD"} {
				direct := resourceDirect(f, method, path+"?download=1", "", v.Tokens.PlaygroundKey, "")
				got := facadeRequest(t, f, method, path+"?download=1", "", v.Tokens.PlaygroundKey, true, nil)
				expectFixture(t, direct, 200)
				expectFixture(t, got, 200)
				for _, h := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Accept-Ranges", "ETag", "Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy"} {
					if got.Header().Get(h) != direct.Header().Get(h) {
						t.Fatalf("%s %s differs %q vs%q", method, h, got.Header().Get(h), direct.Header().Get(h))
					}
				}
				if got.Header().Get(ci.MediaLengthHeader) != "" || got.Header().Get(ci.MediaCompleteHeader) != "" {
					t.Fatal("internal framing leaked")
				}
				if method == "GET" && !bytes.Equal(got.Body.Bytes(), data) || method == "HEAD" && got.Body.Len() != 0 {
					t.Fatal("representation changed", got.Body.Len())
				}
			}
			got := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, func(r *http.Request) { r.Header.Set("Range", "bytes=17-4096") })
			expectFixture(t, got, 206)
			if !bytes.Equal(got.Body.Bytes(), data[17:4097]) || got.Header().Get("Content-Range") != fmt.Sprintf("bytes 17-4096/%d", len(data)) || got.Header().Get("Content-Length") != "4080" {
				t.Fatal("range changed", got.Header())
			}
		})
	}
	for _, p := range []string{"/v1/system/reports/" + task.ID + "/content", "/v1/admin/reports/" + task.ID + "/content"} {
		expectFixture(t, facadeRequest(t, f, "GET", p, "", v.Tokens.PlaygroundKey, true, nil), 403)
	}
	if countFixture(t, f.localDB, "tasks") != 0 {
		t.Fatal("local shadow task")
	}
}
func TestCompanyMediaSignedPrivateReferencePreservesActor(t *testing.T) {
	f, _, library, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	data := testmedia.PNG512("signed reference fixture")
	item, e := library.Upload(ctx, "ref.png", bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	list := resourceDirect(f, "GET", "/v1/references", "", v.Tokens.PlaygroundKey, "")
	expectFixture(t, list, 200)
	var result struct{ Data []struct{ ID, URL string } }
	json.Unmarshal(list.Body.Bytes(), &result)
	var path string
	for _, r := range result.Data {
		if r.ID == item.ID {
			path = r.URL
		}
	}
	if path == "" {
		t.Fatal("missing signed reference")
	}
	got := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, got, 200)
	if !bytes.Equal(got.Body.Bytes(), data) {
		t.Fatal("signed bytes changed")
	}
	expectFixture(t, facadeRequest(t, f, "GET", strings.Replace(path, v.Tokens.Company, uuid.NewString(), 1), "", v.Tokens.PlaygroundKey, true, nil), 403)
	expectFixture(t, facadeRequest(t, f, "GET", path+"&signature=duplicate", "", v.Tokens.PlaygroundKey, true, nil), 403)
}
