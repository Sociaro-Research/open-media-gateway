package api

import (
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"net/http"
)

// All successful JSON responses share a role-aware financial boundary.
func (s *Server) writeViewerJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if admin {
		writeJSON(w, status, value)
		return
	}
	sales, err := s.isClientManager(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	var data []byte
	if sales {
		data, err = core.SalesJSON(value)
	} else {
		data, err = core.CustomerJSON(value)
	}
	if err != nil {
		writeError(w, 500, "serialization_error", "Could not prepare response")
		return
	}
	writeJSON(w, status, json.RawMessage(data))
}
