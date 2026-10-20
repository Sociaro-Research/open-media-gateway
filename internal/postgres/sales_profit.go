package postgres

import (
	"context"
	"database/sql"
)

// Assignment, hierarchy and commission-setting writes take the exclusive version
// of this lock. Take it before task/key locks so first recognition has one owner.
func lockProfitAssignment(ctx context.Context, tx *sql.Tx) error {
	var ignored bool
	return tx.QueryRowContext(ctx, `SELECT id FROM system_settings WHERE id=TRUE FOR SHARE`).Scan(&ignored)
}
