package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

var ErrInvalidProfile = errors.New("invalid profile")

type ProfileUpdate struct {
	Contacts      *[]identity.Contact  `json:"contacts"`
	Name          *string              `json:"name"`
	Theme         *string              `json:"theme"`
	ColorPreset   *string              `json:"color_preset"`
	ReportColumns *[]string            `json:"report_columns"`
	TableColumns  *map[string][]string `json:"table_columns"`
	TableWide     *map[string]*bool    `json:"table_wide"`
}

type ProfileRepository interface {
	UpdateProfile(context.Context, string, ProfileUpdate) (identity.User, error)
}

func (m *Manager) UpdateProfile(ctx context.Context, id string, in ProfileUpdate) (identity.User, error) {
	if in.Name == nil && in.Theme == nil && in.ReportColumns == nil && in.ColorPreset == nil && in.Contacts == nil && in.TableColumns == nil && in.TableWide == nil {
		return identity.User{}, invalidProfile("body", "provide at least one supported profile field: name, theme, color_preset, report_columns, table_columns, table_wide or contacts")
	}
	if err := validateTablePreferences(in); err != nil {
		return identity.User{}, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) || identity.UnreadableName(name) {
			return identity.User{}, invalidProfile("name", "use 1–100 Unicode characters after trimming, at least one visible, without control or text-direction characters")
		}
		in.Name = &name
	}
	if in.Theme != nil && *in.Theme != "dark" && *in.Theme != "light" && *in.Theme != "system" {
		return identity.User{}, invalidProfile("theme", "choose dark, light or system")
	}
	if in.ColorPreset != nil && !branding.ValidPresetID(*in.ColorPreset) {
		return identity.User{}, ErrProfileColorPreset
	}
	if in.ReportColumns != nil {
		allowed := map[string]bool{
			"date": true, "generation_id": true, "organization": true, "user": true,
			"api_key": true, "model": true, "provider": true, "status": true, "tokens": true,
			"estimated_cost": true, "cost": true, "confirmed_cost": true, "settlement": true, "customer_charge": true, "profit": true, "profit_status": true,
		}
		if len(*in.ReportColumns) == 0 || len(*in.ReportColumns) > len(allowed) {
			return identity.User{}, invalidProfile("report_columns", "select 1–16 unique supported report columns")
		}
		seen := map[string]bool{}
		for i, column := range *in.ReportColumns {
			if !allowed[column] {
				return identity.User{}, invalidProfile(fmt.Sprintf("report_columns[%d]", i), "choose date, generation_id, organization, user, api_key, model, provider, status, tokens, estimated_cost, cost, confirmed_cost, settlement, customer_charge, profit or profit_status")
			}
			if seen[column] {
				return identity.User{}, invalidProfile(fmt.Sprintf("report_columns[%d]", i), "remove this duplicate report column")
			}
			seen[column] = true
		}
	}
	if in.Contacts != nil {
		contacts := append([]identity.Contact{}, (*in.Contacts)...)
		if len(contacts) > 20 {
			return identity.User{}, invalidProfile("contacts", "provide at most 20 contacts")
		}
		for i := range contacts {
			c := &contacts[i]
			c.Label, c.URL = strings.TrimSpace(c.Label), strings.TrimSpace(c.URL)
			if c.Label == "" || utf8.RuneCountInString(c.Label) > 80 || strings.ContainsFunc(c.Label, unicode.IsControl) {
				return identity.User{}, invalidProfile(fmt.Sprintf("contacts[%d].label", i), "use 1–80 Unicode characters after trimming, without control characters")
			}
			if len(c.URL) > 2048 || !identity.ValidContactURL(c.URL) {
				return identity.User{}, invalidProfile(fmt.Sprintf("contacts[%d].url", i), "use an HTTP(S), mailto or tel link of at most 2048 bytes, without whitespace, control characters or embedded credentials")
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
