package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"net/http"
)

const storageQuotaMessage = "Shared storage limit exceeded. Delete references or stored generation results before uploading files or starting another generation."

func (s *Server) storageUsage(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.storageAdmin.(interface {
		SharedUsage(context.Context) (storage.Usage, error)
	})
	if !ok {
		writeError(w, 503, "unavailable", "storage usage is unavailable")
		return
	}
	u, e := reader.SharedUsage(r.Context())
	if e != nil {
		writeError(w, 503, "unavailable", "storage usage could not be read")
		return
	}
	s.writeViewerJSON(w, r, 200, u)
}
func (s *Server) storageAdmission(w http.ResponseWriter, r *http.Request) bool {
	if identity.StorageOwnerID(r.Context()) != identity.SharedOwnerID {
		return true
	}
	reader, ok := s.storageAdmin.(interface {
		SharedUsage(context.Context) (storage.Usage, error)
	})
	if !ok {
		return true
	}
	u, e := reader.SharedUsage(r.Context())
	if e != nil {
		writeError(w, 503, "storage_usage_unavailable", "storage usage could not be read")
		return false
	}
	if u.LimitBytes != nil && u.UnknownObjects > 0 {
		writeError(w, 503, "storage_usage_pending", "Storage usage is being verified. Try again shortly.")
		return false
	}
	if u.LimitBytes != nil && u.UsedBytes > *u.LimitBytes {
		writeError(w, 403, "storage_quota_exceeded", storageQuotaMessage)
		return false
	}
	return true
}
func (s *Server) storageQuota(w http.ResponseWriter, r *http.Request) {
	current, e := s.system.Settings(r.Context())
	if e != nil {
		systemError(w, e)
		return
	}
	if r.Method == "PUT" {
		var input struct {
			Limit   json.RawMessage `json:"limit_bytes"`
			Version int64           `json:"version"`
		}
		if !decodeSystem(w, r, &input) {
			return
		}
		if len(input.Limit) == 0 || json.Unmarshal(input.Limit, &current.SharedStorageLimitBytes) != nil {
			writeError(w, 400, "invalid_request", "limit_bytes must be an integer or null")
			return
		}
		current.Version = input.Version
		if e = current.Validate(); e != nil {
			writeError(w, 400, "invalid_request", e.Error())
			return
		}
		current, e = s.system.SaveSettings(r.Context(), identity.OwnerID(r.Context()), current)
		if e != nil {
			systemError(w, e)
			return
		}
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"limit_bytes": current.SharedStorageLimitBytes, "version": current.Version})
}
