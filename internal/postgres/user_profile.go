package postgres

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
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
	var tables, wide any
	if in.TableColumns != nil {
		raw, err := json.Marshal(*in.TableColumns)
		if err != nil {
			return identity.User{}, err
		}
		tables = string(raw)
	}
	if in.TableWide != nil {
		raw, err := json.Marshal(*in.TableWide)
		if err != nil {
			return identity.User{}, err
		}
		wide = string(raw)
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return identity.User{}, err
	}
	defer tx.Rollback()
	if err = guardCompanyMutation(ctx, tx, "", id); err != nil {
		return identity.User{}, err
	}
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
			return identity.User{}, auth.ErrProfileColorPreset
		}
	}
	user, err := scanProfile(tx.QueryRowContext(ctx, `UPDATE users SET
 display_name=COALESCE($2,display_name), custom_display_name=custom_display_name OR $2::text IS NOT NULL,
 theme=COALESCE($3,theme), report_columns=COALESCE($4::jsonb,report_columns), color_preset=COALESCE($5,color_preset), contacts=COALESCE($6::jsonb,contacts), table_columns=jsonb_strip_nulls(table_columns || COALESCE($7::jsonb,'{}'::jsonb)), table_wide=table_wide || COALESCE($8::jsonb,'{}'::jsonb), updated_at=NOW() WHERE id=$1
 RETURNING id,email,display_name,COALESCE(picture_url,''),theme,report_columns,color_preset,contacts,table_columns,table_wide`, id, in.Name, in.Theme, columns, in.ColorPreset, contacts, tables, wide))
	if err != nil {
		return user, err
	}
	return user, tx.Commit()
}

func scanProfile(row rowScanner) (identity.User, error) {
	var user identity.User
	var columns, contacts, tables, wide []byte
	if err := row.Scan(&user.ID, &user.Email, &user.Name, &user.Picture, &user.Theme, &columns, &user.ColorPreset, &contacts, &tables, &wide); err != nil {
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
	if err := json.Unmarshal(tables, &user.TableColumns); err != nil {
		return user, err
	}
	if err := json.Unmarshal(wide, &user.TableWide); err != nil {
		return user, err
	}
	return user, nil
}
