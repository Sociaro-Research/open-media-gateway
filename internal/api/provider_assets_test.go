package api

import (
	"context"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerassets"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type assetRepoFixture struct {
	providerassets.Repository
	owner, author string
}

func (f *assetRepoFixture) Groups(ctx context.Context, _, _ string) ([]providerassets.Group, error) {
	f.owner = identity.OwnerID(ctx)
	f.author = identity.ContentAuthorFilter(ctx)
	return []providerassets.Group{}, nil
}
func (f *assetRepoFixture) SaveGroup(context.Context, string, string, providerassets.Group) error {
	return nil
}

type assetAPIDirectory struct{ client *assetAPIClient }

func (f assetAPIDirectory) AssetLibrary(context.Context, string) (providerassets.Client, error) {
	return f.client, nil
}

type assetAPIClient struct {
	providerassets.Client
	inventories int
}

func (*assetAPIClient) Scope() string { return "fixture" }
func (f *assetAPIClient) List(context.Context, string, string, string) (providerassets.Page, error) {
	f.inventories++
	return providerassets.Page{Items: []providerassets.Asset{}}, nil
}
func (*assetAPIClient) CreateGroup(context.Context, string) (providerassets.Group, error) {
	return providerassets.Group{ID: "group-fixture", Name: "Characters"}, nil
}

func TestProviderAssetAPIOrganizationRolesAndInventory(t *testing.T) {
	for _, role := range []string{org.Supermanager, org.Manager, org.ContentOfficer, org.Developer, org.MemberRole} {
		t.Run(role, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := &rolePolicyFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}}, role: org.Supermanager}
			f.settings.SharedResources = true
			s.system = f
			repo, client := &assetRepoFixture{}, &assetAPIClient{}
			s.providerAssets = providerassets.New(repo, assetAPIDirectory{client})
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"Generation","organization_id":"`+fixtureOrg+`"}`)
			f.role = role
			w := sendTest(h, "GET", "/v1/references/providers/bytedance/groups", "", "", key)
			want := 200
			if role == org.Manager {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("read: %d %s", w.Code, w.Body)
			}
			if want == 200 {
				if repo.owner != fixtureOrg {
					t.Fatal("wrong data owner", repo.owner)
				}
				if role == org.Developer || role == org.MemberRole {
					if repo.author != "alice" {
						t.Fatal("missing author restriction", repo.author)
					}
				}
			}
			w = sendTest(h, "POST", "/v1/references/providers/bytedance/groups", `{"name":"Characters"}`, "", key)
			want = 201
			if role == org.Manager || role == org.ContentOfficer {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("write: %d %s", w.Code, w.Body)
			}
			w = sendTest(h, "GET", "/v1/references/providers/bytedance/inventory", "", "", key)
			if w.Code != 403 || client.inventories != 0 {
				t.Fatalf("shared inventory leak: %d %s", w.Code, w.Body)
			}
		})
	}
	// A generation key owned by an administrator still needs the matching browser
	// session to enumerate the shared provider account; system-scoped keys are explicit.
	s, _ := newPersonalTestServer()
	f := &rolePolicyFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, role: org.Supermanager}
	f.settings.SharedResources = true
	s.system = f
	client := &assetAPIClient{}
	s.providerAssets = providerassets.New(&assetRepoFixture{}, assetAPIDirectory{client})
	h := s.handler()
	_, generation := issueTestKey(t, h, "alice", `{"name":"Generation","organization_id":"`+fixtureOrg+`"}`)
	_, systemKey := issueTestKey(t, h, "alice", `{"name":"System","organization_id":"`+fixtureOrg+`","scopes":["generation","system:manage"]}`)
	for _, tc := range []struct {
		session, key string
		want         int
	}{{"", generation, 403}, {"bob", generation, 403}, {"alice", generation, 200}, {"", systemKey, 200}} {
		w := sendTest(h, "GET", "/v1/references/providers/bytedance/inventory", "", tc.session, tc.key)
		if w.Code != tc.want {
			t.Fatalf("inventory session %q: %d want %d: %s", tc.session, w.Code, tc.want, w.Body)
		}
	}
	if client.inventories != 2 {
		t.Fatal(client.inventories)
	}
}
