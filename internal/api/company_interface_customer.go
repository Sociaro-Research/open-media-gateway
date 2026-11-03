package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

type companyCustomerKey struct{}

func companyCustomerFromContext(ctx context.Context) (ci.Principal, bool) {
	p, ok := ctx.Value(companyCustomerKey{}).(ci.Principal)
	return p, ok
}

// These server-hop routes choose browser versus SDK authentication in trusted
// composition. Caller identity headers never enter the inner handler context.
func (s *Server) registerCompanyCustomerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /auth/company-interface/v1/customer-browser/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, true) })
	mux.HandleFunc("PUT /auth/company-interface/v1/customer-sdk/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, false) })
	mux.HandleFunc("GET /auth/company-interface/v1/customer-browser/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, true) })
	mux.HandleFunc("HEAD /auth/company-interface/v1/customer-browser/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, true) })
	mux.HandleFunc("POST /auth/company-interface/v1/customer-browser/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, true) })
	mux.HandleFunc("PATCH /auth/company-interface/v1/customer-browser/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, true) })
	mux.HandleFunc("DELETE /auth/company-interface/v1/customer-browser/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, true) })
	mux.HandleFunc("GET /auth/company-interface/v1/customer-sdk/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, false) })
	mux.HandleFunc("HEAD /auth/company-interface/v1/customer-sdk/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, false) })
	mux.HandleFunc("POST /auth/company-interface/v1/customer-sdk/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, false) })
	mux.HandleFunc("PATCH /auth/company-interface/v1/customer-sdk/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, false) })
	mux.HandleFunc("DELETE /auth/company-interface/v1/customer-sdk/{path...}", func(w http.ResponseWriter, r *http.Request) { s.companyCustomerRequest(w, r, mux, false) })
}

func (s *Server) companyCustomerRequest(w http.ResponseWriter, r *http.Request, mux *http.ServeMux, browser bool) {
	if !s.companyInterfaceHost(w, r) {
		return
	}
	if s.companyCustomer == nil {
		companyInterfaceError(w, ci.ErrProtocol)
		return
	}
	for _, name := range []string{"Origin", "Cookie", "Referer", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User", "X-API-Key"} {
		if len(r.Header.Values(name)) != 0 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
	}
	for _, name := range []string{ci.InstallationHeader, ci.LocalOriginHeader} {
		if len(r.Header.Values(name)) != 1 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
	}
	c := ci.Credentials{Installation: r.Header.Get(ci.InstallationHeader), KeyPresent: len(r.Header.Values("Authorization")) != 0}
	if c.KeyPresent {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(r.Header.Values("Authorization")) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		c.Key = parts[1]
	}
	if browser {
		if len(r.Header.Values(ci.SessionHeader)) != 1 || len(r.Header.Values(ci.LocalSessionHeader)) != 1 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		c.Session = r.Header.Get(ci.SessionHeader)
		hash, e := hex.DecodeString(r.Header.Get(ci.LocalSessionHeader))
		if e != nil || len(hash) != 32 {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		copy(c.LocalSessionHash[:], hash)
	} else if len(r.Header.Values(ci.SessionHeader)) != 0 || len(r.Header.Values(ci.LocalSessionHeader)) != 0 {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ci.ProtocolTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	gate := ci.Gate{Authority: s.companyCustomer, Keys: s.companyCustomer, Recipients: s.companyCustomer, CentralOrigin: s.companyCentralOrigin}
	authenticate := gate.AuthenticateSDK
	if browser {
		authenticate = gate.AuthenticateBrowser
	}
	principal, e := authenticate(ctx, c, time.Now())
	if e != nil || r.Header.Get(ci.LocalOriginHeader) != principal.Grant.LocalOrigin {
		// Failed credentials use the bounded failure counter. Valid company
		// traffic retains its existing central operation/key/company policy.
		if !s.companyInterfaceRate(w, r, "customer-invalid") {
			return
		}
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	prefix := ci.CustomerSDKPrefix
	if browser {
		prefix = ci.CustomerBrowserPrefix
	}
	path := "/" + strings.TrimPrefix(r.URL.EscapedPath(), prefix)
	deadline, _ := ctx.Deadline()
	_ = http.NewResponseController(w).SetReadDeadline(deadline)
	defer http.NewResponseController(w).SetReadDeadline(time.Time{})
	defer r.Body.Close()
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if e != nil {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	// Browser management is performed by the verified session actor. A supplied
	// key is still validated above, but its recipient cannot lend management
	// authority to that browser. SDK and generation keep original-key identity.
	effective := principal
	if browser && ci.Classify(r.Method, path).BrowserManagement {
		role, err := s.companyCustomer.Membership(ctx, principal.Grant.Company, principal.BrowserActor)
		if err != nil {
			companyInterfaceError(w, ci.ErrDenied)
			return
		}
		effective.Kind = ci.CentralSession
		effective.Actor = principal.BrowserActor
		effective.Recipient = principal.BrowserActor
		effective.Role = role
		effective.KeyID = ""
	}
	contract, e := ci.RequestContract(r.Method, path, r.URL.RawQuery, raw, effective)
	if e != nil {
		companyInterfaceError(w, e)
		return
	}
	headers, e := ci.ForwardHeaders(contract, r.Header)
	if e != nil {
		companyInterfaceError(w, e)
		return
	}
	if effective.Kind == ci.CentralKey {
		headers.Set("Authorization", "Bearer "+c.Key)
	}
	user, e := s.companyCustomer.User(ctx, effective.Actor)
	if e != nil || user.ID != effective.Actor {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	// Stable, non-authorizing display/cache identifier. No central source UUID
	// or access/refresh/installation secret is exposed in the user projection.
	if browser {
		sum := sha256.Sum256([]byte("company-viewer:" + principal.Grant.ID + ":" + principal.SessionID))
		user.SessionID = hex.EncodeToString(sum[:])
	}
	user.SourceSessionID = ""
	ctx = identity.WithCompanyBoundary(ctx, principal.Grant.Company)
	ctx = identity.WithUser(ctx, user)
	ctx = identity.WithOrganizationRole(ctx, principal.Grant.Company, effective.Role)
	ctx = context.WithValue(ctx, companyCustomerKey{}, effective)
	inner := r.Clone(ctx)
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		companyInterfaceError(w, ci.ErrDenied)
		return
	}
	inner.URL = &url.URL{Path: decodedPath, RawPath: path, RawQuery: r.URL.RawQuery}
	inner.RequestURI = inner.URL.RequestURI()
	inner.Header = headers
	inner.Body = io.NopCloser(bytes.NewReader(raw))
	inner.ContentLength = int64(len(raw))
	// This bounded first adapter deliberately buffers its supported responses so
	// revoked/rotated authority cannot release a late body. Large/streaming
	// media integration remains a separate contract and is not advertised.
	out := &companyCustomerResponse{header: http.Header{}}
	mux.ServeHTTP(out, inner)
	latest, e := authenticate(ctx, c, time.Now())
	if e != nil || latest.Grant.ID != principal.Grant.ID || latest.Grant.Epoch != principal.Grant.Epoch || latest.Actor != principal.Actor || latest.Role != principal.Role || latest.SessionID != principal.SessionID || latest.SessionEpoch != principal.SessionEpoch || latest.SessionVersion != principal.SessionVersion || latest.KeyID != principal.KeyID {
		companyInterfaceError(w, ci.ErrConflict)
		return
	}
	if effective.Kind == ci.CentralSession && browser {
		role, err := s.companyCustomer.Membership(ctx, principal.Grant.Company, effective.Actor)
		if err != nil || role != effective.Role {
			companyInterfaceError(w, ci.ErrConflict)
			return
		}
		latest.Kind, latest.Actor, latest.Recipient, latest.Role, latest.KeyID = effective.Kind, effective.Actor, effective.Recipient, role, ""
	}
	if _, e = ci.RequestContract(r.Method, path, r.URL.RawQuery, raw, latest); e != nil {
		companyInterfaceError(w, ci.ErrConflict)
		return
	}
	if out.overflow || ctx.Err() != nil {
		companyInterfaceError(w, ci.ErrUnavailable)
		return
	}
	for name, values := range out.header {
		switch http.CanonicalHeaderKey(name) {
		case "Content-Type", "Content-Disposition", "Content-Range", "Accept-Ranges", "Etag", "Last-Modified", "Retry-After", "Location":
			w.Header()[name] = append([]string(nil), values...)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	status := out.status
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(out.body.Bytes())
	}
}

type companyCustomerResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *companyCustomerResponse) Header() http.Header { return w.header }
func (w *companyCustomerResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *companyCustomerResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.overflow || w.body.Len()+len(p) > 8<<20 {
		w.overflow = true
		return 0, io.ErrShortBuffer
	}
	return w.body.Write(p)
}
