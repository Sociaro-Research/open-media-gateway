package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/audit"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
	"slices"
)

func (s *Server) registerAudit(mux *http.ServeMux) {
	mux.Handle("GET /v1/audit/events", s.authenticate(http.HandlerFunc(s.auditEvents)))
	mux.Handle("GET /v1/audit/events/{event}", s.authenticate(http.HandlerFunc(s.auditEvents)))
	mux.Handle("GET /v1/audit/revoked-keys", s.authenticate(http.HandlerFunc(s.revokedKeys)))
	mux.Handle("GET /v1/organizations/{org}/audit/events", s.authenticate(http.HandlerFunc(s.auditEvents)))
	mux.Handle("GET /v1/organizations/{org}/audit/events/{event}", s.authenticate(http.HandlerFunc(s.auditEvents)))
	mux.Handle("GET /v1/organizations/{org}/audit/revoked-keys", s.authenticate(http.HandlerFunc(s.revokedKeys)))
}

// Organization managers can inspect management history without learning internal
// pricing or administrator-only configuration. Apply this to both list and detail.
func projectOrganizationAudit(event audit.Event) audit.Event {
	switch event.Action {
	case "markups", "request_limits", "model_access", "organization_client_manager", "client_onboarding", "payment_settlement":
		event.Details = json.RawMessage(`{"restricted":true}`)
		return event
	}
	var data any
	if err := json.Unmarshal(event.Details, &data); err != nil {
		event.Details = json.RawMessage(`{"restricted":true}`)
		return event
	}
	var scrub func(any)
	scrub = func(value any) {
		switch fields := value.(type) {
		case map[string]any:
			for key, value := range fields {
				switch key {
				case "markup_basis_points", "markup_exceptions", "markup_percent", "request_limits", "model_ids", "client_manager_id", "settlement", "provider_cost", "provider_usage", "actual_cost", "tariff_usd", "metered_usd":
					delete(fields, key)
				default:
					scrub(value)
				}
			}
		case []any:
			for _, item := range fields {
				scrub(item)
			}
		}
	}
	scrub(data)
	event.Details, _ = json.Marshal(data)
	return event
}
func (s *Server) auditAccess(w http.ResponseWriter, r *http.Request) (audit.Store, string, bool) {
	store, ok := s.system.(audit.Store)
	if !ok {
		writeError(w, 503, "unavailable", "audit history is unavailable")
		return nil, "", false
	}
	id := r.PathValue("org")
	bound, _ := r.Context().Value(credentialOrganizationKey{}).(string)
	if bound != "" && bound != id {
		orgError(w, org.ErrForbidden)
		return nil, "", false
	}
	if id == "" {
		admin, err := s.systemAdmin(r.Context())
		if err != nil {
			systemError(w, err)
			return nil, "", false
		}
		if !admin || bound != "" {
			orgError(w, org.ErrForbidden)
			return nil, "", false
		}
	} else {
		role, err := s.organizationStore().OrganizationAccess(r.Context(), identity.ActorID(r.Context()), id)
		if err != nil {
			orgError(w, err)
			return nil, "", false
		}
		scopes, _ := r.Context().Value(apiKeyScopesContextKey{}).([]string)
		if !org.Manage(role) || identity.APIKeyID(r.Context()) != nil && !slices.Contains(scopes, apikey.OrganizationsManage) {
			orgError(w, org.ErrForbidden)
			return nil, "", false
		}
	}
	return store, id, true
}
func auditFilter(w http.ResponseWriter, r *http.Request, org string, revoked bool) (audit.Filter, bool) {
	q := r.URL.Query()
	f := audit.Filter{Scope: q.Get("scope"), OrganizationID: org, ActorID: q.Get("actor_id"), Action: q.Get("action"), ObjectType: q.Get("object_type"), ObjectID: q.Get("object_id"), Outcome: q.Get("outcome"), CorrelationID: q.Get("correlation_id"), Limit: 51}
	if org == "" {
		f.OrganizationID = q.Get("organization_id")
	}
	zone := time.UTC
	var err error
	if v := q.Get("timezone"); v != "" {
		zone, err = time.LoadLocation(v)
		if err != nil {
			writeError(w, 400, "invalid_filter", "invalid timezone")
			return f, false
		}
	}
	now := time.Now().In(zone)
	f.From = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, zone)
	f.Until = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, zone)
	bad := func() (audit.Filter, bool) {
		writeError(w, 400, "invalid_filter", "use valid dates, IDs, result and pagination values")
		return f, false
	}
	if v := q.Get("from"); v != "" {
		f.From, err = time.ParseInLocation("2006-01-02", v, zone)
		if err != nil {
			return bad()
		}
	}
	if v := q.Get("to"); v != "" {
		f.Until, err = time.ParseInLocation("2006-01-02", v, zone)
		if err != nil {
			return bad()
		}
		f.Until = f.Until.AddDate(0, 0, 1)
	}
	if !f.From.Before(f.Until) {
		return bad()
	}
	for _, id := range []string{f.OrganizationID} {
		if id != "" {
			if _, e := uuid.Parse(id); e != nil {
				return bad()
			}
		}
	}
	for _, v := range []string{f.ActorID, f.Action, f.ObjectType, f.ObjectID, f.CorrelationID} {
		if len(v) > 200 {
			return bad()
		}
	}
	if f.Scope != "" && f.Scope != "system" && f.Scope != "organization" && f.Scope != "personal" {
		return bad()
	}
	if f.Outcome != "" && f.Outcome != "success" && f.Outcome != "denied" && f.Outcome != "failed" {
		return bad()
	}
	page := "before_id"
	if revoked {
		page = "offset"
	}
	if v := q.Get(page); v != "" {
		f.BeforeID, err = strconv.ParseInt(v, 10, 64)
		if err != nil || f.BeforeID < 0 || revoked && f.BeforeID > 100000 {
			return bad()
		}
	}
	return f, true
}
func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request) {
	store, org, ok := s.auditAccess(w, r)
	if !ok {
		return
	}
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if value := r.PathValue("event"); value != "" {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 1 {
			writeError(w, 400, "invalid_filter", "invalid event ID")
			return
		}
		event, err := store.AuditEvent(r.Context(), id, org)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "not_found", "audit event not found")
			return
		}
		if err != nil {
			systemError(w, err)
			return
		}
		if !admin {
			event = projectOrganizationAudit(event)
		}
		s.writeViewerJSON(w, r, 200, event)
		return
	}
	f, ok := auditFilter(w, r, org, false)
	if !ok {
		return
	}
	events, err := store.AuditEvents(r.Context(), f)
	if err != nil {
		systemError(w, err)
		return
	}
	more := len(events) > 50
	if more {
		events = events[:50]
	}
	if !admin {
		for i := range events {
			events[i] = projectOrganizationAudit(events[i])
		}
	}
	cursor := int64(0)
	if len(events) > 0 {
		cursor = events[len(events)-1].ID
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": events, "has_more": more, "next_before_id": cursor})
}
func (s *Server) revokedKeys(w http.ResponseWriter, r *http.Request) {
	store, org, ok := s.auditAccess(w, r)
	if !ok {
		return
	}
	f, ok := auditFilter(w, r, org, true)
	if !ok {
		return
	}
	keys, err := store.RevokedKeys(r.Context(), f)
	if err != nil {
		systemError(w, err)
		return
	}
	more := len(keys) > 50
	if more {
		keys = keys[:50]
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": keys, "has_more": more, "next_offset": f.BeforeID + int64(len(keys))})
}

type auditResponse struct {
	http.ResponseWriter
	status int
}

func (w *auditResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *auditResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.ResponseWriter.Write(b)
}

// Management mutations have bounded bodies and do not stream. Audit failure
// recording is bounded too; a broken journal is surfaced in operational logs.
func (s *Server) auditRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(audit.WithCorrelation(r.Context(), id))
		if r.Method == "GET" || r.Method == "HEAD" || !auditMutation(r) {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &auditResponse{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status >= 400 {
			s.recordAuditFailure(r, recorder.status)
		}
	})
}
func auditMutation(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/v1/system/") || strings.HasPrefix(p, "/v1/api-keys") || p == "/v1/me" || strings.HasPrefix(p, "/v1/invitations/") || strings.HasPrefix(p, "/v1/admin/providers") || strings.HasPrefix(p, "/v1/admin/storage") || strings.HasPrefix(p, "/v1/organizations") && !strings.Contains(p, "/generations")
}
func (s *Server) recordAuditFailure(r *http.Request, status int) {
	store, ok := s.system.(audit.Store)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	org := r.PathValue("org")
	if _, err := uuid.Parse(org); err != nil {
		org = ""
	}
	// The registered route template excludes arbitrary path tokens and query text.
	if err := store.AuditFailure(ctx, org, "management_request", "request", r.Pattern, status); err != nil {
		slog.Error("audit failure could not be recorded", "correlation_id", audit.Correlation(ctx), "http_status", status, "error", err)
	}
}
