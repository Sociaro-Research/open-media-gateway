package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	// Keep connections between concurrent polling waves. Discarding all but five
	// on every quiet interval causes repeated TCP handshakes under sustained load.
	db.SetMaxIdleConns(20)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	// Replicas starting together must not run the same migration; the loser would exit.
	ctx := context.Background()
	// The lock keeps one connection for the whole run; goose still needs the caller's pool.
	if limit := db.Stats().MaxOpenConnections; limit > 0 {
		db.SetMaxOpenConns(limit + 1)
		defer db.SetMaxOpenConns(limit)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer conn.Close()
	const key = "hashtext('goose_db_version:' || current_schema())"
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock("+key+")"); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer conn.ExecContext(ctx, "SELECT pg_advisory_unlock("+key+")")
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
