package postgres

import (
	"context"
	"database/sql"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

// The snapshot belongs to the accepted task. Historical tasks without a rate
// retain zero markup even when an organization's settings change.
func applyTaskMarkup(ctx context.Context, db rowQuerier, task *core.Task, lock bool) error {
	if task.Cost == nil {
		return nil
	}
	task.Cost.MarkupPercent = 0
	if task.ProviderOwnerID != identity.SharedOwnerID {
		return nil
	}
	policy, err := readMarkupPolicy(ctx, db, task.OwnerID, lock)
	if err != nil {
		return err
	}
	// Personal accounts have no organization pricing, even for shared resources.
	if policy.Company.Version == 0 {
		return nil
	}
	task.Cost.MarkupPercent = policy.Resolve(task.Provider, task.Model).Percent
	_, err = task.Cost.CustomerExpected()
	return err
}
func (r *TaskRepository) ApplyMarkup(ctx context.Context, task *core.Task) error {
	if task.Cost == nil || task.ProviderOwnerID != identity.SharedOwnerID {
		return applyTaskMarkup(ctx, r.db, task, false)
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = applyTaskMarkup(ctx, tx, task, false); err != nil {
		return err
	}
	return tx.Commit()
}
