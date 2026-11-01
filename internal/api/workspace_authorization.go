package api

import (
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/limits"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

func workspaceAuthorizationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, wa.ErrLegacy):
		writeError(w, 409, "installation_upgrade_required", wa.ErrLegacy.Error())
	case errors.Is(err, wa.ErrKeyLimit) || errors.Is(err, limits.ErrActiveKeys):
		writeError(w, 409, "active_key_limit", wa.ErrKeyLimit.Error())
	case errors.Is(err, wa.ErrPending):
		writeError(w, 428, "authorization_pending", "Complete sign-in and explicit authorization in the trusted server window")
	case errors.Is(err, wa.ErrSlow):
		w.Header().Set("Retry-After", "10")
		writeError(w, 429, "slow_down", "Wait before checking this authorization again")
	case errors.Is(err, wa.ErrDisabled):
		writeError(w, 403, "workspace_onboarding_disabled", err.Error())
	case errors.Is(err, wa.ErrForbidden):
		writeError(w, 403, "workspace_authorization_forbidden", err.Error())
	case errors.Is(err, wa.ErrUnavailable):
		writeError(w, 410, "workspace_authorization_expired", err.Error())
	case errors.Is(err, wa.ErrConflict):
		writeError(w, 409, "workspace_authorization_changed", err.Error())
	default:
		writeError(w, 400, "workspace_authorization_invalid", "Check installation identity, customer membership and host availability; no credit or access is granted on failure")
	}
}
func (s *Server) workspaceThrottle(w http.ResponseWriter, r *http.Request, operation string, limit int) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.workspaceAuthorization == nil {
		writeError(w, 503, "workspace_onboarding_unavailable", "Workspace authorization is not configured")
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	allowed, err := s.workspaceAuthorization.Repo.AllowAttempt(r.Context(), wa.Digest("workspace:"+operation+":"+host+":"+identity.ActorID(r.Context())), limit, time.Minute)
	if err != nil {
		writeError(w, 503, "workspace_onboarding_unavailable", "Authorization limits could not be checked")
		return false
	}
	if !allowed {
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "rate_limit_exceeded", "Too many installation authorization requests")
		return false
	}
	return true
}
func (s *Server) startWorkspaceAuthorization(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceThrottle(w, r, "start", 30) {
		return
	}
	var in struct {
		Device            string `json:"device_code"`
		Name              string `json:"client_name"`
		Origin            string `json:"client_origin"`
		Replace           string `json:"replace_key_id"`
		InstallationProof string `json:"installation_proof"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	u, err := url.Parse(in.Origin)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		workspaceAuthorizationError(w, wa.ErrUnavailable)
		return
	}
	if in.Replace != "" {
		if _, err = uuid.Parse(in.Replace); err != nil {
			workspaceAuthorizationError(w, wa.ErrUnavailable)
			return
		}
	}
	challenge, err := s.workspaceAuthorization.Start(r.Context(), in.Device, wa.Challenge{Label: strings.TrimSpace(in.Name), Origin: strings.TrimRight(u.String(), "/"), ReplaceKey: in.Replace, InstallationProof: in.InstallationProof})
	if err != nil {
		workspaceAuthorizationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"user_code": challenge.Code, "expires_at": challenge.Expires, "interval": 5})
}
func (s *Server) pollWorkspaceAuthorization(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceThrottle(w, r, "poll", 200) {
		return
	}
	var in struct {
		Device string `json:"device_code"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	challenge, token, err := s.workspaceAuthorization.Poll(r.Context(), in.Device)
	if err != nil {
		workspaceAuthorizationError(w, err)
		return
	}
	// Only the holder of the high-entropy device secret receives the customer key.
	mode := "legacy"
	if len(challenge.InstallationHash) == 32 {
		mode = "dedicated"
	}
	writeJSON(w, 200, map[string]any{"token": token, "organization_id": challenge.Company, "key_id": challenge.KeyID, "provisioning": mode, "installation_id": hex.EncodeToString(challenge.InstallationHash)})
}
func (s *Server) cancelWorkspaceAuthorization(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceThrottle(w, r, "cancel", 30) {
		return
	}
	var in struct {
		Device string `json:"device_code"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	if !wa.ValidDevice(in.Device) {
		workspaceAuthorizationError(w, wa.ErrUnavailable)
		return
	}
	if err := s.workspaceAuthorization.Repo.Cancel(r.Context(), wa.Digest(in.Device)); err != nil {
		workspaceAuthorizationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"cancelled": true})
}
func browserWorkspaceAccount(w http.ResponseWriter, r *http.Request) bool {
	if len(r.Header.Values("Authorization")) > 0 || identity.APIKeyID(r.Context()) != nil || identity.ActorID(r.Context()) == identity.SystemOwnerID {
		workspaceAuthorizationError(w, wa.ErrForbidden)
		return false
	}
	return true
}
func (s *Server) inspectWorkspaceAuthorization(w http.ResponseWriter, r *http.Request) {
	if !browserWorkspaceAccount(w, r) || !s.workspaceThrottle(w, r, "inspect", 30) {
		return
	}
	var in struct {
		Code string `json:"user_code"`
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	challenge, companies, create, err := s.workspaceAuthorization.Repo.Describe(r.Context(), identity.ActorID(r.Context()), in.Code)
	if err != nil {
		workspaceAuthorizationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"request": challenge, "companies": companies, "can_create": create})
}
func (s *Server) approveWorkspaceAuthorization(w http.ResponseWriter, r *http.Request) {
	if !browserWorkspaceAccount(w, r) || !s.workspaceThrottle(w, r, "approve", 30) {
		return
	}
	var in struct {
		Code string `json:"user_code"`
		wa.Approval
	}
	if !decodeSystem(w, r, &in) {
		return
	}
	challenge, err := s.workspaceAuthorization.Repo.Approve(r.Context(), identity.ActorID(r.Context()), in.Code, in.Approval)
	if err != nil {
		workspaceAuthorizationError(w, err)
		return
	}
	writeJSON(w, 200, challenge)
}
func (s *Server) workspaceInstallations(w http.ResponseWriter, r *http.Request) {
	if !browserWorkspaceAccount(w, r) || !s.workspaceThrottle(w, r, "installations", 60) {
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.workspaceAuthorization.Repo.Revoke(r.Context(), identity.ActorID(r.Context()), r.PathValue("id")); err != nil {
			workspaceAuthorizationError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"revoked": true})
		return
	}
	grants, err := s.workspaceAuthorization.Repo.Grants(r.Context(), identity.ActorID(r.Context()))
	if err != nil {
		workspaceAuthorizationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": grants})
}
