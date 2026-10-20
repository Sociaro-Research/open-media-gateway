package api

import (
	"context"
	"encoding/json"
	"errors"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/limits"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
)

type apiKeyScopesContextKey struct{}

// currentAPIKey exposes only the calling credential, including for generation-only keys.
func (s *Server) currentAPIKey(w http.ResponseWriter, r *http.Request) {
	id := identity.APIKeyID(r.Context())
	if id == nil {
		s.writeViewerJSON(w, r, http.StatusOK, map[string]any{"key": nil, "usage": nil})
		return
	}
	owner := identity.ActorID(r.Context())
	keys, err := s.keys.List(r.Context(), owner)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API key could not be read")
		return
	}
	usage, err := s.keys.Usage(r.Context(), owner, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API key usage could not be read")
		return
	}
	for _, key := range keys {
		if key.ID != *id {
			continue
		}
		var current *apikey.MonthlyUsage
		for i := range usage {
			if usage[i].KeyID == *id {
				current = &usage[i]
				break
			}
		}
		response := map[string]any{"key": key, "usage": current}
		if key.OrganizationID != "" && s.organizationStore() != nil {
			items, e := s.organizationStore().Organizations(r.Context(), owner, false)
			if e != nil {
				orgError(w, e)
				return
			}
			for _, o := range items {
				if o.ID == key.OrganizationID {
					response["organization"] = o
					if current != nil && !org.Statistics(o.Role) && key.ResourceMode == "shared" {
						current.HideSettlement = true
					}
				}
			}
		}
		s.writeViewerJSON(w, r, http.StatusOK, response)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "API key not found")
}

func personalOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner := identity.ActorID(r.Context())
	if owner == identity.SystemOwnerID {
		writeError(w, http.StatusForbidden, "personal_account_required", "sign in to create your first personal API key")
		return "", false
	}
	return owner, true
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	owner, ok := personalOwner(w, r)
	if !ok {
		return
	}
	keys, err := s.keys.List(r.Context(), owner)
	organizationID := r.URL.Query().Get("organization_id")
	if organizationID != "" {
		if _, e := uuid.Parse(organizationID); e != nil {
			orgError(w, org.ErrInvalid)
			return
		}
	}
	if organizationID == "" {
		organizationID = credentialOrganization(r.Context())
	}
	if organizationID != "" {
		if bound := credentialOrganization(r.Context()); bound != "" && bound != organizationID {
			orgError(w, org.ErrForbidden)
			return
		}
		if repo, ok := s.keys.(interface {
			OrganizationKeys(context.Context, string, string) ([]apikey.Key, error)
		}); ok {
			keys, err = repo.OrganizationKeys(r.Context(), owner, organizationID)
		}
	}
	if err != nil {
		if errors.Is(err, apikey.ErrForbidden) {
			orgError(w, org.ErrForbidden)
			return
		}
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API keys could not be read")
		return
	}
	if bound := credentialOrganization(r.Context()); bound != "" {
		keys = slices.DeleteFunc(keys, func(k apikey.Key) bool { return k.OrganizationID != bound })
	}
	s.writeViewerJSON(w, r, http.StatusOK, map[string]any{"data": keys})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	owner, ok := personalOwner(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	defer r.Body.Close()
	var input apikey.CreateInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "send one JSON object with name, scopes and optional expires_at")
		return
	}
	if bound := credentialOrganization(r.Context()); bound != "" && input.OrganizationID != bound {
		writeError(w, 403, "organization_mismatch", "the calling key cannot issue keys for another organization")
		return
	}
	if input.UserID != "" {
		if _, err := uuid.Parse(input.UserID); err != nil {
			orgError(w, org.ErrInvalid)
			return
		}
	}
	if input.OrganizationID == "" && input.UserID != "" && input.UserID != owner {
		orgError(w, org.ErrForbidden)
		return
	}
	if input.OrganizationID != "" && s.organizationStore() != nil {
		role, e := s.organizationStore().OrganizationAccess(r.Context(), owner, input.OrganizationID)
		if e != nil {
			orgError(w, e)
			return
		}
		recipient := input.UserID
		if recipient == "" {
			recipient = owner
		}
		targetRole := role
		if recipient != owner {
			members, e := s.organizationStore().OrganizationMembers(r.Context(), owner, input.OrganizationID)
			if e != nil {
				orgError(w, e)
				return
			}
			targetRole = ""
			for _, m := range members {
				if m.ID == recipient {
					targetRole = m.Role
				}
			}
		}
		if !org.IssueKey(role, owner, targetRole, recipient) {
			orgError(w, org.ErrForbidden)
			return
		}
	}
	recipient := owner
	if input.UserID != "" {
		recipient = input.UserID
	}
	if ok, e := s.keyOrganization(r.Context(), recipient, input.OrganizationID); e != nil || !ok {
		if e == nil {
			e = system.ErrForbidden
		}
		orgError(w, e)
		return
	}
	if s.organizationStore() != nil {
		mode := "personal"
		if input.OrganizationID != "" {
			mode = "shared"
		}
		if input.ResourceMode != "" && input.ResourceMode != mode {
			writeError(w, 400, "invalid_request", "organization keys use shared providers; personal keys omit organization_id")
			return
		}
		input.ResourceMode = mode
		input.StorageMode = mode
	}
	if settings, ok := r.Context().Value(systemSettingsKey{}).(system.Settings); ok {
		if input.ResourceMode == "" {
			if settings.SharedResources {
				input.ResourceMode = "shared"
			} else {
				input.ResourceMode = "personal"
			}
		}
		if input.ResourceMode == "personal" && !settings.PersonalProviders {
			admin, err := s.system.IsAdmin(r.Context(), owner)
			if err != nil {
				systemError(w, err)
				return
			}
			if !admin {
				writeError(w, 403, "personal_providers_disabled", "personal provider access is disabled; use shared providers when enabled")
				return
			}
		}
		if input.StorageMode == "" {
			input.StorageMode = input.ResourceMode
		}
		if !settings.SharedResources && input.ResourceMode == "shared" {
			writeError(w, 403, "shared_resources_disabled", "shared providers and storage are disabled")
			return
		}
	}
	if slices.Contains(input.Scopes, apikey.SystemManage) {
		admin, err := s.systemAdmin(r.Context())
		if err != nil {
			systemError(w, err)
			return
		}
		if !admin {
			systemError(w, system.ErrForbidden)
			return
		}
	}
	key, secret, err := apikey.Generate(input, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if parentScopes, ok := r.Context().Value(apiKeyScopesContextKey{}).([]string); ok {
		for _, scope := range key.Scopes {
			if !slices.Contains(parentScopes, scope) {
				writeError(w, http.StatusForbidden, "insufficient_scope", "cannot grant permissions the calling key does not have")
				return
			}
		}
	}
	key.CreatedBy = owner
	if key.UserID == "" {
		key.UserID = owner
	}
	if !s.checkLimits(w, r, input.OrganizationID, "key_create") {
		return
	}
	if err := s.keys.Create(r.Context(), owner, key, apikey.Hash(secret)); err != nil {
		if errors.Is(err, apikey.ErrForbidden) {
			orgError(w, org.ErrForbidden)
		} else if errors.Is(err, limits.ErrActiveKeys) {
			limitError(w, err)
		} else {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API key could not be created")
		}
		return
	}
	s.writeViewerJSON(w, r, http.StatusCreated, struct {
		apikey.Key
		Secret string `json:"secret"`
	}{Key: key, Secret: secret})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	owner, ok := personalOwner(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	if !s.keyWithinOrganization(w, r, owner, id) {
		return
	}
	var err error
	if repo, ok := s.keys.(interface {
		RevokeForActor(context.Context, string, string) error
	}); ok {
		err = repo.RevokeForActor(r.Context(), owner, id)
	} else {
		err = s.keys.Revoke(r.Context(), owner, id)
	}
	if errors.Is(err, apikey.ErrForbidden) {
		orgError(w, org.ErrForbidden)
		return
	}
	if errors.Is(err, apikey.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API key could not be revoked")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiKeyUsage(w http.ResponseWriter, r *http.Request) {
	owner, ok := personalOwner(w, r)
	if !ok {
		return
	}
	location := time.UTC
	if zone := r.URL.Query().Get("timezone"); zone != "" {
		var err error
		location, err = time.LoadLocation(zone)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid timezone")
			return
		}
	}
	month := time.Now().In(location)
	if value := r.URL.Query().Get("month"); value != "" {
		parsed, err := time.ParseInLocation("2006-01", value, location)
		if err != nil || parsed.Year() < 2000 {
			writeError(w, http.StatusBadRequest, "invalid_request", "month must use YYYY-MM, year 2000 or later")
			return
		}
		month = parsed
	}
	usage, err := s.keys.Usage(r.Context(), owner, month)
	organizationID := r.URL.Query().Get("organization_id")
	if organizationID == "" {
		organizationID = credentialOrganization(r.Context())
	}
	if organizationID != "" {
		if bound := credentialOrganization(r.Context()); bound != "" && bound != organizationID {
			orgError(w, org.ErrForbidden)
			return
		}
		if _, e := uuid.Parse(organizationID); e != nil {
			orgError(w, org.ErrInvalid)
			return
		}
		if repo, ok := s.keys.(interface {
			OrganizationUsage(context.Context, string, string, time.Time) ([]apikey.MonthlyUsage, error)
		}); ok {
			usage, err = repo.OrganizationUsage(r.Context(), owner, organizationID, month)
		}
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API key usage could not be read")
		return
	}
	if bound := credentialOrganization(r.Context()); bound != "" {
		keys, e := s.keys.List(r.Context(), owner)
		if repo, ok := s.keys.(interface {
			OrganizationKeys(context.Context, string, string) ([]apikey.Key, error)
		}); ok {
			keys, e = repo.OrganizationKeys(r.Context(), owner, bound)
		}
		if e != nil {
			systemError(w, e)
			return
		}
		allowed := map[string]bool{}
		for _, k := range keys {
			if k.OrganizationID == bound {
				allowed[k.ID] = true
			}
		}
		usage = slices.DeleteFunc(usage, func(u apikey.MonthlyUsage) bool { return !allowed[u.KeyID] })
	}
	if repo := s.organizationStore(); repo != nil {
		organizations, e := repo.Organizations(r.Context(), owner, false)
		if e != nil {
			orgError(w, e)
			return
		}
		keys, e := s.keys.List(r.Context(), owner)
		if e != nil {
			systemError(w, e)
			return
		}
		roles := map[string]string{}
		for _, o := range organizations {
			roles[o.ID] = o.Role
		}
		hidden := map[string]bool{}
		for _, k := range keys {
			hidden[k.ID] = k.ResourceMode == "shared" && !org.Statistics(roles[k.OrganizationID])
		}
		for i := range usage {
			usage[i].HideSettlement = hidden[usage[i].KeyID]
		}
	}
	s.writeViewerJSON(w, r, http.StatusOK, map[string]any{"data": usage, "month": month.Format("2006-01"), "currency": "USD"})
}

func (s *Server) keyWithinOrganization(w http.ResponseWriter, r *http.Request, owner, id string) bool {
	bound := credentialOrganization(r.Context())
	if bound == "" {
		return true
	}
	keys, e := s.keys.List(r.Context(), owner)
	if repo, ok := s.keys.(interface {
		OrganizationKeys(context.Context, string, string) ([]apikey.Key, error)
	}); ok {
		keys, e = repo.OrganizationKeys(r.Context(), owner, bound)
	}
	if e != nil {
		systemError(w, e)
		return false
	}
	for _, k := range keys {
		if k.ID == id && k.OrganizationID == bound {
			return true
		}
	}
	writeError(w, 404, "not_found", "API key not found")
	return false
}
