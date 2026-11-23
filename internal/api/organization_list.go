package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

func organizationList(r *http.Request, repo org.Repository, actor string, all bool) ([]org.Organization, time.Time, error) {
	now := time.Now().UTC()
	q := r.URL.Query()
	if q.Get("view") == "" {
		items, err := repo.Organizations(r.Context(), actor, all)
		return items, time.Time{}, err
	}
	if q.Get("view") != "summary" {
		return nil, now, org.ErrInvalid
	}
	location := time.UTC
	if zone := q.Get("timezone"); zone != "" {
		var err error
		location, err = time.LoadLocation(zone)
		if err != nil {
			return nil, now, org.ErrInvalid
		}
	}
	local := now.In(location)
	fromDate := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
	toDate := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	var err error
	if q.Get("from") != "" {
		fromDate, err = time.Parse("2006-01-02", q.Get("from"))
		if err != nil {
			return nil, now, org.ErrInvalid
		}
	}
	if q.Get("to") != "" {
		toDate, err = time.Parse("2006-01-02", q.Get("to"))
		if err != nil {
			return nil, now, org.ErrInvalid
		}
	}
	if fromDate.After(toDate) {
		return nil, now, org.ErrInvalid
	}
	reader, ok := repo.(org.UsageReader)
	if !ok {
		return nil, now, errors.New("organization usage is unavailable")
	}
	// Same calendar days as reports, also where midnight is skipped or repeats.
	from, until := core.ReportDayStart(fromDate, location), core.ReportDayStart(toDate.AddDate(0, 0, 1), location)
	items, err := reader.OrganizationsWithUsage(r.Context(), actor, all, from, until)
	return items, now, err
}
