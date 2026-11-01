package api

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"net/http"
)

func (s *Server) salesStructure(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin || identity.APIKeyID(r.Context()) != nil {
		systemError(w, system.ErrForbidden)
		return
	}
	repo, ok := s.system.(system.SalesRepository)
	if !ok {
		writeError(w, 503, "unavailable", "sales management is unavailable")
		return
	}
	var result system.SalesStructure
	if readMethod(r.Method) {
		result, err = repo.SalesStructure(r.Context(), identity.ActorID(r.Context()))
	} else {
		id := r.PathValue("employee")
		if _, err = uuid.Parse(id); err != nil {
			writeError(w, 400, "invalid_request", "invalid employee ID")
			return
		}
		var body struct {
			FillVacancy  bool    `json:"fill_vacancy"`
			NewTeam      bool    `json:"new_team"`
			Role         string  `json:"role"`
			SupervisorID *string `json:"supervisor_id"`
			Version      int64   `json:"version"`
		}
		if !decodeSystem(w, r, &body) {
			return
		}
		if system.SalesRank(body.Role) == 0 || body.Version < 1 || body.SupervisorID == nil || (body.FillVacancy || body.NewTeam) && system.SalesRank(body.Role) < 2 || body.FillVacancy && body.NewTeam {
			writeError(w, 400, "invalid_request", "a sales role and current structure version are required")
			return
		}
		in := system.SalesPlacement{Role: body.Role, SupervisorID: *body.SupervisorID, Version: body.Version, FillVacancy: body.FillVacancy, NewTeam: body.NewTeam}
		if in.SupervisorID != "" {
			if _, err = uuid.Parse(in.SupervisorID); err != nil {
				writeError(w, 400, "invalid_request", "invalid supervisor ID")
				return
			}
		}
		result, err = repo.PlaceSalesMember(r.Context(), identity.ActorID(r.Context()), id, in)
	}
	if err != nil {
		systemError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) salesTeam(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if identity.APIKeyID(r.Context()) != nil {
		systemError(w, system.ErrForbidden)
		return
	}
	repo, ok := s.system.(system.SalesRepository)
	if !ok {
		writeError(w, 503, "unavailable", "sales management is unavailable")
		return
	}
	items, err := repo.SalesTeam(r.Context(), identity.ActorID(r.Context()))
	if err != nil {
		systemError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items})
}
