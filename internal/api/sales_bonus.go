package api

import (
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
	"net/http"
)

func (s *Server) salesBonuses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if identity.APIKeyID(r.Context()) != nil {
		systemError(w, system.ErrForbidden)
		return
	}
	repo, ok := s.system.(system.SalesBonusRepository)
	if !ok {
		writeError(w, 503, "unavailable", "sales bonuses are unavailable")
		return
	}
	actor, target := identity.ActorID(r.Context()), r.PathValue("employee")
	if target != "" {
		if _, e := uuid.Parse(target); e != nil {
			writeError(w, 400, "invalid_request", "invalid employee ID")
			return
		}
	}
	var value any
	var err error
	if r.Method == "POST" {
		var in system.BonusPayout
		if !decodeSystem(w, r, &in) {
			return
		}
		var id int64
		id, err = repo.RecordBonusPayout(r.Context(), actor, target, in)
		value = map[string]any{"id": id}
	} else if r.Method == "PUT" {
		var in system.BonusSettings
		if !decodeSystem(w, r, &in) {
			return
		}
		value, err = repo.SaveBonusSettings(r.Context(), actor, in)
	} else if target != "" {
		offset, ok := systemPage(w, r)
		if !ok {
			return
		}
		items, e := repo.BonusEntries(r.Context(), actor, target, offset)
		err = e
		more := len(items) > 50
		if more {
			items = items[:50]
		}
		value = map[string]any{"data": items, "has_more": more}
	} else {
		value, err = repo.SalesBonuses(r.Context(), actor)
	}
	if errors.Is(err, system.ErrBonusInput) {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if err != nil {
		systemError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
