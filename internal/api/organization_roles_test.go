package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type rolePolicyFixture struct {
	*organizationFixture
	role string
}

func (f *rolePolicyFixture) OrganizationAccess(context.Context, string, string) (string, error) {
	return f.role, nil
}
func (f *rolePolicyFixture) Organizations(context.Context, string, bool) ([]org.Organization, error) {
	return []org.Organization{{ID: fixtureOrg, Role: f.role}}, nil
}

func TestOrganizationContentPolicyForSessionsAndExistingKeys(t *testing.T) {
	for _, role := range []string{org.Supermanager, org.Manager, org.ContentOfficer, org.Developer, org.MemberRole} {
		t.Run(role, func(t *testing.T) {
			s, keys := newPersonalTestServer()
			f := &rolePolicyFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}}, role: org.Supermanager}
			f.settings.SharedResources = true
			s.system = f
			_, secret := issueTestKey(t, s.handler(), "alice", `{"name":"Existing","organization_id":"`+fixtureOrg+`"}`)
			f.role = role // Authorization must check the current role, not the issuing role.
			_ = keys
			resultURL := "https://private.example.test/output"
			failure := "Sensitive provider error"
			now := time.Now()
			task := core.Task{ID: "33333333-3333-4333-8333-333333333333", OwnerID: fixtureOrg, ActorID: "alice", Prompt: "Sensitive prompt", Status: core.TaskSucceeded, MediaType: "image", Model: "mock-image-v1", Provider: "mock", CreatedAt: now, UpdatedAt: now, Options: core.MediaOptions{Instructions: "Sensitive instruction", ImageURLs: []string{"https://private.example.test/input"}}, ResultURL: &resultURL, WebhookURL: &resultURL, Error: &failure, Usage: &core.ProviderUsage{Raw: json.RawMessage(`{"prompt":"Sensitive raw"}`)}}
			task.PublicModel = "studio-image"
			s.service = &accountService{tasks: []core.Task{task}}
			path := "/v1/organizations/" + fixtureOrg + "/generations/" + task.ID
			for _, auth := range []struct{ session, key, path string }{{"alice", "", path}, {"", secret, "/v1/generations/" + task.ID}} {
				w := sendTest(s.handler(), "GET", auth.path, "", auth.session, auth.key)
				if w.Code != 200 {
					t.Fatalf("read %d: %s", w.Code, w.Body.String())
				}
				var generation struct {
					Model         string `json:"model"`
					ProviderModel string `json:"provider_model"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &generation); err != nil {
					t.Fatal(err)
				}
				if generation.Model != "studio-image" {
					t.Fatal("public model changed", w.Body.String())
				}
				if role == org.Manager {
					if generation.ProviderModel != "" {
						t.Fatal("manager received native model details", w.Body.String())
					}
				} else if generation.ProviderModel != task.Model {
					t.Fatal("native model changed", w.Body.String())
				}
				if role == org.Manager {
					for _, sensitive := range []string{"Sensitive", "private.example", "content_url", "webhook_url", "instructions", "image"} {
						if sensitive == "image" {
							continue
						}
						if strings.Contains(w.Body.String(), sensitive) {
							t.Fatalf("manager leaked %s: %s", sensitive, w.Body.String())
						}
					}
				} else if !strings.Contains(w.Body.String(), "Sensitive prompt") {
					t.Fatal("allowed content missing", w.Body.String())
				}
			}
			if role == org.Manager {
				for _, suffix := range []string{"/content", "/media-access", "/last-frame"} {
					if w := sendTest(s.handler(), "GET", path+suffix, "", "alice", ""); w.Code != 403 {
						t.Fatalf("manager content route %s: %d", suffix, w.Code)
					}
				}
			}
			if role == org.ContentOfficer || role == org.Manager {
				if w := sendTest(s.handler(), "DELETE", path+"/content", "", "alice", ""); w.Code != 403 {
					t.Fatal("read-only content role deleted result", w.Code)
				}
			}
			if role == org.ContentOfficer {
				if w := sendTest(s.handler(), "POST", "/v1/generations", `{"model":"mock/mock-image-v1","prompt":"test"}`, "", secret); w.Code != 403 {
					t.Fatalf("officer generated through old key: %d", w.Code)
				}
			}
			if role == org.Developer || role == org.MemberRole {
				task.ActorID = "bob"
				s.service = &accountService{tasks: []core.Task{task}}
				if w := sendTest(s.handler(), "GET", path, "", "alice", ""); w.Code != 404 {
					t.Fatalf("colleague task access: %d", w.Code)
				}
				w := sendTest(s.handler(), "GET", "/v1/organizations/"+fixtureOrg+"/generations", "", "alice", "")
				if strings.Contains(w.Body.String(), task.ID) {
					t.Fatal("colleague task in own journal", w.Body.String())
				}
			}
		})
	}
}
