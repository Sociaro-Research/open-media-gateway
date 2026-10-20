package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func catalogJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Include schedule bookkeeping, document contents, history and mail work, not
// just the catalogue version number. Each test owns a fresh migrated schema.
func catalogTables(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"settings", "schedules", "documents", "state", "versions", "runs"} {
		var value string
		err := db.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]')::text FROM catalog_sync_` + table + ` x`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		out[table] = value
	}
	return out
}

func TestCatalogCommitAtomicityAndReplay(t *testing.T) {
	for _, fault := range []string{"documents", "state", "versions", "runs", "commit", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			db := auditDatabase(t, true)
			repo := NewCatalogRepository(db)
			ctx := context.Background()
			now := time.Date(2026, 10, 18, 4, 0, 0, 0, time.UTC)
			schedule := catalogsync.Schedule{Provider: "atomic-fixture", Time: "03:00", Version: 1, Sources: []catalogsync.Source{}}
			if err := repo.EnsureSchedule(ctx, schedule); err != nil {
				t.Fatal(err)
			}
			capture := catalogsync.Capture{Models: []core.MediaModel{{ID: "a"}, {ID: "b"}}, IDs: []string{"a", "b", "unknown"}}
			observations := func(date string) []catalogsync.Observation {
				var out []catalogsync.Observation
				for _, id := range []string{"a", "b"} {
					body := fmt.Sprintf(`{"models":[{"id":%q,"metadata":{"release_date":%q}}]}`, id, date)
					out = append(out, catalogsync.Observation{Source: catalogsync.Source{ID: id, URL: "https://example.invalid/" + id, Parser: "manifest-v1"}, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(body))), Text: body, Entries: []catalogsync.Entry{{ID: id, Metadata: &core.ModelMetadata{ReleaseDate: date}}}})
				}
				return out
			}
			finish := func(run *catalogsync.Run, at time.Time) {
				run.ID = uuid.NewString()
				run.Provider = schedule.Provider
				run.StartedAt = at
				run.CompletedAt = at
			}
			initial, initialRun := catalogsync.Reconcile(catalogsync.State{}, capture, observations("2026-09-01"), now, nil)
			finish(&initialRun, now)
			if initial.Version != 1 || len(initial.Models) != 2 || len(initial.Warnings) != 1 {
				t.Fatalf("invalid baseline: %+v", initial)
			}
			if err := repo.Commit(ctx, schedule, initial, initialRun, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE catalog_sync_schedules SET requested=TRUE`); err != nil {
				t.Fatal(err)
			}
			previous, err := repo.State(ctx, schedule.Provider)
			if err != nil {
				t.Fatal(err)
			}
			capture.Models = append(capture.Models, core.MediaModel{ID: "c"})
			capture.IDs = append(capture.IDs, "c")
			obs := observations("2026-10-01")
			candidate, run := catalogsync.Reconcile(previous, capture, obs, now.Add(time.Hour), nil)
			finish(&run, now.Add(time.Hour))
			if candidate.Version != 2 || len(candidate.Models) != 3 || len(run.Changes) == 0 {
				t.Fatal("invalid candidate")
			}
			input := catalogJSON(t, []any{candidate, run})
			baseline := catalogTables(t, db)
			table := fault
			if fault == "commit" {
				table = "runs"
			}
			if fault != "cancel" {
				if _, err = db.Exec(`CREATE FUNCTION reject_catalog() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic catalogue fault' USING ERRCODE='23514'; END $$`); err != nil {
					t.Fatal(err)
				}
				ddl := `CREATE TRIGGER reject_catalog BEFORE INSERT OR UPDATE ON catalog_sync_` + table + ` FOR EACH ROW EXECUTE FUNCTION reject_catalog()`
				if fault == "documents" {
					// Fail after the first new document was inserted and stripped.
					ddl = fmt.Sprintf(`CREATE TRIGGER reject_catalog BEFORE INSERT ON catalog_sync_documents FOR EACH ROW WHEN (NEW.hash = '%s') EXECUTE FUNCTION reject_catalog()`, run.Sources[1].Hash)
				}
				if fault == "commit" {
					ddl = `CREATE CONSTRAINT TRIGGER reject_catalog AFTER INSERT ON catalog_sync_runs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_catalog()`
				}
				if _, err = db.Exec(ddl); err != nil {
					t.Fatal(err)
				}
				err = repo.Commit(ctx, schedule, candidate, run, now)
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
					t.Fatalf("expected injected SQL failure, got %v", err)
				}
				if _, err = db.Exec(`DROP TRIGGER reject_catalog ON catalog_sync_` + table); err != nil {
					t.Fatal(err)
				}
			} else {
				blocker, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Rollback()
				if _, err = blocker.Exec(`LOCK TABLE catalog_sync_runs IN ACCESS EXCLUSIVE MODE`); err != nil {
					t.Fatal(err)
				}
				var pid int
				if err = blocker.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
					t.Fatal(err)
				}
				call, cancel := context.WithCancel(ctx)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- repo.Commit(call, schedule, candidate, run, now) }()
				deadline := time.Now().Add(5 * time.Second)
				for {
					var waiting bool
					if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE 'INSERT INTO catalog_sync_runs%' AND wait_event_type='Lock')`, pid).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("commit did not reach final journal insert")
					}
					time.Sleep(5 * time.Millisecond)
				}
				// A separate reader must still see the old complete state/version while
				// the transaction has already written their replacements privately.
				visible, e := repo.State(ctx, schedule.Provider)
				if e != nil || catalogJSON(t, visible) != catalogJSON(t, previous) {
					t.Fatal("uncommitted state visible", e)
				}
				versions, e := repo.Versions(ctx, schedule.Provider, 10, 0)
				if e != nil || len(versions) != 1 {
					t.Fatal("uncommitted version visible", e)
				}
				cancel()
				select {
				case err = <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cancel did not return")
				}
				if err = blocker.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			if after := catalogTables(t, db); !reflect.DeepEqual(after, baseline) {
				t.Fatalf("partial publication after %s", fault)
			}
			unchangedInput := catalogJSON(t, []any{candidate, run}) == input
			// Deliberately retry the very same Go values, without refetch/reconcile.
			if err = repo.Commit(ctx, schedule, candidate, run, now); err != nil {
				t.Fatal(err)
			}
			for _, ob := range obs {
				text, e := repo.Document(ctx, ob.Hash)
				if e != nil || text != ob.Text {
					t.Errorf("retry lost source evidence %s: %q, %v", ob.Source.ID, text, e)
				}
			}
			if !unchangedInput || catalogJSON(t, []any{candidate, run}) != input {
				t.Error("Commit mutated caller-owned result; retry input must survive failure and success")
			}
			saved, err := repo.State(ctx, schedule.Provider)
			if err != nil || saved.Version != 2 || len(saved.Models) != 3 || saved.Models["a"].Metadata.ReleaseDate != "2026-10-01" || saved.Models["b"].Metadata.ReleaseDate != "2026-10-01" || saved.Models["c"].Contract == nil {
				t.Fatal("incomplete published models", err)
			}
			if catalogJSON(t, saved.Models) != catalogJSON(t, candidate.Models) || catalogJSON(t, saved.Warnings) != catalogJSON(t, candidate.Warnings) {
				t.Fatal("publication changed model or warning payload")
			}
			for _, ob := range saved.Observations {
				if ob.Text != "" {
					t.Error("inline state text was not stripped")
				}
			}
			versions, err := repo.Versions(ctx, schedule.Provider, 10, 0)
			if err != nil || len(versions) != 2 || catalogJSON(t, versions[0]) != catalogJSON(t, saved) || catalogJSON(t, versions[1]) != catalogJSON(t, previous) {
				t.Fatal("incomplete or changed version history", err)
			}
			runs, err := repo.Runs(ctx, schedule.Provider, 10, 0)
			if err != nil || len(runs) != 2 || runs[0].ID != run.ID || runs[0].Version != 2 {
				t.Fatal("missing or duplicate run", err)
			}
			for _, ob := range runs[0].Sources {
				if ob.Text != "" {
					t.Error("inline run text was not stripped")
				}
			}
			published := catalogTables(t, db)
			// The repository promises fail-closed uniqueness, not success on duplicate
			// Run IDs. A completed-result redelivery must roll back all earlier writes.
			err = repo.Commit(ctx, schedule, candidate, run, now)
			var duplicate *pgconn.PgError
			if !errors.As(err, &duplicate) || duplicate.Code != "23505" {
				t.Fatalf("duplicate run: %v", err)
			}
			if !reflect.DeepEqual(catalogTables(t, db), published) {
				t.Fatal("duplicate result changed publication")
			}
			quiet, quietRun := catalogsync.Reconcile(saved, capture, obs, now.Add(2*time.Hour), nil)
			finish(&quietRun, now.Add(2*time.Hour))
			if quiet.Version != 2 || len(quietRun.Changes) != 0 || len(quietRun.NewWarnings) != 0 || len(quietRun.ResolvedWarnings) != 0 || quietRun.EmailStatus != "not_needed" {
				t.Fatal("same observations reapplied changes", catalogJSON(t, quietRun))
			}
			if len(quiet.Warnings) != 1 || quiet.Warnings[0].Key != previous.Warnings[0].Key || !quiet.Warnings[0].FirstSeen.Equal(previous.Warnings[0].FirstSeen) || quiet.Warnings[0].Occurrences != 3 {
				t.Fatal("warning identity or failed-attempt count", quiet.Warnings)
			}
			if err = repo.Commit(ctx, schedule, quiet, quietRun, now); err != nil {
				t.Fatal(err)
			}
			final := catalogTables(t, db)
			if final["versions"] != published["versions"] || final["documents"] != published["documents"] {
				t.Fatal("quiet replay changed version/evidence")
			}
			var docs int
			if err = db.QueryRow(`SELECT COUNT(*) FROM catalog_sync_documents`).Scan(&docs); err != nil || docs != 4 {
				t.Fatal("document deduplication", docs, err)
			}
			t.Logf("%s: six tables rolled back; identical retry published complete v2/three models/four documents; duplicate run rejected; quiet check retained v2 and warning identity", fault)
			if strings.Contains(final["state"], `"text":`) {
				t.Fatal("state contains inline evidence")
			}
		})
	}
}
