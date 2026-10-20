package postgres

import (
	"context"
	"encoding/json"

	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func (r *SystemRepository) Branding(ctx context.Context) (branding.Settings, error) {
	s := branding.Defaults()
	var raw []byte
	var version int64
	err := r.db.QueryRowContext(ctx, `SELECT configuration,version FROM site_branding WHERE id=TRUE`).Scan(&raw, &version)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	s.Version = version
	return s.Normalize(), nil
}
func (r *SystemRepository) SaveBranding(ctx context.Context, actor string, next branding.Settings) (branding.Settings, error) {
	if err := next.Validate(); err != nil {
		return next, err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	name, err := systemActor(ctx, tx, actor)
	if err != nil {
		return next, err
	}
	var raw []byte
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT configuration,version FROM site_branding WHERE id=TRUE FOR UPDATE`).Scan(&raw, &version); err != nil {
		return next, err
	}
	if version != next.Version {
		return next, system.ErrConflict
	}
	before := branding.Defaults()
	if err = json.Unmarshal(raw, &before); err != nil {
		return next, err
	}
	before.Version = version
	before = before.Normalize()
	// Legacy clients edit the default pair without erasing the saved catalog.
	if next.Presets == nil {
		next.Presets, next.DefaultPreset = append([]branding.Preset(nil), before.Presets...), before.DefaultPreset
		for i := range next.Presets {
			if next.Presets[i].ID == next.DefaultPreset {
				next.Presets[i].Dark = next.Dark
				next.Presets[i].Light = next.Light
			}
		}
	}
	next = next.Normalize()
	if err = next.Validate(); err != nil {
		return next, err
	}
	next.Version++
	data, err := json.Marshal(next)
	if err != nil {
		return next, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE site_branding SET configuration=$1,version=$2,updated_at=NOW() WHERE id=TRUE`, data, next.Version); err != nil {
		return next, err
	}
	// Audit metadata and palettes without duplicating uploaded binaries in the log.
	summary := func(s branding.Settings) any {
		assets := map[string]any{}
		for k, a := range s.Assets {
			assets[k] = map[string]any{"mime": a.MIME, "bytes": len(a.Data)}
		}
		return map[string]any{"name": s.Name, "description": s.Description, "contact_email": s.ContactEmail, "site_max_width": s.SiteMaxWidth, "content_max_width": s.ContentMaxWidth, "sidebar_subsections": s.SidebarSubsections, "dark": s.Dark, "light": s.Light, "presets": s.Presets, "default_preset": s.DefaultPreset, "assets": assets, "version": s.Version}
	}
	if err = auditSystem(ctx, tx, name, "branding", "installation", map[string]any{"before": summary(before), "after": summary(next)}); err != nil {
		return next, err
	}
	return next, tx.Commit()
}
