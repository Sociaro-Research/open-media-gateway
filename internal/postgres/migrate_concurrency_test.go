package postgres

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestMigrationStartChild(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_MIGRATION_CHILD") != "1" {
		return
	}
	db, err := Open(context.Background(), os.Getenv("GATEWAY_TEST_MIGRATION_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
}

// Gateway replicas, or a native gateway and worker, start separate processes.
func TestConcurrentStartsMigrateTheSameDatabase(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	t.Run("new installation", func(t *testing.T) { concurrentStarts(t, dsn, 0) })
	t.Run("update", func(t *testing.T) { concurrentStarts(t, dsn, 96) })
}

func concurrentStarts(t *testing.T, dsn string, existing int64) {
	base, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "migrate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); base.Close() })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if existing > 0 {
		db, err := Open(context.Background(), u.String())
		if err != nil {
			t.Fatal(err)
		}
		goose.SetBaseFS(migrations)
		if err = goose.SetDialect("postgres"); err == nil {
			err = goose.UpTo(db, "migrations", existing)
		}
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	children := make([]*exec.Cmd, 3)
	output := make([]bytes.Buffer, len(children))
	for i := range children {
		children[i] = exec.Command(os.Args[0], "-test.run=^TestMigrationStartChild$", "-test.count=1")
		children[i].Env = append(os.Environ(), "GATEWAY_TEST_MIGRATION_CHILD=1", "GATEWAY_TEST_MIGRATION_DSN="+u.String())
		children[i].Stdout, children[i].Stderr = &output[i], &output[i]
		if err := children[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, child := range children {
		if err := child.Wait(); err != nil {
			t.Errorf("start %d failed while another start migrated: %v\n%s", i+1, err, output[i].String())
		}
	}
	var version int64
	if err := base.QueryRow(`SELECT max(version_id) FROM ` + schema + `.goose_db_version`).Scan(&version); err != nil || version < 98 {
		t.Fatalf("schema not fully migrated: %d %v", version, err)
	}
}
