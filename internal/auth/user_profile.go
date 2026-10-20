package auth

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

var ErrInvalidProfile = errors.New("invalid profile")

type ProfileUpdate struct {
	Contacts      *[]identity.Contact `json:"contacts"`
	Name          *string             `json:"name"`
	Theme         *string             `json:"theme"`
	ColorPreset   *string             `json:"color_preset"`
	ReportColumns *[]string           `json:"report_columns"`
}

type ProfileRepository interface {
	UpdateProfile(context.Context, string, ProfileUpdate) (identity.User, error)
}

func (m *Manager) UpdateProfile(ctx context.Context, id string, in ProfileUpdate) (identity.User, error) {
	if in.Name == nil && in.Theme == nil && in.ReportColumns == nil && in.ColorPreset == nil && in.Contacts == nil {
		return identity.User{}, ErrInvalidProfile
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
			return identity.User{}, ErrInvalidProfile
		}
		in.Name = &name
	}
	if in.Theme != nil && *in.Theme != "dark" && *in.Theme != "light" && *in.Theme != "system" {
		return identity.User{}, ErrInvalidProfile
	}
	if in.ColorPreset != nil && !branding.ValidPresetID(*in.ColorPreset) {
		return identity.User{}, ErrInvalidProfile
	}
	if in.ReportColumns != nil {
		allowed := map[string]bool{
			"date": true, "generation_id": true, "organization": true, "user": true,
			"api_key": true, "model": true, "provider": true, "status": true, "tokens": true,
			"estimated_cost": true, "cost": true, "confirmed_cost": true, "settlement": true, "customer_charge": true, "profit": true, "profit_status": true,
		}
		if len(*in.ReportColumns) == 0 || len(*in.ReportColumns) > len(allowed) {
			return identity.User{}, ErrInvalidProfile
		}
		seen := map[string]bool{}
		for _, column := range *in.ReportColumns {
			if !allowed[column] || seen[column] {
				return identity.User{}, ErrInvalidProfile
			}
			seen[column] = true
		}
	}
	if in.Contacts != nil {
		contacts := append([]identity.Contact{}, (*in.Contacts)...)
		if len(contacts) > 20 {
			return identity.User{}, ErrInvalidProfile
		}
		for i := range contacts {
			c := &contacts[i]
			c.Label, c.URL = strings.TrimSpace(c.Label), strings.TrimSpace(c.URL)
			if c.Label == "" || utf8.RuneCountInString(c.Label) > 80 || strings.ContainsFunc(c.Label, unicode.IsControl) || len(c.URL) > 2048 || !identity.ValidContactURL(c.URL) {
				return identity.User{}, ErrInvalidProfile
			}
		}
		in.Contacts = &contacts
	}
	repo, ok := m.repo.(ProfileRepository)
	if !ok {
		return identity.User{}, errors.New("profile storage unavailable")
	}
	return repo.UpdateProfile(ctx, id, in)
}
