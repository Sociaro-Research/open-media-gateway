package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"time"
)

// Recovery uses current authority, never the stored task's credentials. Business
// settings and source availability are not re-admitted for already accepted work.
func (r *TaskRepository) RecoverAcceptedIntent(ctx context.Context, key, intent string) (core.Task, bool, error) {
	var empty core.Task
	if key == "" || intent == "" {
		return empty, false, nil
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return empty, false, e
	}
	defer tx.Rollback()
	if e = guardCompanyMutation(ctx, tx, identity.OrganizationFromContext(ctx).ID); e != nil {
		return empty, false, e
	}
	var storedIntent sql.NullString
	var actor string
	e = tx.QueryRowContext(ctx, `SELECT admission_intent,COALESCE(actor_id::text,'') FROM tasks WHERE owner_id=$1 AND idempotency_key=$2`, identity.OwnerID(ctx), key).Scan(&storedIntent, &actor)
	if errors.Is(e, sql.ErrNoRows) {
		return empty, false, nil
	}
	if e != nil {
		return empty, false, e
	}
	if !storedIntent.Valid {
		return empty, false, nil
	}
	if actor != identity.ActorID(ctx) {
		return empty, false, core.ErrIdempotencyConflict
	}
	// Different wire encodings may still have the same existing normalized request
	// hash. Only exact intent recovers early; other requests follow normal validation.
	if storedIntent.String != intent {
		return empty, false, nil
	}
	if e = lockGenerationKey(ctx, tx, identity.ProviderOwnerID(ctx)); e != nil {
		return empty, false, e
	}
	if keyID := identity.APIKeyID(ctx); keyID != nil {
		if _, _, e = readBudget(ctx, tx, identity.OwnerID(ctx), *keyID, time.Now(), "", false); e != nil {
			return empty, false, e
		}
	}
	var task core.Task
	e = scanTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE owner_id=$1 AND idempotency_key=$2`, identity.OwnerID(ctx), key), &task)
	if e != nil {
		return empty, false, e
	}
	if e = tx.Commit(); e != nil {
		return empty, false, e
	}
	return task, true, nil
}

func (r *TaskRepository) HasAcceptedIntent(ctx context.Context, key string) (bool, error) {
	var found bool
	e := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE owner_id=$1 AND idempotency_key=$2 AND COALESCE(actor_id::text,'')=$3 AND admission_intent IS NOT NULL)`, identity.OwnerID(ctx), key, identity.ActorID(ctx)).Scan(&found)
	return found, e
}
