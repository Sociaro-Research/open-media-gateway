package postgres

import (
	"context"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func readEmailDelivery(ctx context.Context, q rowQuerier, lock bool) (auth.DeliveryRecord, error) {
	query := `SELECT provider,sender,key_ciphertext,version FROM email_delivery_settings WHERE id=TRUE`
	if lock {
		query += ` FOR UPDATE`
	}
	var r auth.DeliveryRecord
	err := q.QueryRowContext(ctx, query).Scan(&r.Provider, &r.From, &r.KeyCiphertext, &r.Version)
	return r, err
}
func (r *SystemRepository) EmailDelivery(ctx context.Context) (auth.DeliveryRecord, error) {
	return readEmailDelivery(ctx, r.db, false)
}
func (r *SystemRepository) SaveEmailDelivery(ctx context.Context, actor string, next auth.DeliveryRecord) (auth.DeliveryRecord, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	// Match administrator-role updates' lock order and recheck authorization at save.
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return next, err
	}
	if actor == "" {
		return next, system.ErrForbidden
	}
	_, err = systemActor(ctx, tx, actor)
	if err != nil {
		return next, err
	}
	before, err := readEmailDelivery(ctx, tx, true)
	if err != nil {
		return next, err
	}
	if before.Version != next.Version {
		return next, system.ErrConflict
	}
	next.Version++
	_, err = tx.ExecContext(ctx, `UPDATE email_delivery_settings SET provider=$1,sender=$2,key_ciphertext=$3,version=$4,updated_at=NOW() WHERE id=TRUE`, next.Provider, next.From, next.KeyCiphertext, next.Version)
	if err != nil {
		return next, err
	}
	// Neither plaintext nor ciphertext belongs in the activity log.
	return next, tx.Commit()
}
func (r *SystemRepository) BootstrapEmailDelivery(ctx context.Context, next auth.DeliveryRecord) error {
	// Legacy environment variables seed an untouched installation once. They must
	// never override console edits or restore a connection an admin removed.
	_, err := r.db.ExecContext(ctx, `UPDATE email_delivery_settings SET provider=$1,sender=$2,key_ciphertext=$3,version=2,updated_at=NOW() WHERE id=TRUE AND version=1 AND provider=''`, next.Provider, next.From, next.KeyCiphertext)
	return err
}
