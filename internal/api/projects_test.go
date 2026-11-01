package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

type projectAPIService struct {
	*openAIServiceFixture
	id            string
	limit, offset int
}

func (s *projectAPIService) ListProjects(context.Context) ([]core.Project, error) {
	return []core.Project{{ID: "project", Name: "Film"}}, nil
}
func (s *projectAPIService) CreateProject(_ context.Context, name string) (core.Project, error) {
	name, err := core.ProjectName(name)
	return core.Project{ID: "project", Name: name}, err
}
func (s *projectAPIService) ListProjectTasks(_ context.Context, id string, limit, offset int) ([]core.Task, error) {
	s.id = id
	s.limit = limit
	s.offset = offset
	if id != "project" && id != core.NoProjectFilter {
		return nil, core.ErrProjectInvalid
	}
	return []core.Task{{ID: "one"}, {ID: "two"}}, nil
}
func TestProjectsAPIContractsAndPrivateMetadata(t *testing.T) {
	s := &projectAPIService{openAIServiceFixture: openAIFixture()}
	h := New(s, healthStub{}, "secret")
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/projects", "", 200}, {"POST", "/v1/projects", `{"name":"  Film  "}`, 201}, {"POST", "/v1/projects", `{"name":" "}`, 400}, {"POST", "/v1/projects", `{"name":"x","owner_id":"other"}`, 400}, {"POST", "/v1/projects", `{"name":"x"} {}`, 400},
		{"GET", "/v1/generations?project_id=project&limit=1&offset=7", "", 200}, {"GET", "/v1/generations?project_id=foreign", "", 400},
		{"GET", "/v1/generations?project_id=none&limit=1&offset=7", "", 200},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("anonymous projects access", w.Code)
		}
		r = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer secret")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if strings.Contains(tc.path, "limit=1") && (!strings.Contains(w.Body.String(), `"has_more":true`) || s.limit != 2 || s.offset != 7 || (strings.Contains(tc.path, "project_id=none") && s.id != core.NoProjectFilter)) {
			t.Fatal("pagination", w.Body.String(), s.limit, s.offset)
		}
	}
	w := openAIRequest(h, "/v1/generations", `{"model":"mock/image","prompt":"scene","project_id":"11111111-1111-4111-8111-111111111111"}`)
	if w.Code != 202 || s.input.ProjectID != "11111111-1111-4111-8111-111111111111" {
		t.Fatal("optional metadata not accepted", w.Code, w.Body.String(), s.input)
	}
	s.err = core.ErrProjectInvalid
	w = openAIRequest(h, "/v1/generations", `{"model":"mock/image","prompt":"scene","project_id":"foreign"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"project_id"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	task := core.Task{ID: "t", ActorID: "alice", ProjectID: "private-project"}
	for _, actor := range []string{"alice", "bob"} {
		r := httptest.NewRequest(http.MethodGet, "/v1/generations/t", nil)
		r = r.WithContext(identity.WithOwnerID(r.Context(), actor))
		b, err := json.Marshal((&Server{}).generationView(r, task))
		if err != nil || strings.Contains(string(b), "private-project") != (actor == "alice") {
			t.Fatal("personal metadata disclosure", actor, string(b), err)
		}
	}
}

func (s *projectAPIService) RenameProject(_ context.Context, id, name string, version int64) (core.Project, error) {
	if version != 1 {
		return core.Project{}, core.ErrProjectConflict
	}
	return core.Project{ID: id, Name: name, Version: 2}, nil
}
func (s *projectAPIService) DeleteProject(_ context.Context, id string, version int64) error {
	if version != 1 {
		return core.ErrProjectConflict
	}
	return nil
}
func (s *projectAPIService) AssignTaskProject(_ context.Context, id, project string, version int64) (core.ProjectAssignment, error) {
	if version != 0 {
		return core.ProjectAssignment{}, core.ErrProjectConflict
	}
	return core.ProjectAssignment{ProjectID: project, Version: 1}, nil
}
func TestProjectManagementAPI(t *testing.T) {
	h := New(&projectAPIService{openAIServiceFixture: openAIFixture()}, healthStub{}, "secret")
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"PATCH", "/v1/projects/project", `{"name":"New name","version":1}`, 200},
		{"PATCH", "/v1/projects/project", `{"name":"New name"}`, 400},
		{"PATCH", "/v1/projects/project", `{"name":"New name","version":2}`, 409},
		{"DELETE", "/v1/projects/project", `{"version":1}`, 204},
		{"DELETE", "/v1/projects/project", `{"version":2}`, 409},
		{"DELETE", "/v1/projects/project", `{}`, 400},
		{"PATCH", "/v1/generations/task/project", `{"project_id":"project","version":0}`, 200},
		{"PATCH", "/v1/generations/task/project", `{"project_id":null,"version":0}`, 200},
		{"PATCH", "/v1/generations/task/project", `{"project_id":"project","version":1}`, 409},
		{"PATCH", "/v1/generations/task/project", `{"project_id":"project"}`, 400},
		{"PATCH", "/v1/generations/task/project", `{"owner_id":"foreign","version":0}`, 400},
	} {
		for _, authorized := range []bool{false, true} {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if authorized {
				req.Header.Set("Authorization", "Bearer secret")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			want := 401
			if authorized {
				want = tc.status
			}
			if w.Code != want {
				t.Fatal(tc.method, tc.path, w.Code, w.Body.String())
			}
		}
	}
}

// Status does not authorize or hide grouping; authorship and content roles do.
func TestProjectActionPermissionsAcrossTaskStatuses(t *testing.T) {
	for _, status := range []core.TaskStatus{core.TaskQueued, core.TaskRunning, core.TaskSucceeded, core.TaskFailed} {
		for _, tc := range []struct {
			name, actor, role string
			allowed           bool
		}{
			{"personal author", "alice", "", true}, {"personal other author", "bob", "", false},
			{"organization supermanager author", "alice", "supermanager", true}, {"organization supermanager other author", "bob", "supermanager", true},
			{"developer author", "alice", "developer", true}, {"member author", "alice", "member", true},
			{"developer other company author", "bob", "developer", true}, {"member other company author", "bob", "member", true},
			{"manager content restricted", "alice", "admin", false}, {"content officer read only", "alice", "content_officer", false},
		} {
			t.Run(string(status)+"/"+tc.name, func(t *testing.T) {
				task := core.Task{ID: "generation", ActorID: "alice", ProjectID: "private-project", Status: status}
				if tc.role != "" {
					task.OwnerID = "workspace"
					task.OrganizationID = "workspace"
				}
				r := httptest.NewRequest(http.MethodGet, "/v1/generations/generation", nil)
				ctx := identity.WithOwnerID(r.Context(), tc.actor)
				if tc.role != "" {
					ctx = identity.WithOrganizationRole(ctx, "workspace", tc.role)
				}
				r = r.WithContext(ctx)
				body, err := json.Marshal((&Server{}).generationView(r, task))
				if err != nil {
					t.Fatal(err)
				}
				var data struct {
					CanChange bool   `json:"can_change_project"`
					Project   string `json:"project_id"`
				}
				if err = json.Unmarshal(body, &data); err != nil {
					t.Fatal(err)
				}
				if data.CanChange != tc.allowed {
					t.Fatalf("permission: %s", body)
				}
				if tc.actor != "alice" && tc.role == "" && data.Project != "" {
					t.Fatalf("foreign project disclosed: %s", body)
				}
			})
		}
	}
}

func TestCompanyProjectWriteRoutesRespectRoles(t *testing.T) {
	h := New(&projectAPIService{openAIServiceFixture: openAIFixture()}, healthStub{}, "secret")
	for _, role := range []string{"supermanager", "developer", "member", "content_officer", "admin"} {
		allowed := role == "supermanager" || role == "developer" || role == "member"
		req := httptest.NewRequest("GET", "/v1/projects", nil)
		req.Header.Set("Authorization", "Bearer secret")
		req = req.WithContext(identity.WithOrganizationRole(req.Context(), "workspace", role))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var response struct {
			CanManage bool   `json:"can_manage_projects"`
			Scope     string `json:"scope"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.CanManage != allowed || response.Scope != "company" {
			t.Fatal(role, w.Code, w.Body.String(), err)
		}

		for _, tc := range []struct{ method, path, body string }{{"POST", "/v1/projects", `{"name":"Film"}`}, {"PATCH", "/v1/projects/project", `{"name":"Film","version":1}`}, {"DELETE", "/v1/projects/project", `{"version":1}`}, {"PATCH", "/v1/generations/task/project", `{"project_id":"project","version":0}`}} {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer secret")
			req = req.WithContext(identity.WithOrganizationRole(req.Context(), "workspace", role))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if allowed && w.Code >= 400 || !allowed && w.Code != 403 {
				t.Fatalf("%s %s: %d %s", role, tc.path, w.Code, w.Body.String())
			}
		}
	}
}
