package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type headOrganizationFixture struct{ *rolePolicyFixture }

func (f *headOrganizationFixture) Organizations(context.Context, string, bool) ([]org.Organization, error) {
	return []org.Organization{{ID: fixtureOrg, Role: f.role}, {ID: fixtureOtherOrg, Role: f.role}}, nil
}

func TestOrganizationArtifactHEADUsesReadScope(t *testing.T) {
	s, _ := newPersonalTestServer()
	policy := &headOrganizationFixture{&rolePolicyFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, role: org.Supermanager}}
	policy.settings.SharedResources = true
	s.system = policy
	task := detailTask()
	task.OwnerID = fixtureOrg
	task.OrganizationID = fixtureOrg
	local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	media, err := local.Archive(context.Background(), task.ID, "data:image/png;base64,aGVsbG8=")
	if err != nil {
		t.Fatal(err)
	}
	task.StorageKey = &media.StorageKey
	task.LastFrameStorageKey = &media.StorageKey
	task.HasStoredResult = true
	s.storage = local
	s.service = &detailReportService{&accountService{tasks: []core.Task{task}}}
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Read media","organization_id":"`+fixtureOrg+`"}`)
	_, foreign := issueTestKey(t, h, "alice", `{"name":"Other organization","organization_id":"`+fixtureOtherOrg+`"}`)
	for _, kind := range []string{"generations", "reports"} {
		for _, artifact := range []string{"content", "last-frame"} {
			t.Run(kind+"/"+artifact, func(t *testing.T) {
				path := "/v1/organizations/" + fixtureOrg + "/" + kind + "/" + task.ID + "/" + artifact
				get := sendTest(h, "GET", path, "", "", key)
				head := sendTest(h, "HEAD", path, "", "", key)
				if get.Code != 200 || get.Body.String() != "hello" || head.Code != 200 || head.Body.Len() != 0 {
					t.Fatalf("read-scope parity: GET %d %q, HEAD %d %q", get.Code, get.Body.String(), head.Code, head.Body.String())
				}
				for _, name := range []string{"Content-Type", "Content-Length", "ETag"} {
					if head.Header().Get(name) != get.Header().Get(name) {
						t.Fatalf("HEAD %s differs from GET", name)
					}
				}
				for _, method := range []string{"GET", "HEAD"} {
					if w := sendTest(h, method, path, "", "", foreign); w.Code != 403 {
						t.Fatalf("foreign organization %s: %d", method, w.Code)
					}
					policy.role = org.Manager
					w := sendTest(h, method, path, "", "", key)
					policy.role = org.Supermanager
					if w.Code != 403 {
						t.Fatalf("current Manager content denial %s: %d", method, w.Code)
					}
				}
			})
		}
	}
	path := "/v1/organizations/" + fixtureOrg + "/generations/" + task.ID + "/content"
	if w := sendTest(h, "DELETE", path, "", "", key); w.Code != 403 {
		t.Fatalf("generation-only key acquired mutation authority: %d", w.Code)
	}
}
