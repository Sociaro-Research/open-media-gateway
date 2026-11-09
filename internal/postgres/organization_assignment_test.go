package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

// This driver models the existing organization lock and relevant membership SQL.
// It executes the real repository, but is not PostgreSQL execution/locking proof.
type assignmentModel struct {
	mu                  sync.Mutex
	lock                chan struct{}
	lockAttempts        chan struct{}
	active              bool
	role, access        string
	limit, defaultLimit *money.USD
	supermanagers       int64
	writes              int
	insertEntered       chan struct{}
	insertRelease       chan struct{}
	insertOnce          sync.Once
}

func newAssignmentModel(active bool) *assignmentModel {
	limit, def := money.USD(17_000_000), money.USD(5_000_000)
	m := &assignmentModel{lock: make(chan struct{}, 1), lockAttempts: make(chan struct{}, 8), active: active, role: org.Developer, access: "system_admin", limit: &limit, defaultLimit: &def, supermanagers: 1}
	m.lock <- struct{}{}
	return m
}

type assignmentConnector struct{ model *assignmentModel }

func (c assignmentConnector) Connect(context.Context) (driver.Conn, error) {
	return &assignmentConn{model: c.model}, nil
}
func (c assignmentConnector) Driver() driver.Driver { return assignmentDriver{c.model} }

type assignmentDriver struct{ model *assignmentModel }

func (d assignmentDriver) Open(string) (driver.Conn, error) {
	return &assignmentConn{model: d.model}, nil
}

type assignmentConn struct {
	model  *assignmentModel
	locked bool
	write  bool
	active bool
	role   string
	limit  *money.USD
}

