package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
)

func (r *SystemRepository) OrganizationSupport(ctx context.Context, actor, id string) (org.Support, error) {
	out := org.Support{}
	if _, err := r.OrganizationAccess(ctx, actor, id); err != nil {
		return out, err
	}
	var person org.SupportPerson
	var contacts []byte
	err := r.db.QueryRowContext(ctx, `SELECT u.id,u.display_name,COALESCE(u.picture_url,''),u.contacts FROM organizations o JOIN users u ON u.id=o.client_manager_id AND u.is_client_manager WHERE o.id=$1`, id).Scan(&person.ID, &person.Name, &person.Picture, &contacts)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(contacts, &person.Contacts); err != nil {
		return out, err
	}
	out.ClientManager = &person
	return out, nil
}
