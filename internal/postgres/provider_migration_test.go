package postgres

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestProviderMigrationPreservesLegacyFreeAndPaidTasks(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "provider_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrations)
	if err = goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpTo(db, "migrations", 24); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"legacy", "free_mock", "paid"} {
		var cost any
		provider := "mock"
		if source == "free_mock" {
			cost = `{"expected_usd":0,"actual_usd":0,"status":"confirmed","actual_source":"free_mock"}`
		}
		if source == "paid" {
			provider = "openai"
			cost = `{"expected_usd":0.2,"actual_usd":0.1,"status":"confirmed","actual_source":"invoice"}`
		}
		if _, err = db.Exec(`INSERT INTO tasks(id,status,provider,model,prompt,cost,provider_owner_id,storage_owner_id) VALUES($1,'succeeded',$2,$3,'migration test',$4,'00000000-0000-0000-0000-000000000000','00000000-0000-0000-0000-000000000000')`, uuid.NewString(), provider, source, cost); err != nil {
			t.Fatal(err)
		}
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT model,cost FROM tasks`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var model string
		var raw []byte
		if err = rows.Scan(&model, &raw); err != nil {
			t.Fatal(err)
		}
		var cost core.TaskCost
		if err = json.Unmarshal(raw, &cost); err != nil {
			t.Fatal(err)
		}
		if model == "paid" {
			if cost.ActualSource != "invoice" || cost.ActualUSD == nil || *cost.ActualUSD != 100000 {
				t.Fatal("paid task changed", cost)
			}
		} else if cost.ActualSource != "free_provider" || cost.ActualUSD == nil || *cost.ActualUSD != 0 {
			t.Fatal("free task lost", cost)
		}
		count++
	}
	if err = rows.Err(); err != nil || count != 3 {
		t.Fatal(count, err)
	}
}
