package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type detailReportService struct{ *accountService }

func (s *detailReportService) CostReport(ctx context.Context, f core.ReportFilter) ([]core.CostReportRow, error) {
	rows := []core.CostReportRow{}
	for _, task := range s.tasks {
		if task.ID != f.GenerationID || (!f.AllOrganizations && task.OwnerID != identity.OwnerID(ctx)) || (f.AllOrganizations && task.OrganizationID == "") || (identity.FinancialAuthorFilter(ctx) != "" && task.ActorID != identity.ActorID(ctx)) {
			continue
		}
		rows = append(rows, core.CostReportRow{ID: task.ID, OrganizationID: task.OrganizationID, UserID: task.ActorID, Model: task.Model, Status: string(task.Status), Usage: task.Usage, Cost: task.Cost, CreatedAt: task.CreatedAt})
	}
	return rows, nil
}
func detailTask() core.Task {
	now := time.Now().UTC()
	hook := "https://example.test/webhook"
	audio := "https://example.test/audio"
	seed := int64(123)
	on := true
	return core.Task{ID: "abcdefab-1111-4111-8111-abcdefabcdef", OwnerID: identity.PersonalOwnerID("alice"), ActorID: "alice", Status: core.TaskSucceeded, Provider: "mock", Model: "mock-image", MediaType: "image", Prompt: "Full private prompt\nsecond line", CreatedAt: now, UpdatedAt: now, WebhookURL: &hook, Options: core.MediaOptions{Resolution: "1024x1024", Ratio: "1:1", Duration: 5, Seed: &seed, GenerateAudio: &on, ImageURLs: []string{"https://example.test/reference"}, AudioURL: audio, Instructions: "Private instruction", NegativePrompt: "Private negative", Parameters: map[string]any{"custom": "Private extension"}}, Usage: &core.ProviderUsage{Raw: json.RawMessage(`{"private":"Raw private usage"}`)}}
}
func TestReportDetailPersonalAccessAndArtifacts(t *testing.T) {
	s, _ := newPersonalTestServer()
	task := detailTask()
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	media, err := store.Archive(context.Background(), task.ID, "data:image/png;base64,aGVsbG8=")
	if err != nil {
		t.Fatal(err)
	}
	task.StorageKey = &media.StorageKey
	task.HasStoredResult = true
	task.LastFrameStorageKey = &media.StorageKey
	s.storage = store
	service := &detailReportService{&accountService{tasks: []core.Task{task}}}
	s.service = service
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Reader","scopes":["generation","keys:manage"]}`)
	_, limited := issueTestKey(t, h, "alice", `{"name":"Generation only"}`)
	path := "/v1/admin/reports/" + strings.ToUpper(task.ID)
	for _, auth := range []struct{ session, key string }{{"alice", ""}, {"", key}} {
		w := sendTest(h, "GET", path, "", auth.session, auth.key)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var data struct {
			Request struct {
				Prompt  string
				Options core.MediaOptions
			}
			Links map[string]string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if data.Request.Prompt != task.Prompt || data.Request.Options.AudioURL != task.Options.AudioURL || data.Request.Options.Parameters["custom"] != "Private extension" {
			t.Fatal("missing saved request", w.Body.String())
		}
		for _, suffix := range []string{"/content", "/content?download=1", "/last-frame"} {
			result := sendTest(h, "GET", path+suffix, "", auth.session, auth.key)
			if result.Code != 200 || result.Body.String() != "hello" {
				t.Fatal("artifact", suffix, result.Code, result.Body.String())
			}
		}
		head := sendTest(h, "HEAD", path+"/content", "", auth.session, auth.key)
		if head.Code != 200 || head.Body.Len() != 0 {
			t.Fatal("HEAD", head.Code, head.Body.String())
		}
		if !strings.HasSuffix(data.Links["download_url"], "/content?download=1") {
			t.Fatal("download link missing")
		}
	}
	for _, tc := range []struct {
		path, session, key string
		status             int
	}{{path, "bob", "", 404}, {path, "", limited, 403}, {path, "", "", 401}, {"/v1/admin/reports/bad", "alice", "", 400}, {"/v1/admin/reports/11111111-1111-4111-8111-111111111111", "alice", "", 404}} {
		w := sendTest(h, "GET", tc.path, "", tc.session, tc.key)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	deleted := time.Now()
	service.tasks[0].ResultDeletedAt = &deleted
	w := sendTest(h, "GET", path, "", "alice", "")
	if strings.Contains(w.Body.String(), "download_url") {
		t.Fatal("deleted file linked", w.Body.String())
	}
	if w := sendTest(h, "GET", path+"/content", "", "alice", ""); w.Code != 404 {
		t.Fatal("deleted file readable", w.Code)
	}
}
func TestReportDetailOrganizationPermissionsAndRetention(t *testing.T) {
	for _, role := range []string{org.Supermanager, org.Manager, org.ContentOfficer, org.Developer, org.MemberRole} {
		t.Run(role, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := &rolePolicyFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}}, role: role}
			f.settings.SharedResources = true
			s.system = f
			task := detailTask()
			task.OwnerID = fixtureOrg
			task.OrganizationID = fixtureOrg
			service := &detailReportService{&accountService{tasks: []core.Task{task}}}
			s.service = service
			path := "/v1/organizations/" + fixtureOrg + "/reports/" + task.ID
			w := sendTest(s.handler(), "GET", path, "", "alice", "")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if role == org.Manager {
				if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "Private") || strings.Contains(w.Body.String(), "example.test") {
					t.Fatal("restricted role leaked content", w.Body.String())
				}
				if w := sendTest(s.handler(), "GET", path+"/content", "", "alice", ""); w.Code != 403 {
					t.Fatal("restricted content", w.Code)
				}
			} else if !strings.Contains(w.Body.String(), "Full private prompt") {
				t.Fatal("prompt absent", w.Body.String())
			}
			service.tasks[0].MinimizeRequestLogging = true
			w = sendTest(s.handler(), "GET", path, "", "alice", "")
			if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "Private") || strings.Contains(w.Body.String(), "example.test") {
				t.Fatal("minimal logging leaked in-flight content", w.Body.String())
			}
			if role != org.Manager && !strings.Contains(w.Body.String(), `"resolution":"1024x1024"`) {
				t.Fatal("retained options missing", w.Body.String())
			}
			if role == org.Developer || role == org.MemberRole {
				service.tasks[0].ActorID = "bob"
				if w := sendTest(s.handler(), "GET", path, "", "alice", ""); w.Code != 404 {
					t.Fatal("colleague report", w.Code)
				}
			}
		})
	}
}
func TestReportDetailSystemAndClientManagerScope(t *testing.T) {
	s, _ := newPersonalTestServer()
	task := detailTask()
	task.OwnerID = fixtureOrg
	task.OrganizationID = fixtureOrg
	s.service = &detailReportService{&accountService{tasks: []core.Task{task}}}
	f := &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, manager: true, assigned: fixtureOrg}
	f.settings.SharedResources = true
	s.system = f
	h := s.handler()
	systemPath := "/v1/system/reports/" + task.ID
	w := sendTest(h, "GET", systemPath, "", "alice", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Full private prompt") || !strings.Contains(w.Body.String(), `"content_access":true`) {
		t.Fatal("system detail", w.Code, w.Body.String())
	}
	if w := sendTest(h, "GET", systemPath, "", "bob", ""); w.Code != 403 {
		t.Fatal("non-admin system detail", w.Code)
	}
	path := "/v1/organizations/" + fixtureOrg + "/reports/" + task.ID
	w = sendTest(h, "GET", path, "", "bob", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "example.test") {
		t.Fatal("client manager detail", w.Code, w.Body.String())
	}
	for _, p := range []string{path + "/content", strings.Replace(path, fixtureOrg, "22222222-2222-4222-8222-222222222222", 1)} {
		if w := sendTest(h, "GET", p, "", "bob", ""); w.Code != 403 {
			t.Fatal("client manager boundary", p, w.Code)
		}
	}
}
