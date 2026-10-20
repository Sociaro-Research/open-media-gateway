package api

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

func (s *Server) costReport(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/admin/reports" {
		if credentialOrganization(r.Context()) != "" {
			writeError(w, 403, "forbidden", "use this organization's report endpoint")
			return
		}
		r = r.WithContext(identity.WithPersonalData(r.Context()))
	}
	if _, ok := personalOwner(w, r); !ok {
		return
	}
	q := r.URL.Query()
	location := time.UTC
	if zone := q.Get("timezone"); zone != "" {
		var err error
		location, err = time.LoadLocation(zone)
		if err != nil {
			writeError(w, 400, "invalid_filter", "invalid timezone")
			return
		}
	}
	now := time.Now().In(location)
	f := core.ReportFilter{From: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location), Until: time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, location), Limit: 50, KeyID: q.Get("api_key_id"), GenerationID: strings.TrimSpace(q.Get("generation_id"))}
	f.AllOrganizations = strings.HasPrefix(r.URL.Path, "/v1/system/")
	if identity.FinancialAuthorFilter(r.Context()) == "" {
		user := strings.TrimSpace(q.Get("user"))
		if user == "" {
			user = strings.TrimSpace(q.Get("user_id"))
		}
		if strings.Contains(user, "@") {
			address, err := mail.ParseAddress(user)
			if err != nil || address.Address != user {
				writeError(w, 400, "invalid_filter", "enter a user ID or email")
				return
			}
			f.UserEmail = user
		} else {
			f.UserID = user
		}
	}
	bad := func() {
		writeError(w, http.StatusBadRequest, "invalid_filter", "use YYYY-MM-DD dates (from <= to), valid generation and API key IDs, limit 1–100 and offset 0–100000")
	}
	var err error
	if q.Get("from") != "" {
		f.From, err = time.ParseInLocation("2006-01-02", q.Get("from"), location)
		if err != nil {
			bad()
			return
		}
	}
	if q.Get("to") != "" {
		f.Until, err = time.ParseInLocation("2006-01-02", q.Get("to"), location)
		if err != nil {
			bad()
			return
		}
		f.Until = f.Until.AddDate(0, 0, 1)
	}
	if !f.From.Before(f.Until) {
		bad()
		return
	}
	if f.UserID != "" {
		if _, err = uuid.Parse(f.UserID); err != nil {
			bad()
			return
		}
	}
	if f.GenerationID != "" {
		id, parseErr := uuid.Parse(f.GenerationID)
		if parseErr != nil {
			bad()
			return
		}
		f.GenerationID = id.String()
	}
	if f.KeyID != "" {
		if _, err = uuid.Parse(f.KeyID); err != nil {
			bad()
			return
		}
	}
	if q.Has("limit") {
		f.Limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || f.Limit < 1 || f.Limit > 100 {
			bad()
			return
		}
	}
	if q.Has("offset") {
		f.Offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil || f.Offset < 0 || f.Offset > 100000 {
			bad()
			return
		}
	}
	if format := q.Get("format"); format != "" && format != "csv" {
		writeError(w, 400, "invalid_filter", "unsupported report format")
		return
	}
	if view := q.Get("view"); view != "" && view != "summary" {
		writeError(w, 400, "invalid_filter", "unsupported report view")
		return
	}
	if q.Get("view") == "summary" || q.Get("format") == "csv" {
		// Large summaries and CSV spools can legitimately exceed the server's
		// ordinary 15-second write deadline. Bound their work separately, leaving
		// time to send a structured failure if calculation reaches its deadline.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			writeError(w, 503, "unavailable", "report could not be started")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
	}
	if q.Get("view") == "summary" {
		if q.Get("format") != "" {
			writeError(w, 400, "invalid_filter", "summary cannot be exported as CSV")
			return
		}
		interval := q.Get("interval")
		if interval == "" {
			interval = "day"
		}
		reader, ok := s.service.(core.ReportWalker)
		if !ok {
			writeError(w, 503, "unavailable", "report summary is unavailable")
			return
		}
		summary, err := core.SummarizeCostReport(r.Context(), reader, f, interval)
		if errors.Is(err, core.ErrReportInterval) {
			writeError(w, 400, "invalid_filter", err.Error())
			return
		}
		if err != nil {
			writeError(w, 503, "unavailable", "report summary could not be loaded")
			return
		}
		s.writeViewerJSON(w, r, 200, summary)
		return
	}
	if q.Get("format") == "csv" {
		s.exportCostReport(w, r, f)
		return
	}
	reader, ok := s.service.(interface {
		CostReport(context.Context, core.ReportFilter) ([]core.CostReportRow, error)
	})
	if !ok {
		writeError(w, 503, "unavailable", "cost reports are unavailable")
		return
	}
	limit := f.Limit
	f.Limit++
	rows, err := reader.CostReport(r.Context(), f)

	if err != nil {
		writeError(w, 503, "unavailable", "cost report could not be loaded")
		return
	}
	role := identity.OrganizationFromContext(r.Context()).Role
	if role == "admin" || role == "client_manager" {
		for i := range rows {
			rows[i].Usage = safeUsage(rows[i].Usage)
		}
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": rows, "has_more": more, "next_offset": f.Offset + len(rows)})
}
