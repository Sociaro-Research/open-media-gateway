package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func (s *Server) isClientManager(ctx context.Context) (bool, error) {
	repo, ok := s.system.(system.ClientManagers)
	if !ok {
		return false, nil
	}
	return repo.IsClientManager(ctx, identity.ActorID(ctx))
}

// A restrictive allowlist applies to every authenticated endpoint, including old keys.
// OrganizationAccess separately checks the current assignment on each read.
func (s *Server) clientManagerPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manager, err := s.isClientManager(r.Context())
		if err != nil {
			systemError(w, err)
			return
		}
		if manager {
			path := r.URL.Path
			allowed := strings.HasPrefix(path, "/provider-assets/") || path == "/v1/me" || path == "/v1/info" || path == "/v1/organizations" || path == "/v1/sales/team"
			parts := strings.Split(strings.Trim(path, "/"), "/")
			if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "organizations" {
				allowed = len(parts) == 3 || len(parts) == 4 && (parts[3] == "members" || parts[3] == "credits" || parts[3] == "balance-history" || parts[3] == "reports") || len(parts) == 5 && parts[3] == "reports"
			}
			onboarding := path == "/v1/client-onboardings" && (r.Method == "GET" || r.Method == "POST") || len(parts) == 4 && parts[0] == "v1" && parts[1] == "client-onboardings" && (parts[3] == "resend" || parts[3] == "revoke") && r.Method == "POST"
			transfer := len(parts) == 4 && parts[0] == "v1" && parts[1] == "organizations" && parts[3] == "client-manager" && r.Method == "PUT" && identity.APIKeyID(r.Context()) == nil
			if transfer {
				if repo, ok := s.system.(system.SalesRepository); ok {
					role, e := repo.SalesRole(r.Context(), identity.ActorID(r.Context()))
					if e != nil {
						systemError(w, e)
						return
					}
					transfer = system.SalesRank(role) > 1
				} else {
					transfer = false
				}
			}
			commercial := false
			if identity.APIKeyID(r.Context()) == nil {
				commercial = path == "/v1/sales/bonuses" && r.Method == "GET" || len(parts) == 5 && parts[0] == "v1" && parts[1] == "sales" && parts[2] == "bonuses" && (parts[4] == "entries" && r.Method == "GET" || parts[4] == "payouts" && r.Method == "POST") || path == "/v1/sales/permissions" && r.Method == "GET" || len(parts) == 5 && parts[0] == "v1" && parts[1] == "sales" && parts[2] == "permissions" && r.Method == "PUT"
				if len(parts) >= 4 && parts[0] == "v1" && parts[1] == "organizations" {
					commercial = len(parts) == 4 && ((parts[3] == "sales-permissions" && r.Method == "GET") || (parts[3] == "markups" || parts[3] == "model-access") && (r.Method == "GET" || r.Method == "PUT") || parts[3] == "credits" && r.Method == "POST") || len(parts) == 5 && parts[3] == "markups" && parts[4] == "catalog" && r.Method == "GET"
				}
			}
			if !commercial && (r.Method != "GET" || !allowed) && !onboarding && !transfer && !(r.Method == "PATCH" && path == "/v1/me") {
				writeError(w, 403, "client_manager_read_only", "client managers can onboard companies and read statistics for their assigned organizations")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) assignClientManager(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		repo, ok := s.system.(system.SalesRepository)
		if !ok || identity.APIKeyID(r.Context()) != nil {
			systemError(w, system.ErrForbidden)
			return
		}
		role, e := repo.SalesRole(r.Context(), identity.ActorID(r.Context()))
		if e != nil {
			systemError(w, e)
			return
		}
		if system.SalesRank(role) < 2 {
			systemError(w, system.ErrForbidden)
			return
		}
	}
	if bound := credentialOrganization(r.Context()); bound != "" && bound != r.PathValue("org") {
		systemError(w, system.ErrForbidden)
		return
	}
	if _, err = uuid.Parse(r.PathValue("org")); err != nil {
		writeError(w, 400, "invalid_request", "invalid organization ID")
		return
	}
	var input struct {
		User *string `json:"user"`
	}
	if !decodeSystem(w, r, &input) {
		return
	}
	if input.User == nil {
		writeError(w, 400, "invalid_request", "user is required; use an empty string to clear the assignment")
		return
	}
	repo, ok := s.system.(system.ClientManagers)
	if !ok {
		writeError(w, 503, "unavailable", "client managers are unavailable")
		return
	}
	if err = repo.AssignClientManager(r.Context(), identity.ActorID(r.Context()), r.PathValue("org"), *input.User); err != nil {
		orgError(w, err)
		return
	}
	w.WriteHeader(204)
}
