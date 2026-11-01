package api

import (
	"errors"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

func projectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrProjectForbidden):
		writeError(w, 403, "forbidden", err.Error())
	case errors.Is(err, core.ErrProjectConflict), errors.Is(err, core.ErrProjectNameTaken):
		writeError(w, 409, "project_conflict", err.Error())
	case errors.Is(err, core.ErrTaskNotFound):
		writeError(w, 404, "not_found", "generation not found")
	case errors.Is(err, core.ErrProjectName):
		invalidParameter(w, "name", err.Error())
	case errors.Is(err, core.ErrProjectInvalid):
		invalidParameter(w, "project_id", err.Error())
	default:
		writeError(w, 503, "service_unavailable", "projects could not be read or saved")
	}
}
func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	store, ok := s.service.(core.ProjectRepository)
	if !ok {
		projectError(w, core.ErrProjectInvalid)
		return
	}
	if readMethod(r.Method) {
		projects, err := store.ListProjects(r.Context())
		if err != nil {
			projectError(w, err)
			return
		}
		s.writeViewerJSON(w, r, 200, map[string]any{"data": projects, "can_manage_projects": org.WriteProjects(identity.OrganizationFromContext(r.Context()).Role), "scope": projectScopeName(r)})
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decodeSystemLimit(w, r, &input, 4096) {
		return
	}
	p, err := store.CreateProject(r.Context(), input.Name)
	if err != nil {
		projectError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 201, p)
}

func (s *Server) manageProject(w http.ResponseWriter, r *http.Request) {
	store, ok := s.service.(core.ProjectManagement)
	if !ok {
		projectError(w, core.ErrProjectInvalid)
		return
	}
	var input struct {
		Name    string `json:"name"`
		Version *int64 `json:"version"`
	}
	if !decodeSystemLimit(w, r, &input, 4096) {
		return
	}
	if input.Version == nil || *input.Version < 1 {
		invalidParameter(w, "version", "a positive project version is required")
		return
	}
	if r.Method == "DELETE" {
		if err := store.DeleteProject(r.Context(), r.PathValue("project"), *input.Version); err != nil {
			projectError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	project, err := store.RenameProject(r.Context(), r.PathValue("project"), input.Name, *input.Version)
	if err != nil {
		projectError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, project)
}
func (s *Server) assignProject(w http.ResponseWriter, r *http.Request) {
	store, ok := s.service.(core.ProjectManagement)
	if !ok {
		projectError(w, core.ErrProjectInvalid)
		return
	}
	var input struct {
		ProjectID string `json:"project_id"`
		Version   *int64 `json:"version"`
	}
	if !decodeSystemLimit(w, r, &input, 4096) {
		return
	}
	if input.Version == nil || *input.Version < 0 {
		invalidParameter(w, "version", "the current generation project version is required")
		return
	}
	value, err := store.AssignTaskProject(r.Context(), r.PathValue("id"), input.ProjectID, *input.Version)
	if err != nil {
		projectError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, value)
}

func (s *Server) projectRoute(handler http.HandlerFunc) http.Handler {
	return s.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !readMethod(r.Method) && !org.WriteProjects(identity.OrganizationFromContext(r.Context()).Role) {
			projectError(w, core.ErrProjectForbidden)
			return
		}
		handler(w, r)
	}))
}

func projectScopeName(r *http.Request) string {
	if identity.OrganizationFromContext(r.Context()).ID != "" {
		return "company"
	}
	return "personal"
}
