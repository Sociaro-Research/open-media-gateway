package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/playgroundkey"
)

func playgroundSlot(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner, organization string) (playgroundkey.Record, error) {
	r := playgroundkey.Record{Owner: owner, Organization: organization}
	err := q.QueryRowContext(ctx, `SELECT key_id,ciphertext FROM playground_key_slots WHERE user_id=$1 AND workspace=$2`, owner, organization).Scan(&r.KeyID, &r.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return r, playgroundkey.ErrMissing
	}
	return r, err
}
func (r *APIKeyRepository) PlaygroundKey(ctx context.Context, owner, organization string) (playgroundkey.Record, error) {
	return playgroundSlot(ctx, r.db, owner, organization)
}
func (r *APIKeyRepository) CreatePlaygroundKey(ctx context.Context, owner, organization, request string, key apikey.Key, hash, encrypted []byte) (playgroundkey.Record, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return playgroundkey.Record{}, err
	}
	defer tx.Rollback()
	// Match ordinary issuance/membership lock order, then serialize this account.
	var ignored string
	if organization != "" {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, organization).Scan(&ignored); err != nil {
			return playgroundkey.Record{}, apikey.ErrForbidden
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, owner).Scan(&ignored); err != nil {
		return playgroundkey.Record{}, apikey.ErrForbidden
	}
	current, slotErr := playgroundSlot(ctx, tx, owner, organization)
	if slotErr != nil && !errors.Is(slotErr, playgroundkey.ErrMissing) {
		return current, slotErr
	}
	var priorKey, priorWorkspace string
	err = tx.QueryRowContext(ctx, `SELECT key_id,workspace FROM playground_key_requests WHERE user_id=$1 AND request_id=$2`, owner, request).Scan(&priorKey, &priorWorkspace)
	if err == nil {
		if slotErr != nil || priorKey != current.KeyID || priorWorkspace != organization {
			return current, playgroundkey.ErrConflict
		}
		// A revoked/expired replay is rejected by Manager.open, never re-created.
		return current, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return current, err
	}
	active := false
	if slotErr == nil {
		if err = tx.QueryRowContext(ctx, `SELECT revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp()) FROM user_api_keys WHERE id=$1`, current.KeyID).Scan(&active); err != nil {
			return current, err
		}
	}
	if !active {
		if err = createAPIKey(ctx, tx, owner, key, hash); err != nil {
			return current, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO playground_key_slots(user_id,workspace,key_id,ciphertext) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,workspace) DO UPDATE SET key_id=EXCLUDED.key_id,ciphertext=EXCLUDED.ciphertext`, owner, organization, key.ID, encrypted); err != nil {
			return current, err
		}
		current = playgroundkey.Record{Owner: owner, Organization: organization, KeyID: key.ID, Ciphertext: encrypted}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO playground_key_requests(user_id,request_id,workspace,key_id) VALUES($1,$2,$3,$4)`, owner, request, organization, current.KeyID); err != nil {
		return current, err
	}
	if err = tx.Commit(); err != nil {
		return current, err
	}
	return current, nil
}
