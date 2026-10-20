package reliability_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nativeServices struct {
	DataDir     string `json:"data_dir"`
	Log         string `json:"log"`
	DatabaseURL string `json:"database_url"`
	Options     string `json:"postgres_options"`
	PGCtl       string `json:"pg_ctl"`
	PGDump      string `json:"pg_dump"`
	PGRestore   string `json:"pg_restore"`
}

func controlledNativeServices(t *testing.T) nativeServices {
	t.Helper()
	if os.Getenv("GATEWAY_NATIVE_RECOVERY") != "1" {
		t.Skip("native server restart/restore requires a dedicated recovery run")
	}
	name := os.Getenv("GATEWAY_TEST_NATIVE_SERVICES")
	if name == "" {
		t.Skip("native process/dump recovery requires scripts/with-test-services.py; network outage tests still run in container CI")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var control nativeServices
	if err = json.Unmarshal(data, &control); err != nil {
		t.Fatal(err)
	}
	// Process control is available only for the wrapper's disposable directory.
	// Merely supplying a database URL can never authorize stopping PostgreSQL.
	root := filepath.Dir(name)
	if filepath.Base(name) != "services-control.json" || !strings.HasPrefix(filepath.Base(root), "gateway-isolated-") || control.DataDir != filepath.Join(root, "postgres") || control.DatabaseURL != os.Getenv("GATEWAY_TEST_DATABASE_URL") {
		t.Fatal("native service descriptor is outside the disposable test boundary")
	}
	return control
}

func nativeCommand(ctx context.Context, binary string, args ...string) error {
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", filepath.Base(binary), err, output)
	}
	return nil
}

func pendingDurableTasks(t *testing.T, s *testStack, count int) []string {
	t.Helper()
	ids := make([]string, count)
	for i := range ids {
		ids[i] = s.submit("durable-recovery", fmt.Sprint(i), false)
	}
	eventually(t, s.ctx, "provider IDs were not committed", func() bool {
		var n int
		err := s.db.QueryRowContext(s.ctx, "SELECT count(*) FROM tasks WHERE provider_task_id IS NOT NULL").Scan(&n)
		return err == nil && n == count
	})
	return ids
}

func verifyRecoveredTasks(t *testing.T, s *testStack, ids []string) {
	t.Helper()
	s.fixture.release()
	for _, id := range ids {
		s.checkFile(s.terminal(id))
		if s.fixture.job(id).Submissions != 1 {
			t.Fatal("database recovery repeated provider submission", id)
		}
	}
	s.checkFile(s.terminal(s.submit("healthy-after-database-recovery", "after", false)))
}

func TestReliabilityPostgresImmediateStopAndWALRecovery(t *testing.T) {
	control := controlledNativeServices(t)
	s := newStack(t, 2*time.Minute)
	s.personalKey()
	s.fixture.ready = false
	s.startWorkers(2, 100*time.Millisecond)
	ids := pendingDurableTasks(t, s, 20)
	running := true
	t.Cleanup(func() {
		if !running {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := nativeCommand(ctx, control.PGCtl, "-D", control.DataDir, "-l", control.Log, "-o", control.Options, "-w", "start"); err != nil {
				t.Error(err)
			}
		}
	})
	if err := nativeCommand(s.ctx, control.PGCtl, "-D", control.DataDir, "-m", "immediate", "-w", "stop"); err != nil {
		t.Fatal(err)
	}
	running = false
	status, _, err := s.request("GET", "/healthz", "", "")
	if err != nil || status != 503 {
		t.Fatalf("health hid stopped PostgreSQL: %d %v", status, err)
	}
	if err := nativeCommand(s.ctx, control.PGCtl, "-D", control.DataDir, "-l", control.Log, "-o", control.Options, "-w", "start"); err != nil {
		t.Fatal(err)
	}
	running = true
	verifyRecoveredTasks(t, s, ids)
	t.Log("20 committed provider IDs survived immediate PostgreSQL shutdown and WAL recovery; all results verified, no resubmission")
}

func TestReliabilityPostgresDumpAndRestore(t *testing.T) {
	control := controlledNativeServices(t)
	s := newStack(t, 90*time.Second)
	s.personalKey()
	s.fixture.ready = false
	stop := s.startWorkers(2, 100*time.Millisecond)
	ids := pendingDurableTasks(t, s, 30)
	stop()
	s.workers.Wait()
	var schema string
	if err := s.db.QueryRow("SELECT current_schema()").Scan(&schema); err != nil || !strings.HasPrefix(schema, "reliability_") {
		t.Fatalf("unsafe restore schema %q: %v", schema, err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot.dump")
	if err := nativeCommand(s.ctx, control.PGDump, "--dbname", control.DatabaseURL, "--format=custom", "--schema", schema, "--file", backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backup, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(backup)
	if err != nil || !strings.HasPrefix(string(data), "PGDMP") {
		t.Fatalf("unreadable custom-format backup: %v", err)
	}
	if err := nativeCommand(s.ctx, control.PGRestore, "--list", backup); err != nil {
		t.Fatal(err)
	}
	// Delete only this test's schema, never a whole database or another schema.
	if _, err := s.db.ExecContext(s.ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err := nativeCommand(s.ctx, control.PGRestore, "--exit-on-error", "--dbname", control.DatabaseURL, backup); err != nil {
		t.Fatal(err)
	}
	s.startWorkers(2, 100*time.Millisecond)
	verifyRecoveredTasks(t, s, ids)
	digest := sha256.Sum256(data)
	t.Logf("custom-format backup (%d bytes, SHA-256 %s) restored 30 durable tasks and API authorization; all results verified", len(data), hex.EncodeToString(digest[:]))
}