func (c *assignmentConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (c *assignmentConn) Close() error { return nil }
func (c *assignmentConn) Begin() (driver.Tx, error) {
	c.write = false
	return &assignmentTx{c}, nil
}

type assignmentTx struct{ conn *assignmentConn }

func (tx *assignmentTx) finish(commit bool) error {
	c := tx.conn
	if commit && c.write {
		c.model.mu.Lock()
		c.model.active, c.model.role, c.model.limit = c.active, c.role, c.limit
		c.model.writes++
		c.model.mu.Unlock()
	}
	if c.locked {
		c.locked = false
		c.model.lock <- struct{}{}
	}
	return nil
}
func (tx *assignmentTx) Commit() error   { return tx.finish(true) }
func (tx *assignmentTx) Rollback() error { return tx.finish(false) }

type assignmentRows struct {
	columns []string
	values  []driver.Value
	done    bool
}

func (r *assignmentRows) Columns() []string { return r.columns }
func (r *assignmentRows) Close() error      { return nil }
func (r *assignmentRows) Next(dst []driver.Value) error {
	if r.done || r.values == nil {
		return io.EOF
	}
	copy(dst, r.values)
	r.done = true
	return nil
}
func assignmentRow(columns string, values ...driver.Value) driver.Rows {
	return &assignmentRows{columns: strings.Split(columns, ","), values: values}
}
func assignmentLimit(limit *money.USD) driver.Value {
	if limit == nil {
		return nil
	}
	return int64(*limit)
}
func (c *assignmentConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	m := c.model
	if strings.Contains(q, "FROM organizations WHERE id=$1 FOR NO KEY UPDATE") {
		m.lockAttempts <- struct{}{}
		select {
		case <-m.lock:
			c.locked = true
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return assignmentRow("id", args[0].Value), nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case strings.Contains(q, "SELECT CASE WHEN u.is_client_manager"):
		return assignmentRow("role", m.access), nil
	case strings.Contains(q, "SELECT id FROM users WHERE (id::text=$1 OR lower(email)=lower($1))"):
		target := args[0].Value.(string)
		if target != "target-id" && !strings.EqualFold(target, "member@example.test") {
			return assignmentRow("id"), nil
		}
		return assignmentRow("id", "target-id"), nil
	case strings.Contains(q, "SELECT role,weekly_limit_micros FROM organization_members"):
		if !c.locked {
			return nil, errors.New("membership read outside organization lock")
		}
		if !m.active {
			return assignmentRow("role,weekly_limit_micros"), nil
		}
		return assignmentRow("role,weekly_limit_micros", m.role, assignmentLimit(m.limit)), nil
	case strings.Contains(q, "SELECT default_weekly_limit_micros FROM organizations"):
		return assignmentRow("default_weekly_limit_micros", assignmentLimit(m.defaultLimit)), nil
	case strings.Contains(q, "SELECT count(*) FROM organization_members"):
		return assignmentRow("count", m.supermanagers), nil
	}
	return nil, errors.New("unexpected assignment query: " + q)
}
func (c *assignmentConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(q, "set_config('gateway.audit_") {
		return driver.RowsAffected(1), nil
	}
	if !c.locked {
		return nil, errors.New("membership mutation outside organization lock")
	}
	if strings.Contains(q, "INSERT INTO organization_members(") {
		m := c.model
		var hold bool
		if m.insertEntered != nil {
			m.insertOnce.Do(func() { hold = true; close(m.insertEntered) })
		}
		if hold {
			select {
			case <-m.insertRelease:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		c.write, c.active, c.role = true, true, args[2].Value.(string)
		c.limit = nil
		if args[3].Value != nil {
			value := money.USD(args[3].Value.(int64))
			c.limit = &value
		}
		return driver.RowsAffected(1), nil
	}
	return nil, errors.New("unexpected assignment exec: " + q)
}

func assignmentFixture(t *testing.T, active bool) (context.Context, *SystemRepository, *assignmentModel) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	m := newAssignmentModel(active)
	db := sql.OpenDB(assignmentConnector{m})
	t.Cleanup(func() { db.Close() })
	return ctx, NewSystemRepository(db), m
}

func TestOrganizationAssignmentAlreadyActive(t *testing.T) {
	for _, target := range []string{"target-id", "MEMBER@EXAMPLE.TEST"} {
		for _, role := range []string{org.Developer, org.MemberRole} {
			t.Run(target+"/"+role, func(t *testing.T) {
				ctx, repo, model := assignmentFixture(t, true)
				changedLimit := money.USD(99_000_000)
				err := repo.AssignOrganizationMember(ctx, "admin", "company", target, role, &changedLimit, true)
				t.Logf("error=%v role=%s limit=%v writes=%d active=%v", err, model.role, *model.limit, model.writes, model.active)
				if !errors.Is(err, org.ErrAlreadyMember) || model.role != org.Developer || model.limit == nil || *model.limit != 17_000_000 || model.writes != 0 || !model.active {
					t.Fatal("assignment did not preserve active membership")
				}
			})
		}
	}
}

func TestOrganizationAssignmentControls(t *testing.T) {
	for _, state := range []string{"new", "removed"} {
		t.Run(state, func(t *testing.T) {
			ctx, repo, model := assignmentFixture(t, false)
			if state == "new" {
				model.role, model.limit = "", nil
			}
			if err := repo.AssignOrganizationMember(ctx, "admin", "company", "member@example.test", org.ContentOfficer, nil, false); err != nil {
				t.Fatal(err)
			}
			if !model.active || model.role != org.ContentOfficer || model.limit == nil || *model.limit != *model.defaultLimit || model.writes != 1 {
				t.Fatal("new/restored assignment changed", model)
			}
		})
	}
	t.Run("intentional-role-apply", func(t *testing.T) {
		ctx, repo, model := assignmentFixture(t, true)
		if err := repo.SetOrganizationMember(ctx, "admin", "company", "target-id", org.MemberRole, nil, false, false); err != nil {
			t.Fatal(err)
		}
		if model.role != org.MemberRole || *model.limit != 17_000_000 || model.writes != 1 {
			t.Fatal("intentional role update changed", model)
		}
	})
	t.Run("last-supermanager", func(t *testing.T) {
		ctx, repo, model := assignmentFixture(t, true)
		model.role = org.Supermanager
		if err := repo.SetOrganizationMember(ctx, "admin", "company", "target-id", org.MemberRole, nil, false, false); !errors.Is(err, org.ErrConflict) || model.writes != 0 || model.role != org.Supermanager {
			t.Fatal("last Supermanager guard changed", err)
		}
	})
	t.Run("repeat-assign-supermanager", func(t *testing.T) {
		ctx, repo, model := assignmentFixture(t, true)
		model.role = org.Supermanager
		if err := repo.AssignOrganizationMember(ctx, "admin", "company", "MEMBER@EXAMPLE.TEST", org.MemberRole, nil, false); !errors.Is(err, org.ErrAlreadyMember) || model.writes != 0 || model.role != org.Supermanager {
			t.Fatal("repeat assignment showed a demotion error or changed Supermanager", err)
		}
	})
	for _, access := range []string{org.Supermanager, org.Manager, org.MemberRole} {
		t.Run("not-system-admin/"+access, func(t *testing.T) {
			ctx, repo, model := assignmentFixture(t, true)
			model.access = access
			if err := repo.AssignOrganizationMember(ctx, "actor", "company", "target-id", org.MemberRole, nil, false); !errors.Is(err, org.ErrForbidden) || model.writes != 0 {
				t.Fatal("assignment authorization changed", err)
			}
		})
	}
}

func TestOrganizationAssignmentLateRequestCannotOverwriteWinner(t *testing.T) {
	ctx, repo, model := assignmentFixture(t, false)
	model.insertEntered, model.insertRelease = make(chan struct{}), make(chan struct{})
	var release sync.Once
	open := func() { release.Do(func() { close(model.insertRelease) }) }
	defer open()
	winner, late := make(chan error, 1), make(chan error, 1)
	winningLimit := money.USD(23_000_000)
	go func() {
		winner <- repo.AssignOrganizationMember(ctx, "admin", "company", "target-id", org.Developer, &winningLimit, true)
	}()
	select {
	case <-model.insertEntered:
	case <-ctx.Done():
		t.Fatal("winner did not hold its transaction")
	}
	<-model.lockAttempts // First transaction reached the organization lock.
	go func() {
		late <- repo.AssignOrganizationMember(ctx, "admin", "company", "MEMBER@EXAMPLE.TEST", org.MemberRole, nil, false)
	}()
	select {
	case <-model.lockAttempts: // Late request has reached the held lock.
	case <-ctx.Done():
		t.Fatal("late request did not reach lock")
	}
	open()
	for name, result := range map[string]<-chan error{"winner": winner, "late": late} {
		select {
		case err := <-result:
			if name == "winner" && err != nil || name == "late" && !errors.Is(err, org.ErrAlreadyMember) {
				t.Fatalf("%s: %v", name, err)
			}
		case <-ctx.Done():
			t.Fatal(name, "did not finish")
		}
	}
	if model.role != org.Developer || model.limit == nil || *model.limit != winningLimit || model.writes != 1 || !model.active {
		t.Fatal("late request overwrote winner", model)
	}
}
