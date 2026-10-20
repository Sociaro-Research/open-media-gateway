package api

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"net/http"
	"time"
)

func (s *Server) deleteResult(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.generationTask(w, r); !ok {
		return
	}
	service, ok := s.service.(interface {
		DeleteResult(context.Context, string, func(context.Context, string, string) error) error
	})
	store, storeOK := s.storage.(interface {
		DeleteResult(context.Context, string, string) error
	})
	if !ok || !storeOK {
		writeError(w, 503, "unavailable", "result deletion unavailable")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	e := service.DeleteResult(ctx, r.PathValue("id"), store.DeleteResult)
	if errors.Is(e, core.ErrTaskNotFound) {
		writeError(w, 404, "not_found", "task not found")
		return
	}
	if errors.Is(e, core.ErrTaskActive) {
		writeError(w, 409, "task_active", e.Error())
		return
	}
	if e != nil {
		writeError(w, 503, "delete_failed", "Some result files could not be deleted. Retry deletion; task history and costs are preserved.")
		return
	}
	w.WriteHeader(204)
}
