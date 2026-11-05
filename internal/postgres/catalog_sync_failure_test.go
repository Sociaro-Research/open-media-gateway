package postgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCatalogRepositoryClassifiesSharedFailures(t *testing.T) {
	for _, code := range []string{"08006", "25006", "53300", "57P01", "57P03", "58030"} {
		t.Run(code, func(t *testing.T) {
			original := &pgconn.PgError{Code: code}
			err := catalogRepositoryError(fmt.Errorf("wrapped: %w", original))
			if !errors.Is(err, catalogsync.ErrRepositoryUnavailable) || !errors.Is(err, original) {
				t.Fatal("shared failure lost", err)
			}
		})
	}
	if !errors.Is(catalogRepositoryError(driver.ErrBadConn), catalogsync.ErrRepositoryUnavailable) {
		t.Fatal("bad connection not shared")
	}
	for _, original := range []error{nil, system.ErrConflict, context.Canceled, context.DeadlineExceeded, fmt.Errorf("admission timeout: %w", context.DeadlineExceeded), errors.New("invalid provider state"), &pgconn.PgError{Code: "23514"}, &pgconn.PgError{Code: "57014"}} {
		if err := catalogRepositoryError(original); err != original {
			t.Fatalf("local failure misclassified: %v", err)
		}
	}
}
func TestCatalogRepositoryReadOnlyOutageKeepsEvidence(t *testing.T) {
	db := auditDatabase(t, true)
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	repo := NewCatalogRepository(db)
	schedule := catalogsync.Schedule{Provider: "fixture", Enabled: true, Time: "03:00", Version: 1, Sources: []catalogsync.Source{}}
	if err := repo.EnsureSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`SET default_transaction_read_only=on`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.Exec(`SET default_transaction_read_only=off`); err != nil {
			t.Error(err)
		}
	}()
	if _, err := repo.Settings(ctx); err != nil {
		t.Fatal("read-only outage should still allow settings reads", err)
	}
	run := catalogsync.Run{ID: "fixture", Provider: "fixture", CompletedAt: time.Now()}
	if err := repo.Commit(ctx, schedule, catalogsync.State{Version: 9}, run, time.Now()); !errors.Is(err, catalogsync.ErrRepositoryUnavailable) {
		t.Fatal("write outage not classified", err)
	}
	if err := repo.EnsureSchedule(ctx, schedule); !errors.Is(err, catalogsync.ErrRepositoryUnavailable) {
		t.Fatal("bootstrap outage not classified", err)
	}
	state, err := repo.State(ctx, "fixture")
	if err != nil || state.Version != 0 {
		t.Fatal("failed publication changed evidence", state, err)
	}
	schedules, err := repo.Schedules(ctx)
	if err != nil || len(schedules) != 1 || schedules[0].LastCheck != nil {
		t.Fatal("failed publication marked checked", schedules, err)
	}
}
