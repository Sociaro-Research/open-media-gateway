package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/catalogsync"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type CatalogRepository struct{ db *sql.DB }

func NewCatalogRepository(db *sql.DB) *CatalogRepository { return &CatalogRepository{db: db} }
func (r *CatalogRepository) Settings(ctx context.Context) (catalogsync.Settings, error) {
	var s catalogsync.Settings
	err := r.db.QueryRowContext(ctx, `SELECT timezone,email,version FROM catalog_sync_settings WHERE id=TRUE`).Scan(&s.Timezone, &s.Email, &s.Version)
	return s, err
}
func (r *CatalogRepository) SaveSettings(ctx context.Context, actor string, s catalogsync.Settings) (catalogsync.Settings, error) {
	if err := s.Validate(); err != nil {
		return s, err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return s, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return s, err
	}
	if actor == "" {
		return s, system.ErrForbidden
	}
	name, err := systemActor(ctx, tx, actor)
	if err != nil {
		return s, err
	}
	var before catalogsync.Settings
	if err = tx.QueryRowContext(ctx, `SELECT timezone,email,version FROM catalog_sync_settings WHERE id=TRUE FOR UPDATE`).Scan(&before.Timezone, &before.Email, &before.Version); err != nil {
		return s, err
	}
	if s.Version != before.Version {
		return s, system.ErrConflict
	}
	s.Version++
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_sync_settings SET timezone=$1,email=$2,version=$3 WHERE id=TRUE`, s.Timezone, s.Email, s.Version); err != nil {
		return s, err
	}
	if before.Timezone != s.Timezone {
		rows, e := tx.QueryContext(ctx, `SELECT provider,clock FROM catalog_sync_schedules ORDER BY provider FOR UPDATE`)
		if e != nil {
			return s, e
		}
		type target struct{ provider, clock string }
		var targets []target
		for rows.Next() {
			var v target
			if e = rows.Scan(&v.provider, &v.clock); e != nil {
				rows.Close()
				return s, e
			}
			targets = append(targets, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return s, e
		}
		for _, v := range targets {
			if _, err = tx.ExecContext(ctx, `UPDATE catalog_sync_schedules SET next_check=$2,version=version+1 WHERE provider=$1`, v.provider, catalogsync.NextDaily(time.Now(), v.clock, s.Timezone)); err != nil {
				return s, err
			}
		}
	}
	if err = auditSystem(ctx, tx, name, "catalog_sync_settings", "", map[string]any{"before": before, "after": s}); err != nil {
		return s, err
	}
	return s, tx.Commit()
}
func (r *CatalogRepository) Schedules(ctx context.Context) ([]catalogsync.Schedule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT provider,enabled,clock,sources,version,next_check,last_check,requested FROM catalog_sync_schedules ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []catalogsync.Schedule{}
	for rows.Next() {
		var s catalogsync.Schedule
		var data []byte
		if err = rows.Scan(&s.Provider, &s.Enabled, &s.Time, &data, &s.Version, &s.NextCheck, &s.LastCheck, &s.Requested); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &s.Sources); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}
func (r *CatalogRepository) EnsureSchedule(ctx context.Context, s catalogsync.Schedule) error {
	if err := s.Validate(); err != nil {
		return err
	}
	settings, err := r.Settings(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(s.Sources)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO catalog_sync_schedules(provider,enabled,clock,sources,next_check) VALUES($1,$2,$3,$4,$5) ON CONFLICT(provider) DO NOTHING`, s.Provider, s.Enabled, s.Time, data, catalogsync.NextDaily(time.Now(), s.Time, settings.Timezone))
	return err
}
func (r *CatalogRepository) SaveSchedule(ctx context.Context, actor string, s catalogsync.Schedule) (catalogsync.Schedule, error) {
	if err := s.Validate(); err != nil {
		return s, err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return s, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return s, err
	}
	if actor == "" {
		return s, system.ErrForbidden
	}
	name, err := systemActor(ctx, tx, actor)
	if err != nil {
		return s, err
	}
	var zone string
	if err = tx.QueryRowContext(ctx, `SELECT timezone FROM catalog_sync_settings WHERE id=TRUE FOR UPDATE`).Scan(&zone); err != nil {
		return s, err
	}
	data, err := json.Marshal(s.Sources)
	if err != nil {
		return s, err
	}
	next := catalogsync.NextDaily(time.Now(), s.Time, zone)
	result, err := tx.ExecContext(ctx, `UPDATE catalog_sync_schedules SET enabled=$2,clock=$3,sources=$4,next_check=$5,version=version+1 WHERE provider=$1 AND version=$6`, s.Provider, s.Enabled, s.Time, data, next, s.Version)
	if err != nil {
		return s, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return s, err
	}
	if n != 1 {
		return s, system.ErrConflict
	}
	if err = auditSystem(ctx, tx, name, "catalog_sync_schedule", s.Provider, map[string]any{"time": s.Time, "enabled": s.Enabled, "sources": s.Sources}); err != nil {
		return s, err
	}
	s.Version++
	s.NextCheck = &next
	return s, tx.Commit()
}
func (r *CatalogRepository) Request(ctx context.Context, actor, provider string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	if actor == "" {
		return system.ErrForbidden
	}
	name, err := systemActor(ctx, tx, actor)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE catalog_sync_schedules SET requested=TRUE WHERE provider=$1`, provider)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return system.ErrNotFound
	}
	if err = auditSystem(ctx, tx, name, "catalog_sync_requested", provider, nil); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *CatalogRepository) State(ctx context.Context, provider string) (catalogsync.State, error) {
	s := catalogsync.State{Models: map[string]catalogsync.Entry{}, Observations: map[string]catalogsync.Observation{}, Warnings: []catalogsync.Warning{}}
	var data []byte
	err := r.db.QueryRowContext(ctx, `SELECT payload FROM catalog_sync_state WHERE provider=$1`, provider).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}
func (r *CatalogRepository) Commit(ctx context.Context, schedule catalogsync.Schedule, state catalogsync.State, run catalogsync.Run, next time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Compare the configuration again, so edits while fetching cannot publish
	// an obsolete set of sources or overwrite the newly selected schedule.
	var zone string
	if err = tx.QueryRowContext(ctx, `SELECT timezone FROM catalog_sync_settings WHERE id=TRUE FOR UPDATE`).Scan(&zone); err != nil {
		return err
	}
	next = catalogsync.NextDaily(run.CompletedAt, schedule.Time, zone)
	res, err := tx.ExecContext(ctx, `UPDATE catalog_sync_schedules SET next_check=$2,last_check=$3,requested=FALSE WHERE provider=$1 AND version=$4`, schedule.Provider, next, run.CompletedAt, schedule.Version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return system.ErrConflict
	}
	// Strip inline text from local copies: a failed transaction must leave the
	// caller's result intact so retrying it can still persist every document.
	run.Sources = slices.Clone(run.Sources)
	state.Observations = maps.Clone(state.Observations)
	for i, ob := range run.Sources {
		documentHash := ob.Hash
		if ob.FetchedHash != "" {
			documentHash = ob.FetchedHash
		}
		if documentHash != "" && ob.Text != "" {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_sync_documents(hash,content) VALUES($1,$2) ON CONFLICT DO NOTHING`, documentHash, ob.Text); err != nil {
				return err
			}
		}
		run.Sources[i].Text = ""
	}
	for id, ob := range state.Observations {
		ob.Text = ""
		state.Observations[id] = ob
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_sync_state(provider,payload) VALUES($1,$2) ON CONFLICT(provider) DO UPDATE SET payload=EXCLUDED.payload`, schedule.Provider, payload); err != nil {
		return err
	}
	if state.Version > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_sync_versions(provider,version,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, schedule.Provider, state.Version, payload); err != nil {
			return err
		}
	}
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_sync_runs(id,provider,completed_at,payload,email_status) VALUES($1,$2,$3,$4,$5)`, run.ID, run.Provider, run.CompletedAt, data, run.EmailStatus); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *CatalogRepository) Runs(ctx context.Context, provider string, limit, offset int) ([]catalogsync.Run, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload,email_status FROM catalog_sync_runs WHERE ($1='' OR provider=$1) ORDER BY completed_at DESC,id DESC LIMIT $2 OFFSET $3`, provider, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []catalogsync.Run{}
	for rows.Next() {
		var run catalogsync.Run
		var data []byte
		var status string
		if err = rows.Scan(&data, &status); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &run); err != nil {
			return nil, err
		}
		run.EmailStatus = status
		result = append(result, run)
	}
	return result, rows.Err()
}
func (r *CatalogRepository) Versions(ctx context.Context, provider string, limit, offset int) ([]catalogsync.State, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM catalog_sync_versions WHERE provider=$1 ORDER BY version DESC LIMIT $2 OFFSET $3`, provider, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []catalogsync.State{}
	for rows.Next() {
		var data []byte
		var state catalogsync.State
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &state); err != nil {
			return nil, err
		}
		result = append(result, state)
	}
	return result, rows.Err()
}
func (r *CatalogRepository) WithLock(ctx context.Context, fn func(context.Context) error) (bool, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(154862751,1)`).Scan(&locked); err != nil || !locked {
		return false, err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(release, `SELECT pg_advisory_unlock(154862751,1)`); err != nil {
			// A session-scoped lock must never return to the reusable pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return true, fn(ctx)
}
func (r *CatalogRepository) Document(ctx context.Context, hash string) (string, error) {
	var text string
	err := r.db.QueryRowContext(ctx, `SELECT content FROM catalog_sync_documents WHERE hash=$1`, hash).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", system.ErrNotFound
	}
	return text, err
}
func (r *CatalogRepository) PendingEmails(ctx context.Context) ([]catalogsync.Run, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM catalog_sync_runs WHERE email_status IN ('pending','retry') AND email_retry_at<=NOW() AND email_attempts<10 ORDER BY completed_at LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []catalogsync.Run{}
	for rows.Next() {
		var run catalogsync.Run
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &run); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}
func (r *CatalogRepository) EmailResult(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE catalog_sync_runs SET email_status=CASE WHEN $2='retry' AND email_attempts>=9 THEN 'failed' ELSE $2 END,email_attempts=email_attempts+1,email_retry_at=NOW()+INTERVAL '15 minutes' WHERE id=$1`, id, status)
	return err
}
