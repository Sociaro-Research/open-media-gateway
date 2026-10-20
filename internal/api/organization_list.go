package api

import (
	"errors"
	"net/http"
	"time"

	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
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
	from := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
	until := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
	var err error
	if q.Get("from") != "" {
		from, err = time.ParseInLocation("2006-01-02", q.Get("from"), location)
		if err != nil {
			return nil, now, org.ErrInvalid
		}
	}
	if q.Get("to") != "" {
		until, err = time.ParseInLocation("2006-01-02", q.Get("to"), location)
		if err != nil {
			return nil, now, org.ErrInvalid
		}
		until = until.AddDate(0, 0, 1)
	}
	if !from.Before(until) {
		return nil, now, org.ErrInvalid
	}
	reader, ok := repo.(org.UsageReader)
	if !ok {
		return nil, now, errors.New("organization usage is unavailable")
	}
	items, err := reader.OrganizationsWithUsage(r.Context(), actor, all, from, until)
	return items, now, err
}
