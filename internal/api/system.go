package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

type systemSettingsKey struct{}

func (s *Server) systemAdmin(ctx context.Context) (bool, error) {
	if s.system == nil {
		return false, nil
	}
	if identity.APIKeyID(ctx) != nil {
		scopes, _ := ctx.Value(apiKeyScopesContextKey{}).([]string)
		if !slices.Contains(scopes, apikey.SystemManage) {
			return false, nil
		}
	}
	return s.system.IsAdmin(ctx, identity.ActorID(ctx))
}
func (s *Server) systemPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.system == nil {
			next.ServeHTTP(w, r)
			return
		}
		settings, err := s.system.Settings(r.Context())
		if err != nil {
			systemError(w, err)
			return
		}
		path := r.URL.Path
		if (identity.ProviderOwnerID(r.Context()) == identity.SharedOwnerID || identity.StorageOwnerID(r.Context()) == identity.SharedOwnerID) && !settings.SharedResources && ((r.Method == "POST" && r.Pattern != "POST /v1/references/uploads/{id}/complete") || isModelCatalogPath(path)) && !strings.HasPrefix(path, "/v1/system/") {
			writeError(w, 403, "shared_resources_disabled", "shared providers and storage are disabled")
			return
		}
		// Apply consumption policy to the account role, independently of management scopes.
		generation := (r.Method == http.MethodPost && isGenerationPath(path)) || isModelCatalogPath(path)
		upload := (path == "/v1/references" || strings.HasPrefix(path, "/v1/references/uploads")) && r.Method == "POST"
		// Completion consumes an already admitted upload; quota changes must not
		// prevent finalization or a retry of a successful completion response.
		startingUpload := path == "/v1/references" || path == "/v1/references/uploads"
		if (generation || startingUpload) && r.Method == "POST" && !s.storageAdmission(w, r) {
			return
		}
		if (generation || upload) && identity.ActorID(r.Context()) != identity.SystemOwnerID {
			admin, err := s.system.IsAdmin(r.Context(), identity.ActorID(r.Context()))
			if err != nil {
				systemError(w, err)
				return
			}
			if !admin && generation && !settings.PersonalProviders && identity.ProviderOwnerID(r.Context()) != identity.SharedOwnerID {
				writeError(w, 403, "personal_providers_disabled", "personal provider access is disabled; use a shared key when shared access is enabled")
				return
			}
			if !admin && r.Method == "POST" && identity.StorageOwnerID(r.Context()) != identity.SharedOwnerID && s.storageAdmin != nil {
				current, err := s.storageAdmin.Get(r.Context())
				if err != nil {
					writeError(w, 503, "storage_unavailable", "storage settings could not be read")
					return
				}
				if current.Driver != "s3" {
					writeError(w, 403, "personal_storage_required", "configure personal S3 storage or use a key with shared storage")
					return
				}
			}
		}
		personalStorageConfiguration := strings.HasPrefix(path, "/v1/admin/storage")
		providerConfiguration := path == "/v1/admin/providers" || strings.HasPrefix(path, "/v1/admin/providers/")
		if (strings.HasPrefix(path, "/v1/system/") && !sharedTemplateRead(r)) || providerConfiguration || personalStorageConfiguration {
			admin, err := s.systemAdmin(r.Context())
			if err != nil {
				systemError(w, err)
				return
			}
			if !admin && (!(providerConfiguration || personalStorageConfiguration) || !settings.PersonalProviders) {
				writeError(w, 403, "system_policy", "this operation requires administrator access")
				return
			}
		}
		if strings.HasPrefix(path, "/v1/system/providers") || strings.HasPrefix(path, "/v1/system/storage") || strings.HasPrefix(path, "/v1/system/templates") || strings.HasPrefix(path, "/v1/system/template-references") {
			r = r.WithContext(identity.WithResourceOwners(r.Context(), identity.SharedOwnerID, identity.SharedOwnerID))
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(system.WithAdmissionCheck(r.Context()), systemSettingsKey{}, settings)))
	})
}
func systemError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, system.ErrSetupBusy):
		writeError(w, 409, "setup_busy", err.Error())
	case errors.Is(err, system.ErrForbidden):
		writeError(w, 403, "forbidden", err.Error())
	case errors.Is(err, system.ErrRoleConflict), errors.Is(err, system.ErrSalesPlacement), errors.Is(err, system.ErrLastAdmin), errors.Is(err, system.ErrConflict), errors.Is(err, system.ErrBootstrapped):
		writeError(w, 409, "conflict", err.Error())
	case errors.Is(err, system.ErrNotFound):
		writeError(w, 404, "not_found", err.Error())
	default:
		writeError(w, 503, "service_unavailable", "system administration is unavailable")
	}
}
func decodeSystem(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeSystemLimit(w, r, v, 8192)
}
func decodeSystemLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil || d.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid_request", "send one JSON object with the documented fields")
		return false
	}
	return true
}
func (s *Server) systemSettings(w http.ResponseWriter, r *http.Request) {
	if readMethod(r.Method) {
		settings, err := s.system.Settings(r.Context())
		if err != nil {
			systemError(w, err)
			return
		}
		s.writeViewerJSON(w, r, 200, settings)
		return
	}
	var input struct {
		CustomerWorkspaces    *bool          `json:"customer_workspaces"`
		MarkupPercent         *money.Percent `json:"markup_percent"`
		AllowInlineReferences *bool          `json:"allow_inline_references"`
		SharedResources       *bool          `json:"shared_resources"`
		GenerationUI          *bool          `json:"generation_ui"`
		PersonalProviders     *bool          `json:"personal_providers"`
		Version               int64          `json:"version"`
	}
	if !decodeSystem(w, r, &input) {
		return
	}
	if input.GenerationUI == nil {
		writeError(w, 400, "invalid_request", "generation_ui is required")
		return
	}
	current, err := s.system.Settings(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	next := current
	next.GenerationUI = *input.GenerationUI
	next.Version = input.Version
	if input.PersonalProviders != nil {
		next.PersonalProviders = *input.PersonalProviders
	}
	if input.CustomerWorkspaces != nil {
		next.CustomerWorkspaces = *input.CustomerWorkspaces
	}
	if input.MarkupPercent != nil {
		next.MarkupPercent = *input.MarkupPercent
	}
	if input.AllowInlineReferences != nil {
		next.AllowInlineReferences = *input.AllowInlineReferences
	}
	if input.SharedResources != nil {
		next.SharedResources = *input.SharedResources
	}
	if err := next.Validate(); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	saved, err := s.system.SaveSettings(r.Context(), identity.ActorID(r.Context()), next)
	if err != nil {
		systemError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, saved)
}
func systemPage(w http.ResponseWriter, r *http.Request) (int, bool) {
	offset := 0
	var err error
	if r.URL.Query().Has("offset") {
		offset, err = strconv.Atoi(r.URL.Query().Get("offset"))
	}
	if err != nil || offset < 0 || offset > 100000 {
		writeError(w, 400, "invalid_request", "offset must be from 0 to 100000")
		return 0, false
	}
	return offset, true
}
func (s *Server) systemUsers(w http.ResponseWriter, r *http.Request) {
	offset, ok := systemPage(w, r)
	if !ok {
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > 200 {
		writeError(w, 400, "invalid_request", "search must be at most 200 bytes")
		return
	}
	users, err := s.system.Users(r.Context(), search, r.URL.Query().Get("admins_only") == "true", 51, offset)
	if err != nil {
		systemError(w, err)
		return
	}
	more := len(users) > 50
	if more {
		users = users[:50]
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": users, "has_more": more, "next_offset": offset + len(users)})
}
func (s *Server) setSystemAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, 400, "invalid_request", "invalid user ID")
		return
	}
	var input struct {
		Role          *string `json:"role"`
		Admin         *bool   `json:"is_system_admin"`
		ClientManager *bool   `json:"is_client_manager"`
	}
	if !decodeSystem(w, r, &input) {
		return
	}
	if input.Role != nil {
		if input.Admin != nil || input.ClientManager != nil || !(slices.Contains([]string{"user", "system_admin"}, *input.Role) || system.SalesRank(*input.Role) > 0) {
			writeError(w, 400, "invalid_request", "send a user, administrator or sales role")
			return
		}
		repo, ok := s.system.(interface {
			SetUserRole(context.Context, string, string, string) error
		})
		if !ok {
			writeError(w, 503, "unavailable", "role changes are unavailable")
			return
		}
		if err := repo.SetUserRole(r.Context(), identity.ActorID(r.Context()), id, *input.Role); err != nil {
			systemError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	if input.ClientManager != nil {
		if input.Admin != nil {
			writeError(w, 400, "invalid_request", "change one role at a time")
			return
		}
		repo, ok := s.system.(system.ClientManagers)
		if !ok {
			writeError(w, 503, "unavailable", "client managers are unavailable")
			return
		}
		if err := repo.SetClientManager(r.Context(), identity.ActorID(r.Context()), id, *input.ClientManager); err != nil {
			systemError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	if input.Admin == nil {
		writeError(w, 400, "invalid_request", "is_system_admin is required")
		return
	}
	if err := s.system.SetAdmin(r.Context(), identity.ActorID(r.Context()), id, *input.Admin, false); err != nil {
		systemError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) importShared(w http.ResponseWriter, r *http.Request) {
	if err := s.system.ImportShared(r.Context(), identity.ActorID(r.Context())); err != nil {
		systemError(w, err)
		return
	}
	w.WriteHeader(204)
}
