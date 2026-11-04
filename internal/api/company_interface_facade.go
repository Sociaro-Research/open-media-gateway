package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

const companyFacadePrefix = ci.LocalCustomerPrefix

// Reserved facade exercises the real central endpoint composition without
// changing the existing portal mode. Complete capability negotiation still
// precedes any refresh/dispatch; no partial Connected state is introduced.
func (s *Server) registerCompanyFacadeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /auth/company-interface/local/customer/{path...}", s.companyFacade)
	mux.HandleFunc("GET /auth/company-interface/local/customer/{path...}", s.companyFacade)
	mux.HandleFunc("HEAD /auth/company-interface/local/customer/{path...}", s.companyFacade)
	mux.HandleFunc("POST /auth/company-interface/local/customer/{path...}", s.companyFacade)
	mux.HandleFunc("PATCH /auth/company-interface/local/customer/{path...}", s.companyFacade)
	mux.HandleFunc("DELETE /auth/company-interface/local/customer/{path...}", s.companyFacade)
}
func companyHeaderPresent(h http.Header, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}
func (s *Server) companyFacade(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	own, _ := r.Context().Value(storageBrowserOriginKey{}).(string)
	meta := installation.FromContext(r.Context())
	if s.companyInterface == nil || s.companyCoordinator == nil || own == "" || own != s.companyCoordinator.LocalOrigin() || meta.Role == "central" || meta.CentralOrigin != s.companyCoordinator.CentralOrigin() {
		companyInterfaceError(w, ci.ErrProtocol)
		return
	}
	path := "/" + strings.TrimPrefix(r.URL.EscapedPath(), companyFacadePrefix)
	if ci.Classify(r.Method, path).Stream && (r.ContentLength != 0 || len(r.TransferEncoding) != 0) {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ci.CustomerTimeout(r.Method, path))
	defer cancel()
	r = r.WithContext(ctx)
	// Ordinary customer traffic uses the existing central company/key request
	// limits, not a shared-IP authorization-attempt product limit.
	browser := false
	for _, name := range []string{"Cookie", "Origin", "Referer", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User"} {
		if companyHeaderPresent(r.Header, name) {
			browser = true
		}
	}
	local := ""
	if browser {
		cookies := r.CookiesNamed(companyLocalCookie)
		if len(cookies) != 1 || !ci.ValidSecret(cookies[0].Value) {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		local = cookies[0].Value
		origins := r.Header.Values("Origin")
		if companyHeaderPresent(r.Header, "Origin") && (len(origins) != 1 || origins[0] != own) {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && (len(origins) != 1 || origins[0] != own) {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
	}
	if companyHeaderPresent(r.Header, "X-API-Key") {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	keyPresent := companyHeaderPresent(r.Header, "Authorization")
	key := ""
	if keyPresent {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(r.Header.Values("Authorization")) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		key = parts[1]
	}
	if !browser && !keyPresent {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	prepareContext := ctx
	done := func() {}
	if ci.Classify(r.Method, path).Stream {
		prepareContext, done = context.WithTimeout(ctx, ci.MediaAuthorityTimeout)
	}
	prepared, e := s.companyCoordinator.PrepareCustomer(prepareContext, local, browser)
	done()
	if e != nil {
		companyInterfaceError(w, e)
		return
	}
	// Capture the exact local/global fence before consuming even the request body.
	deadline, _ := ctx.Deadline()
	_ = http.NewResponseController(w).SetReadDeadline(deadline)
	_ = http.NewResponseController(w).SetWriteDeadline(deadline.Add(5 * time.Second))
	defer http.NewResponseController(w).SetReadDeadline(time.Time{})
	defer r.Body.Close()
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, ci.CustomerBodyLimit(r.Method, path)))
	if e != nil {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	if ci.Classify(r.Method, path).Stream {
		if len(raw) != 0 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		if e := s.companyCoordinator.StreamCustomer(ctx, prepared, r.Method, path, r.URL.RawQuery, r.Header, keyPresent, key, w); e != nil {
			companyInterfaceError(w, e)
		}
		return
	}
	response, e := s.companyCoordinator.Customer(ctx, prepared, r.Method, path, r.URL.RawQuery, raw, r.Header, keyPresent, key)
	if e != nil {
		companyInterfaceError(w, e)
		return
	}
	for name, values := range response.Header {
		w.Header()[name] = append([]string(nil), values...)
	}
	w.WriteHeader(response.Status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(response.Body)
	}
}
