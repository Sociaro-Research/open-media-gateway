package postgres

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/branding"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
)

func (r *AuthRepository) UpdateProfile(ctx context.Context, id string, in auth.ProfileUpdate) (identity.User, error) {
	var contacts any
	if in.Contacts != nil {
		values := *in.Contacts
		if values == nil {
			values = []identity.Contact{}
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return identity.User{}, err
		}
		contacts = string(encoded)
	}
	var columns any
	if in.ReportColumns != nil {
		encoded, err := json.Marshal(*in.ReportColumns)
		if err != nil {
			return identity.User{}, err
		}
		columns = string(encoded)
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return identity.User{}, err
	}
	defer tx.Rollback()
	if err = setAuditActor(ctx, tx, id); err != nil {
		return identity.User{}, err
	}
	if in.ColorPreset != nil && *in.ColorPreset != "" {
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT configuration FROM site_branding WHERE id=TRUE FOR SHARE`).Scan(&raw); err != nil {
			return identity.User{}, err
		}
		settings := branding.Defaults()
		if err = json.Unmarshal(raw, &settings); err != nil {
			return identity.User{}, err
		}
		if !settings.HasPreset(*in.ColorPreset) {
			return identity.User{}, auth.ErrInvalidProfile
		}
	}
	user, err := scanProfile(tx.QueryRowContext(ctx, `UPDATE users SET
 display_name=COALESCE($2,display_name), custom_display_name=custom_display_name OR $2::text IS NOT NULL,
 theme=COALESCE($3,theme), report_columns=COALESCE($4::jsonb,report_columns), color_preset=COALESCE($5,color_preset), contacts=COALESCE($6::jsonb,contacts), updated_at=NOW() WHERE id=$1
 RETURNING id,email,display_name,COALESCE(picture_url,''),theme,report_columns,color_preset,contacts`, id, in.Name, in.Theme, columns, in.ColorPreset, contacts))
	if err != nil {
		return user, err
	}
	return user, tx.Commit()
}

func scanProfile(row rowScanner) (identity.User, error) {
	var user identity.User
	var columns, contacts []byte
	if err := row.Scan(&user.ID, &user.Email, &user.Name, &user.Picture, &user.Theme, &columns, &user.ColorPreset, &contacts); err != nil {
		return user, err
	}
	if len(columns) > 0 {
		if err := json.Unmarshal(columns, &user.ReportColumns); err != nil {
			return user, err
		}
	}
	if err := json.Unmarshal(contacts, &user.Contacts); err != nil {
		return user, err
	}
	return user, nil
}
