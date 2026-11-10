package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

// This real-PostgreSQL gate must run on a verified disposable fixture. The
// in-memory driver's passes do not replace this SQL/lock/persistence coverage.
func TestPostgresOrganizationAssignment(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("verified disposable PostgreSQL fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	auth, repo := NewAuthRepository(db), NewSystemRepository(db)
	admin, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Assignment administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Assignment target", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	company, err := repo.CreateOrganization(ctx, admin.ID, "Assignment test", admin.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Only fixture-owned rows are removed; never run this against application data.
		for _, query := range []string{`DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, err := db.ExecContext(cleanup, query, company.ID); err != nil {
				t.Error(err)
			}
		}
		if _, err := db.ExecContext(cleanup, `DELETE FROM users WHERE id=$1 OR id=$2`, admin.ID, target.ID); err != nil {
			t.Error(err)
		}
	})
	check := func(wantRole string, wantLimit money.USD) {
		t.Helper()
		var role string
		var limit money.USD
		var active bool
		if err := db.QueryRowContext(ctx, `SELECT role,weekly_limit_micros,removed_at IS NULL FROM organization_members WHERE organization_id=$1 AND user_id=$2`, company.ID, target.ID).Scan(&role, &limit, &active); err != nil || role != wantRole || limit != wantLimit || !active {
			t.Fatalf("role=%s limit=%v active=%v error=%v", role, limit, active, err)
		}
	}
	initial := money.USD(17_000_000)
	if err = repo.AssignOrganizationMember(ctx, admin.ID, company.ID, "nobody-"+target.Email, org.Developer, &initial, true); !errors.Is(err, org.ErrUnknownUser) || !errors.Is(err, org.ErrNotFound) {
		t.Fatal("unknown email must say no account uses it", err)
	}
	if err = repo.AssignOrganizationMember(ctx, admin.ID, company.ID, target.Email, org.Developer, &initial, true); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []string{target.ID, strings.ToUpper(target.Email)} {
		for _, role := range []string{org.Developer, org.MemberRole} {
			changed := money.USD(99_000_000)
			if err = repo.AssignOrganizationMember(ctx, admin.ID, company.ID, lookup, role, &changed, true); !errors.Is(err, org.ErrAlreadyMember) {
				t.Fatal("active assignment was not rejected", lookup, role, err)
			}
			check(org.Developer, initial)
		}
	}
	if err = repo.SetOrganizationMember(ctx, admin.ID, company.ID, target.ID, org.Supermanager, nil, false, false); err != nil {
		t.Fatal("intentional role edit", err)
	}
	check(org.Supermanager, initial)
	if err = repo.AssignOrganizationMember(ctx, admin.ID, company.ID, target.ID, org.MemberRole, nil, false); !errors.Is(err, org.ErrAlreadyMember) {
		t.Fatal("repeat Assign must report membership, not demotion", err)
	}
	if err = repo.SetOrganizationMember(ctx, admin.ID, company.ID, admin.ID, org.MemberRole, nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetOrganizationMember(ctx, admin.ID, company.ID, target.ID, org.MemberRole, nil, false, false); !errors.Is(err, org.ErrLastSupermanager) || !errors.Is(err, org.ErrConflict) {
		t.Fatal("last Supermanager guard changed", err)
	}
	if err = repo.SetOrganizationMember(ctx, admin.ID, company.ID, admin.ID, org.Supermanager, nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetOrganizationMember(ctx, admin.ID, company.ID, target.ID, org.MemberRole, nil, false, true); err != nil {
		t.Fatal(err)
	}
	def := money.USD(5_000_000)
	if err = repo.OrganizationSettings(ctx, admin.ID, company.ID, company.Name, 0, &def, false); err != nil {
		t.Fatal(err)
	}
	if err = repo.AssignOrganizationMember(ctx, admin.ID, company.ID, target.ID, org.Developer, nil, false); err != nil {
		t.Fatal("removed-member restoration", err)
	}
	check(org.Developer, def)

	// Hold the same organization lock while a winning assignment is uncommitted.
	// A late Assign cannot inspect/overwrite it before that transaction releases.
	if err = repo.SetOrganizationMember(ctx, admin.ID, company.ID, target.ID, org.MemberRole, nil, false, true); err != nil {
		t.Fatal(err)
	}
	winner, err := beginAuditTx(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer winner.Rollback()
	var winnerPID int
	if err = winner.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&winnerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = orgLock(ctx, winner, admin.ID, company.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = winner.ExecContext(ctx, `UPDATE organization_members SET removed_at=NULL,role='developer',weekly_limit_micros=$3 WHERE organization_id=$1 AND user_id=$2`, company.ID, target.ID, initial); err != nil {
		t.Fatal(err)
	}
	// Pin the late repository to one backend so the observation cannot match an
	// unrelated request. This separate pool uses the same disposable fixture.
	lateDB, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lateDB.Close() })
	lateDB.SetMaxOpenConns(1)
	lateDB.SetMaxIdleConns(1)
	var latePID int
	if err = lateDB.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&latePID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- NewSystemRepository(lateDB).AssignOrganizationMember(ctx, admin.ID, company.ID, strings.ToUpper(target.Email), org.MemberRole, nil, false)
	}()
	wait, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case early := <-done:
			t.Fatal("late assignment finished before observed contention", early)
		default:
		}
		var blocked bool
		if err = db.QueryRowContext(wait, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$2 AND $1::int=ANY(pg_blocking_pids(pid)) AND wait_event_type='Lock' AND query LIKE 'SELECT id FROM organizations WHERE id=%FOR NO KEY UPDATE%')`, winnerPID, latePID).Scan(&blocked); err != nil {
			t.Fatal("could not observe late assignment lock contention", err)
		}
		if blocked {
			break
		}
		select {
		case early := <-done:
			t.Fatal("late assignment did not wait for winner", early)
		case <-wait.Done():
			t.Fatal("late assignment never blocked on winner transaction", wait.Err())
		case <-tick.C:
		}
	}
	select {
	case early := <-done:
		t.Fatal("late assignment finished before winner released", early)
	default:
	}
	t.Logf("late backend %d blocked by winner backend %d at organization lock", latePID, winnerPID)
	if err = winner.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, org.ErrAlreadyMember) {
			t.Fatal("late assignment overwrote winner", err)
		}
	case <-ctx.Done():
		t.Fatal("late assignment did not finish")
	}
	check(org.Developer, initial)
}
