package postgres

import (
	"context"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func (r *SystemRepository) LegalFooter(ctx context.Context) (legal.Footer, error) {
	var f legal.Footer
	err := r.db.QueryRowContext(ctx, "SELECT copyright_holder,version FROM legal_footer WHERE id=TRUE").Scan(&f.CopyrightHolder, &f.Version)
	return f, err
}

func (r *SystemRepository) SaveLegalFooter(ctx context.Context, actor string, next legal.Footer) (legal.Footer, error) {
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
	var before legal.Footer
	if err = tx.QueryRowContext(ctx, "SELECT copyright_holder,version FROM legal_footer WHERE id=TRUE FOR UPDATE").Scan(&before.CopyrightHolder, &before.Version); err != nil {
		return next, err
	}
	if before.Version != next.Version {
		return next, system.ErrConflict
	}
	next.CopyrightHolder = strings.TrimSpace(next.CopyrightHolder)
	next.Version++
	if _, err = tx.ExecContext(ctx, "UPDATE legal_footer SET copyright_holder=$1,version=$2,updated_at=NOW() WHERE id=TRUE", next.CopyrightHolder, next.Version); err != nil {
		return next, err
	}
	if err = auditSystem(ctx, tx, name, "legal_footer", "installation", map[string]any{"before": before, "after": next}); err != nil {
		return next, err
	}
	return next, tx.Commit()
}
