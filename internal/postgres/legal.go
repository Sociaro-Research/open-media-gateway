package postgres

import (
	"context"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func (r *SystemRepository) LegalDocument(ctx context.Context, kind string) (legal.Document, error) {
	var d legal.Document
	if !legal.ValidKind(kind) {
		return d, system.ErrNotFound
	}
	err := r.db.QueryRowContext(ctx, "SELECT body,version FROM legal_documents WHERE kind=$1", kind).Scan(&d.Text, &d.Version)
	return d, err
}
func (r *SystemRepository) SaveLegalDocument(ctx context.Context, actor, kind string, next legal.Document) (legal.Document, error) {
	if !legal.ValidKind(kind) {
		return next, system.ErrNotFound
	}
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
	var before legal.Document
	if err = tx.QueryRowContext(ctx, "SELECT body,version FROM legal_documents WHERE kind=$1 FOR UPDATE", kind).Scan(&before.Text, &before.Version); err != nil {
		return next, err
	}
	if before.Version != next.Version {
		return next, system.ErrConflict
	}
	next.Text = strings.TrimSpace(strings.ReplaceAll(next.Text, "\r\n", "\n"))
	next.Version++
	if _, err = tx.ExecContext(ctx, "UPDATE legal_documents SET body=$1,version=$2,updated_at=NOW() WHERE kind=$3", next.Text, next.Version, kind); err != nil {
		return next, err
	}
	// Record the change, not a second copy of the document.
	if err = auditSystem(ctx, tx, name, "legal_document", kind, map[string]any{"before_version": before.Version, "version": next.Version, "bytes": len(next.Text), "default": next.Text == ""}); err != nil {
		return next, err
	}
	return next, tx.Commit()
}
