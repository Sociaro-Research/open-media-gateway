package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

func (s *Server) registerCompanyInterfaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/company-interface/v1/capabilities", s.companyInterfaceCapabilities)
	mux.HandleFunc("POST /auth/company-interface/v1/start", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "start") })
	mux.HandleFunc("POST /auth/company-interface/v1/redeem", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "redeem") })
	mux.HandleFunc("POST /auth/company-interface/v1/refresh", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "refresh") })
	mux.HandleFunc("POST /auth/company-interface/v1/cancel", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "cancel") })
	mux.HandleFunc("POST /auth/company-interface/v1/reconcile", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "reconcile") })
	mux.HandleFunc("POST /auth/company-interface/v1/recover-installation", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "recover-installation") })
	mux.HandleFunc("POST /auth/company-interface/v1/revoke-family", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "revoke-family") })
	mux.HandleFunc("POST /auth/company-interface/v1/revoke-issued-family", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "revoke-issued-family") })
	mux.HandleFunc("POST /auth/company-interface/v1/logout", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceProof(w, r, "logout") })
	mux.HandleFunc("POST /v1/company-interface/authorizations/inspect", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceBrowser(w, r, "inspect") })
	mux.HandleFunc("POST /v1/company-interface/authorizations/approve", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceBrowser(w, r, "approve") })
	mux.HandleFunc("DELETE /v1/company-interface/grants/{id}", func(w http.ResponseWriter, r *http.Request) { s.companyInterfaceBrowser(w, r, "revoke") })
}
func companyInterfaceError(w http.ResponseWriter, e error) {
	status, code, message := http.StatusServiceUnavailable, "company_interface_unavailable", "Company authorization is unavailable; retain the pending operation and try again."
	switch {
	case errors.Is(e, ci.ErrProtocol):
		status, code, message = 409, "company_interface_update_required", "Update both installations before enabling the full company interface."
	case errors.Is(e, ci.ErrConflict):
		status, code, message = 409, "company_interface_conflict", "This authorization context changed; retain and reconcile its original operation."
	case errors.Is(e, ci.ErrPending):
		status, code, message = 428, "company_interface_pending", "Complete sign-in, matching-code review and explicit approval at the central service."
	case errors.Is(e, ci.ErrDenied):
		status, code, message = 403, "company_interface_denied", "Company authorization could not be verified."
	case errors.Is(e, ci.ErrRateLimited):
		status, code, message = 429, "company_interface_rate_limit", "Wait before retrying this authorization operation."
		w.Header().Set("Retry-After", "60")
	}
	writeError(w, status, code, message)
}
func (s *Server) companyInterfaceHost(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	meta := installation.FromContext(r.Context())
	if s.companyInterface == nil || meta.Role != "central" || s.companyCentralOrigin == "" || meta.CentralOrigin != s.companyCentralOrigin {
		companyInterfaceError(w, ci.ErrProtocol)
		return false
	}
	return true
}
func (s *Server) companyInterfaceRate(w http.ResponseWriter, r *http.Request, operation string) bool {
	peer, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		peer = r.RemoteAddr
	}
	hash := ci.Digest("company-interface:" + operation + ":" + peer + ":" + identity.ActorID(r.Context()))
	limit := 120
	if operation == "start" || operation == "inspect" || operation == "approve" || operation == "revoke" {
		limit = 30
	}
	allowed, e := s.companyInterface.AllowAttempt(r.Context(), hash[:], limit, time.Minute)
	if e != nil {
		companyInterfaceError(w, ci.ErrUnavailable)
		return false
	}
	if !allowed {
		companyInterfaceError(w, ci.ErrRateLimited)
		return false
	}
	return true
}
func decodeCompanyInterface(w http.ResponseWriter, r *http.Request, value any) bool {
	mediaType, _, mediaError := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || mediaError != nil || mediaType != "application/json" {
		companyInterfaceError(w, ci.ErrDenied)
		return false
	}
	deadline := time.Now().Add(ci.ProtocolTimeout)
	if limit, ok := r.Context().Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	_ = http.NewResponseController(w).SetReadDeadline(deadline)
	defer http.NewResponseController(w).SetReadDeadline(time.Time{})
	reader := http.MaxBytesReader(w, r.Body, ci.MaxProtocolBody)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		companyInterfaceError(w, ci.ErrDenied)
		return false
	}
	return true
}
func (s *Server) companyInterfaceCapabilities(w http.ResponseWriter, r *http.Request) {
	if !s.companyInterfaceHost(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	writeJSON(w, 200, ci.ImplementedProtocol())
}
func (s *Server) companyInterfaceProof(w http.ResponseWriter, r *http.Request, operation string) {
	if !s.companyInterfaceHost(w, r) {
		return
	}
	// Browser adapters never call these credential-returning endpoints. They use
	// the separate local coordinator and receive only non-secret status/code data.
	for _, header := range []string{"Origin", "Cookie", "Referer", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Authorization", "X-API-Key"} {
		if len(r.Header.Values(header)) > 0 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), ci.ProtocolTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if !s.companyInterfaceRate(w, r, operation) {
		return
	}
	var value any
	var e error
	switch operation {
	case "start":
		var in ci.Start
		if !decodeCompanyInterface(w, r, &in) {
			return
		}
		value, e = s.companyInterface.Start(ctx, in)
	case "logout":
		var in ci.Credentials
		if !decodeCompanyInterface(w, r, &in) {
			return
		}
		e = s.companyInterface.Logout(ctx, in)
		value = map[string]bool{"logged_out": e == nil}
	case "revoke-family":
		var in ci.Refresh
		if !decodeCompanyInterface(w, r, &in) {
			return
		}
		value, e = s.companyInterface.RevokeFamilyProof(ctx, in)
	case "refresh":
		var in ci.Refresh
		if !decodeCompanyInterface(w, r, &in) {
			return
		}
		value, e = s.companyInterface.Refresh(ctx, in)
	default:
		var in ci.Redemption
		if !decodeCompanyInterface(w, r, &in) {
			return
		}
		switch operation {
		case "redeem":
			value, e = s.companyInterface.Redeem(ctx, in)
		case "cancel":
			e = s.companyInterface.Cancel(ctx, in)
			value = map[string]bool{"cancelled": e == nil}
		case "reconcile":
			value, e = s.companyInterface.Reconcile(ctx, in)
		case "revoke-issued-family":
			e = s.companyInterface.RevokeIssuedFamily(ctx, in)
			value = map[string]bool{"revoked": e == nil}
		case "recover-installation":
			value, e = s.companyInterface.RecoverInstallation(ctx, in)
		default:
			e = ci.ErrDenied
		}
	}
	if e != nil {
		companyInterfaceError(w, e)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Server) companyInterfaceBrowser(w http.ResponseWriter, r *http.Request, operation string) {
	if !s.companyInterfaceHost(w, r) {
		return
	}
	own, _ := r.Context().Value(storageBrowserOriginKey{}).(string)
	// The comparison is to deployment configuration, never Host/Forwarded or a
	// posted origin. The outer CrossOriginProtection is an additional CSRF gate.
	if s.auth == nil || own == "" || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != own || len(r.Header.Values("Authorization")) > 0 || len(r.Header.Values("X-API-Key")) > 0 {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ci.ProtocolTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	user, e := s.auth.Authenticate(r)
	if e != nil || !ci.ValidID(user.SourceSessionID) {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	ctx = identity.WithUser(ctx, user)
	r = r.WithContext(ctx)
	if !s.companyInterfaceRate(w, r, operation) {
		return
	}
	switch operation {
	case "inspect":
		var in struct {
			Code string `json:"user_code"`
		}
		if !decodeCompanyInterface(w, r, &in) {
			return
		}
		challenge, binding, err := s.companyInterface.Describe(ctx, in.Code)
		if err != nil {
			companyInterfaceError(w, err)
			return
		}
		// No proofs, token hashes, private source-session anchors or credentials.
		writeJSON(w, 200, map[string]any{"attempt_id": challenge.ID, "user_code": challenge.Code, "state": challenge.State, "expires_at": challenge.ExpiresAt, "client_name": binding.Label, "approved_local_origin": binding.LocalOrigin, "central_origin": binding.CentralOrigin, "purpose": binding.Purpose, "version": binding.Version, "mode": binding.Mode})
	case "approve":
		var in struct {
			Code        string `json:"user_code"`
			OperationID string `json:"operation_id"`
			Consent     bool   `json:"consent"`
		}
		if !decodeCompanyInterface(w, r, &in) {
			return
		}
		challenge, err := s.companyInterface.Approve(ctx, ci.Approval{Code: in.Code, OperationID: in.OperationID, Consent: in.Consent})
		if err != nil {
			companyInterfaceError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"attempt_id": challenge.ID, "state": challenge.State})
	case "revoke":
		var in struct{}
		if !decodeCompanyInterface(w, r, &in) {
			return
		}
		if err := s.companyInterface.Revoke(ctx, r.PathValue("id")); err != nil {
			companyInterfaceError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"revoked": true})
	default:
		companyInterfaceError(w, ci.ErrDenied)
	}
}
