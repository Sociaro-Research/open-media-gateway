package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/google/uuid"
	"net/http"
	"slices"
	"strings"
)

type credentialOrganizationKey struct{}
type globalOrganizationKey struct{}

func credentialOrganization(ctx context.Context) string {
	global, _ := ctx.Value(globalOrganizationKey{}).(bool)
	if global {
		return ""
	}
	id, _ := ctx.Value(credentialOrganizationKey{}).(string)
	return id
}
func (s *Server) organizationStore() org.Repository { r, _ := s.system.(org.Repository); return r }
func orgError(w http.ResponseWriter, e error) {
	var duplicate *org.Duplicate
	if errors.As(e, &duplicate) {
		onboardingError(w, e)
		return
	}
	switch {
	case errors.Is(e, org.ErrForbidden):
		writeError(w, 403, "forbidden", e.Error())
	case errors.Is(e, org.ErrRecipient):
		writeError(w, 403, "invitation_recipient", e.Error())
	case errors.Is(e, org.ErrNotFound):
		writeError(w, 404, "not_found", e.Error())
	case errors.Is(e, org.ErrInvalid):
		writeError(w, 400, "invalid_request", e.Error())
	case errors.Is(e, org.ErrInvitationUnavailable):
		writeError(w, 409, "invitation_unavailable", e.Error())
	case errors.Is(e, org.ErrConflict):
		writeError(w, 409, "conflict", e.Error())
	default:
		systemError(w, e)
	}
}
func (s *Server) registerOrganizations(mux *http.ServeMux) {
	mux.Handle("GET /v1/organizations/{org}/support", s.authenticate(http.HandlerFunc(s.organizationSupport)))
	mux.Handle("GET /v1/client-onboardings", s.authenticate(http.HandlerFunc(s.clientOnboarding)))
	mux.Handle("POST /v1/client-onboardings", s.authenticate(http.HandlerFunc(s.clientOnboarding)))
	mux.Handle("POST /v1/client-onboardings/{client}/{action}", s.authenticate(http.HandlerFunc(s.clientOnboarding)))
	mux.Handle("GET /v1/organizations/{org}/limits", s.authenticate(http.HandlerFunc(s.limitSettings)))
	mux.Handle("PUT /v1/organizations/{org}/limits", s.authenticate(http.HandlerFunc(s.limitSettings)))
	mux.Handle("GET /v1/organizations/{org}/sales-permissions", s.authenticate(http.HandlerFunc(s.organizationSalesPermissions)))
	mux.Handle("GET /v1/organizations/{org}/markups/catalog", s.authenticate(http.HandlerFunc(s.markupCatalog)))
	mux.Handle("GET /v1/organizations/{org}/markups", s.authenticate(http.HandlerFunc(s.markupSettings)))
	mux.Handle("PUT /v1/organizations/{org}/markups", s.authenticate(http.HandlerFunc(s.markupSettings)))
	mux.Handle("GET /v1/organizations/{org}/storage", s.authenticate(http.HandlerFunc(s.organizationStorage)))
	mux.Handle("PUT /v1/organizations/{org}/storage", s.authenticate(http.HandlerFunc(s.organizationStorage)))
	mux.Handle("POST /v1/organizations/{org}/storage/check", s.authenticate(http.HandlerFunc(s.organizationStorage)))
	mux.Handle("GET /v1/organizations", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("POST /v1/organizations", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("GET /v1/organizations/{org}", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("PUT /v1/organizations/{org}", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("PUT /v1/organizations/{org}/request-logging", s.authenticate(http.HandlerFunc(s.organizationRequestLogging)))
	mux.Handle("PUT /v1/organizations/{org}/client-manager", s.authenticate(http.HandlerFunc(s.assignClientManager)))
	mux.Handle("GET /v1/organizations/{org}/members", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("PUT /v1/organizations/{org}/members/{user}", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("DELETE /v1/organizations/{org}/members/{user}", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("GET /v1/organizations/{org}/balance-history", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("GET /v1/organizations/{org}/credits", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("POST /v1/organizations/{org}/credits", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("GET /v1/organizations/{org}/invitations", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("POST /v1/organizations/{org}/invitations", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("DELETE /v1/organizations/{org}/invitations/{invite}", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("GET /v1/organizations/{org}/reports/{id}", s.authenticate(http.HandlerFunc(s.reportDetail)))
	mux.Handle("GET /v1/organizations/{org}/reports/{id}/content", s.authenticate(http.HandlerFunc(s.reportArtifact)))
	mux.Handle("HEAD /v1/organizations/{org}/reports/{id}/content", s.authenticate(http.HandlerFunc(s.reportArtifact)))
	mux.Handle("GET /v1/organizations/{org}/reports/{id}/last-frame", s.authenticate(http.HandlerFunc(s.reportArtifact)))
	mux.Handle("HEAD /v1/organizations/{org}/reports/{id}/last-frame", s.authenticate(http.HandlerFunc(s.reportArtifact)))
	mux.Handle("GET /v1/organizations/{org}/reports", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("GET /v1/organizations/{org}/generations/{id}", s.authenticate(http.HandlerFunc(s.getGeneration)))
	mux.Handle("GET /v1/organizations/{org}/generations/{id}/media-access", s.authenticate(http.HandlerFunc(s.mediaAccess)))
	mux.Handle("GET /v1/organizations/{org}/generations/{id}/content", s.authenticate(http.HandlerFunc(s.generationContent)))
	mux.Handle("HEAD /v1/organizations/{org}/generations/{id}/content", s.authenticate(http.HandlerFunc(s.generationContent)))
	mux.Handle("HEAD /v1/organizations/{org}/generations/{id}/last-frame", s.authenticate(http.HandlerFunc(s.serveAuthorizedLastFrame)))
	mux.Handle("GET /v1/organizations/{org}/generations/{id}/last-frame", s.authenticate(http.HandlerFunc(s.serveAuthorizedLastFrame)))
	mux.Handle("DELETE /v1/organizations/{org}/generations/{id}/content", s.authenticate(http.HandlerFunc(s.deleteResult)))
	mux.Handle("GET /v1/organizations/{org}/generations", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("POST /v1/invitations/preview", s.authenticate(http.HandlerFunc(s.organizations)))
	mux.Handle("POST /v1/invitations/accept", s.authenticate(http.HandlerFunc(s.organizations)))

}

func (s *Server) organizationRequestLogging(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("org")
	if _, err := uuid.Parse(id); err != nil {
		orgError(w, org.ErrInvalid)
		return
	}
	if bound := credentialOrganization(r.Context()); bound != "" && bound != id {
		orgError(w, org.ErrForbidden)
		return
	}
	var input struct {
		Minimize *bool `json:"minimize_request_logging"`
	}
	if !decodeSystem(w, r, &input) {
		return
	}
	if input.Minimize == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "minimize_request_logging is required")
		return
	}
	repo, ok := s.system.(interface {
		SetOrganizationRequestLogging(context.Context, string, string, bool) error
	})
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "organization request logging is unavailable")
		return
	}
	if err := repo.SetOrganizationRequestLogging(r.Context(), identity.ActorID(r.Context()), id, *input.Minimize); err != nil {
		orgError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) organizations(w http.ResponseWriter, r *http.Request) {
	repo := s.organizationStore()
	if repo == nil {
		writeError(w, 503, "unavailable", "organizations are unavailable")
		return
	}
	actor := identity.ActorID(r.Context())
	if actor == identity.SystemOwnerID {
		writeError(w, 403, "account_required", "a user account is required")
		return
	}
	id := r.PathValue("org")
	if bound := credentialOrganization(r.Context()); bound != "" && id != "" && id != bound {
		orgError(w, org.ErrForbidden)
		return
	}
	if id != "" {
		if _, e := uuid.Parse(id); e != nil {
			orgError(w, org.ErrInvalid)
			return
		}
	}
	// Financial/system actions require both the role and the API permission, when a key is used.
	systemAdmin, e := s.systemAdmin(r.Context())
	if e != nil {
		systemError(w, e)
		return
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/v1/invitations/") {
		var input struct {
			Token string `json:"token"`
		}
		if !decodeSystem(w, r, &input) {
			return
		}
		i, e := repo.OrganizationInvitation(r.Context(), actor, input.Token, strings.HasSuffix(path, "/accept"))
		if e != nil {
			orgError(w, e)
			return
		}
		s.writeViewerJSON(w, r, 200, i)
		return
	}
	if id == "" {
		if readMethod(r.Method) {
			all := r.URL.Query().Get("all") == "true"
			if all && !systemAdmin {
				orgError(w, org.ErrForbidden)
				return
			}
			items, asOf, e := organizationList(r, repo, actor, all)
			if e != nil {
				orgError(w, e)
				return
			}
			if bound := credentialOrganization(r.Context()); bound != "" {
				items = slices.DeleteFunc(items, func(o org.Organization) bool { return o.ID != bound })
			}
			response := map[string]any{"data": items}
			if !asOf.IsZero() {
				response["as_of"] = asOf
			}
			s.writeViewerJSON(w, r, 200, response)
			return
		}
		if !systemAdmin {
			orgError(w, org.ErrForbidden)
			return
		}
		var input struct {
			Name   string `json:"name"`
			Admin  string `json:"admin_user"`
			Domain string `json:"domain"`
		}
		if !decodeSystem(w, r, &input) {
			return
		}
		o, e := repo.CreateOrganization(r.Context(), actor, input.Name, input.Admin, input.Domain)
		if e != nil {
			orgError(w, e)
			return
		}
		s.writeViewerJSON(w, r, 201, o)
		return
	}
	role, e := repo.OrganizationAccess(r.Context(), actor, id)
	if e != nil {
		orgError(w, e)
		return
	}
	switch {
	case strings.HasSuffix(path, "/reports") || strings.HasSuffix(path, "/generations"):
		ctx := identity.WithOrganizationRole(r.Context(), id, role)
		r = r.WithContext(ctx)
		if strings.HasSuffix(path, "/reports") {
			s.costReport(w, r)
		} else {
			s.listGenerations(w, r)
		}
		return
	case strings.Contains(path, "/members"):
		if readMethod(r.Method) {
			m, e := repo.OrganizationMembers(r.Context(), actor, id)
			if e != nil {
				orgError(w, e)
				return
			}
			s.writeViewerJSON(w, r, 200, map[string]any{"data": m})
			return
		}
		var input struct {
			Role        string          `json:"role"`
			WeeklyLimit json.RawMessage `json:"weekly_limit_usd"`
		}
		if r.Method == "DELETE" {
			input.Role = "member"
		}
		if r.Method != "DELETE" && !decodeSystem(w, r, &input) {
			return
		}
		var weekly *money.USD
		limitProvided := len(input.WeeklyLimit) != 0
		if limitProvided && json.Unmarshal(input.WeeklyLimit, &weekly) != nil {
			writeError(w, 400, "invalid_request", "weekly_limit_usd must be a number or null for unlimited")
			return
		}
		e = repo.SetOrganizationMember(r.Context(), actor, id, r.PathValue("user"), input.Role, weekly, limitProvided, r.Method == "DELETE")
	case strings.HasSuffix(path, "/balance-history"):
		history, ok := repo.(payments.HistoryRepository)
		if !ok {
			paymentError(w, payments.ErrUnavailable)
			return
		}
		offset, ok := systemPage(w, r)
		if !ok {
			return
		}
		entries, err := history.BalanceHistory(r.Context(), actor, id, offset)
		if err != nil {
			orgError(w, err)
			return
		}
		more := len(entries) > 50
		if more {
			entries = entries[:50]
		}
		s.writeViewerJSON(w, r, 200, map[string]any{"data": entries, "has_more": more})
		return
	case strings.HasSuffix(path, "/credits"):
		if readMethod(r.Method) {
			offset, ok := systemPage(w, r)
			if !ok {
				return
			}
			credits, e := repo.OrganizationCredits(r.Context(), actor, id, offset)
			if e != nil {
				orgError(w, e)
				return
			}
			more := len(credits) > 50
			if more {
				credits = credits[:50]
			}
			s.writeViewerJSON(w, r, 200, map[string]any{"data": credits, "has_more": more})
			return
		}
		if !systemAdmin && !s.requireCommercialPermission(w, r, "topup") {
			return
		}
		var input struct {
			Amount     money.USD `json:"amount_usd"`
			Kind       string    `json:"kind"`
			Reason     string    `json:"reason"`
			ExternalID string    `json:"external_id"`
		}
		if !decodeSystem(w, r, &input) {
			return
		}
		c, e := repo.AddOrganizationCredit(r.Context(), actor, id, org.Credit{Amount: input.Amount, Kind: input.Kind, Reason: input.Reason, ExternalID: input.ExternalID})
		if e != nil {
			orgError(w, e)
			return
		}
		s.writeViewerJSON(w, r, 201, c)
		return
	case strings.Contains(path, "/invitations"):
		if readMethod(r.Method) {
			items, e := repo.OrganizationInvites(r.Context(), actor, id)
			if e != nil {
				orgError(w, e)
				return
			}
			s.writeViewerJSON(w, r, 200, map[string]any{"data": items})
			return
		}
		if r.Method == "POST" {
			var input struct {
				Role *string `json:"role"`
			}
			if r.Body != nil && r.ContentLength != 0 && !decodeSystem(w, r, &input) {
				return
			}
			role := org.MemberRole
			if input.Role != nil {
				role = *input.Role
			}
			if !org.ValidRole(role) {
				orgError(w, org.ErrInvalid)
				return
			}
			i, e := repo.CreateOrganizationInvite(r.Context(), actor, id, role)
			if e != nil {
				orgError(w, e)
				return
			}
			s.writeViewerJSON(w, r, 201, i)
			return
		}
		invite := r.PathValue("invite")
		if _, err := uuid.Parse(invite); err != nil {
			orgError(w, org.ErrInvalid)
			return
		}
		e = repo.RevokeOrganizationInvite(r.Context(), actor, id, invite)
	default:
		if readMethod(r.Method) {
			items, e := repo.Organizations(r.Context(), actor, systemAdmin)
			if e != nil {
				orgError(w, e)
				return
			}
			for _, o := range items {
				if o.ID == id {
					s.writeViewerJSON(w, r, 200, o)
					return
				}
			}
			orgError(w, org.ErrNotFound)
			return
		}
		var input struct {
			Name               string          `json:"name"`
			MarkupPercent      json.RawMessage `json:"markup_percent"`
			DefaultWeeklyLimit json.RawMessage `json:"default_weekly_limit_usd"`
			CreditLimit        *money.USD      `json:"credit_limit_usd"`
		}
		if !decodeSystem(w, r, &input) {
			return
		}
		var credit money.USD
		if input.CreditLimit != nil {
			if !systemAdmin {
				orgError(w, org.ErrForbidden)
				return
			}
			credit = *input.CreditLimit
		}
		var weekly *money.USD
		if len(input.DefaultWeeklyLimit) == 0 || json.Unmarshal(input.DefaultWeeklyLimit, &weekly) != nil {
			writeError(w, 400, "invalid_request", "default_weekly_limit_usd is required; use null for unlimited")
			return
		}
		var markup []*money.Percent
		if len(input.MarkupPercent) > 0 {
			if !systemAdmin {
				orgError(w, org.ErrForbidden)
				return
			}
			var value *money.Percent
			if json.Unmarshal(input.MarkupPercent, &value) != nil {
				writeError(w, 400, "invalid_request", "markup_percent must be 0–1000 with at most two decimals, or null to inherit")
				return
			}
			markup = append(markup, value)
		}
		e = repo.OrganizationSettings(r.Context(), actor, id, input.Name, credit, weekly, input.CreditLimit != nil, markup...)
	}
	if e != nil {
		orgError(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) keyOrganization(ctx context.Context, actor, id string) (bool, error) {
	repo := s.organizationStore()
	if repo == nil || id == "" {
		return true, nil
	}
	if _, e := uuid.Parse(id); e != nil {
		return false, org.ErrInvalid
	}
	// Even a system administrator must explicitly be a member to issue a consuming key.
	items, e := repo.Organizations(ctx, actor, false)
	if e != nil {
		return false, e
	}
	for _, o := range items {
		if o.ID == id && org.ValidRole(o.Role) {
			return true, nil
		}
	}
	return false, org.ErrForbidden
}
