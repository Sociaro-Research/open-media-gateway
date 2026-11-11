package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/google/uuid"
)

func TestProviderAssetPendingSweep(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	// Use the migrated schema, but keep the global background query isolated
	// from concurrent fixtures. Every query stays on this temporary-table session.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err = db.ExecContext(ctx, `CREATE TEMP TABLE provider_assets (LIKE provider_assets INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	repo := NewProviderAssetRepository(db)
	owner, account := uuid.NewString(), uuid.NewString()
	other, otherAccount := uuid.NewString(), uuid.NewString()
	id := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	insert := func(n int, status string) {
		t.Helper()
		o, a := owner, account
		if n%2 == 0 {
			o, a = other, otherAccount
		}
		_, e := db.ExecContext(ctx, `INSERT INTO provider_assets(id,owner_id,actor_id,provider_owner_id,provider,library_scope,asset_id,status,data,updated_at) VALUES($1,$2,'actor',$3,'fixture','scope',$4,$5,'{}',$6)`, id(n), o, a, fmt.Sprintf("native-%d", n), status, stamp)
		if e != nil {
			t.Fatal(e)
		}
	}
	for n := 1; n <= 49; n++ {
		insert(n, "Processing")
	}
	for i, status := range []string{"Active", "Missing", "Disconnected", "Deleting", "Deleted", "Detached", "Failed"} {
		insert(50+i, status)
	}
	page := func(after string, want ...int) []providerassets.Record {
		t.Helper()
		rows, e := repo.Pending(ctx, 24, after)
		if e != nil || len(rows) != len(want) {
			t.Fatalf("pending after %q: rows=%d want=%d err=%v", after, len(rows), len(want), e)
		}
		for i, n := range want {
			if rows[i].ID != id(n) || rows[i].Asset.ID != fmt.Sprintf("native-%d", n) || rows[i].Status != "Processing" && rows[i].Status != "Unknown" && rows[i].Status != "Creating" {
				t.Fatal("wrong pending binding/order/state", rows[i])
			}
			if n%2 == 0 && (rows[i].Owner != other || rows[i].ProviderOwner != otherAccount) || n%2 != 0 && (rows[i].Owner != owner || rows[i].ProviderOwner != account) {
				t.Fatal("lost account attribution", rows[i])
			}
		}
		return rows
	}
	rangeIDs := func(first, last int) []int {
		var ids []int
		for n := first; n <= last; n++ {
			ids = append(ids, n)
		}
		return ids
	}
	page("", rangeIDs(1, 24)...)
	// A successful still-Processing refresh must not move a binding forward in
	// the sweep. A terminal transition must disappear without an offset skip.
	if _, err = db.ExecContext(ctx, `UPDATE provider_assets SET updated_at=now() WHERE id=$1`, id(1)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE provider_assets SET status='Deleted' WHERE id=$1`, id(25)); err != nil {
		t.Fatal(err)
	}
	insert(0, "Unknown")
	insert(100, "Creating")
	page(id(24), rangeIDs(26, 49)...)
	page(id(49), 100)
	page(id(100))
	page("", rangeIDs(0, 23)...)
	var updated time.Time
	if err = db.QueryRowContext(ctx, `SELECT updated_at FROM provider_assets WHERE id=$1`, id(2)).Scan(&updated); err != nil || !updated.Equal(stamp) {
		t.Fatal("pending traversal changed stored timestamps", updated, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = repo.Pending(canceled, 24, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("pending ignored cancellation", err)
	}
}
