package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/google/uuid"
)

// This driver executes the production repository call sequence, but models only
// the relevant SQL predicates. It is not PostgreSQL/locking proof. In particular,
// an unguarded UPDATE really does overwrite Detached in this model.
type assetMutationModel struct {
	mu                          sync.Mutex
	row                         providerassets.Record
	busy, detachAtLock          bool
	updates, commits, rollbacks int
	updateSQL                   string
}
type assetMutationConnector struct{ model *assetMutationModel }

func (c assetMutationConnector) Connect(context.Context) (driver.Conn, error) {
	return &assetMutationConn{model: c.model}, nil
}
func (c assetMutationConnector) Driver() driver.Driver { return assetMutationDriver{c.model} }

type assetMutationDriver struct{ model *assetMutationModel }

func (d assetMutationDriver) Open(string) (driver.Conn, error) {
	return &assetMutationConn{model: d.model}, nil
}

type assetMutationConn struct{ model *assetMutationModel }

func (c *assetMutationConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (c *assetMutationConn) Close() error              { return nil }
func (c *assetMutationConn) Begin() (driver.Tx, error) { return &assetMutationTx{c.model}, nil }

type assetMutationTx struct{ model *assetMutationModel }

func (t *assetMutationTx) Commit() error {
	t.model.mu.Lock()
	defer t.model.mu.Unlock()
	t.model.commits++
	return nil
}
func (t *assetMutationTx) Rollback() error {
	t.model.mu.Lock()
	defer t.model.mu.Unlock()
	t.model.rollbacks++
	return nil
}

type assetMutationRows struct {
	columns []string
	values  []driver.Value
	done    bool
}

func (r *assetMutationRows) Columns() []string { return r.columns }
func (r *assetMutationRows) Close() error      { return nil }
func (r *assetMutationRows) Next(v []driver.Value) error {
	if r.done {
		return io.EOF
	}
	copy(v, r.values)
	r.done = true
	return nil
}
func (c *assetMutationConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	m := c.model
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.Contains(q, "FROM provider_assets WHERE id=$1 AND owner_id=$2") {
		if args[0].Value != m.row.ID || args[1].Value != m.row.Owner || args[2].Value != m.row.ProviderOwner {
			return nil, errors.New("unexpected identity")
		}
		raw, _ := json.Marshal(m.row.Asset)
		return &assetMutationRows{columns: strings.Split(assetColumns, ","), values: []driver.Value{m.row.ID, m.row.Owner, "actor", m.row.ProviderOwner, m.row.Provider, m.row.Scope, "", m.row.Asset.ID, m.row.Status, raw, time.Unix(100, 0)}}, nil
	}
	if strings.Contains(q, "SELECT EXISTS(SELECT 1 FROM tasks WHERE provider_owner_id=") {
		return &assetMutationRows{columns: []string{"busy"}, values: []driver.Value{m.busy}}, nil
	}
	return nil, errors.New("unexpected query: " + q)
}
func (c *assetMutationConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	m := c.model
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case strings.Contains(q, "pg_advisory_xact_lock"):
		if m.detachAtLock {
			m.row.Status = "Detached"
			m.detachAtLock = false
		}
		return driver.RowsAffected(1), nil
	case strings.Contains(q, "SET status='Deleting'"):
		m.updateSQL = q
		m.updates++
		// Evaluate the production predicate, rather than assuming the desired guard.
		if strings.Contains(q, "status<>'Detached'") && m.row.Status == "Detached" {
			return driver.RowsAffected(0), nil
		}
		m.row.Status = "Deleting"
		return driver.RowsAffected(1), nil
	case strings.Contains(q, "SET status='Detached'"):
		switch m.row.Status {
		case "Creating", "Unknown", "Processing", "Deleting", "Deleted", "Detached":
			return driver.RowsAffected(0), nil
		}
		m.row.Status = "Detached"
		return driver.RowsAffected(1), nil
	case strings.Contains(q, "SET status='Deleted'"):
		if m.row.Status != "Failed" || m.row.Asset.ID != "" {
			return driver.RowsAffected(0), nil
		}
		m.row.Status = "Deleted"
		return driver.RowsAffected(1), nil
	case strings.Contains(q, "SET asset_id=$1,status=$2,data=$3"):
		if args[0].Value != m.row.Asset.ID {
			return driver.RowsAffected(0), nil
		}
		m.row.Status = args[1].Value.(string)
		return driver.RowsAffected(1), nil
	}
	return nil, errors.New("unexpected exec: " + q)
}

type assetDeleteMarker struct{}
type assetMutationClient struct {
	providerassets.Client
	mu     sync.Mutex
	calls  int
	lastID string
}

func (*assetMutationClient) Scope() string { return "scope" }
func (c *assetMutationClient) Delete(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.lastID = id
	return nil
}

type assetMutationDirectory struct {
	client           *assetMutationClient
	entered, release chan struct{}
	once, openOnce   sync.Once
}

func (d *assetMutationDirectory) AssetLibrary(ctx context.Context, _ string) (providerassets.Client, error) {
	if ctx.Value(assetDeleteMarker{}) == true {
		d.once.Do(func() { close(d.entered) })
		select {
		case <-d.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return d.client, nil
}
func (d *assetMutationDirectory) open() { d.openOnce.Do(func() { close(d.release) }) }
func assetMutationContext(t *testing.T) (context.Context, providerassets.Record) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	owner, account := uuid.NewString(), uuid.NewString()
	ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, owner), account, account)
	return ctx, providerassets.Record{ID: uuid.NewString(), Owner: owner, ProviderOwner: account, Provider: "fixture", Scope: "scope", Asset: providerassets.Asset{ID: "native-fixture", Status: "Active"}}
}
func exerciseDetachDelete(t *testing.T, ctx context.Context, repo *ProviderAssetRepository, row providerassets.Record) {
	t.Helper()
	client := &assetMutationClient{}
	directory := &assetMutationDirectory{client: client, entered: make(chan struct{}), release: make(chan struct{})}
	defer directory.open()
	svc := providerassets.New(repo, directory)
	done := make(chan error, 1)
	go func() { done <- svc.Delete(context.WithValue(ctx, assetDeleteMarker{}, true), row.Provider, row.ID) }()
	select {
	case <-directory.entered:
	case <-ctx.Done():
		t.Fatal("Delete did not pause after initial Get")
	}
	if err := svc.Detach(ctx, row.Provider, row.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Get(ctx, row.ID)
	if err != nil || stored.Status != "Detached" {
		t.Fatal("Detach did not win", stored.Status, err)
	}
	directory.open()
	select {
	case err = <-done:
	case <-ctx.Done():
		t.Fatal("Delete did not finish")
	}
	stored, readErr := repo.Get(ctx, row.ID)
	t.Logf("delete_error=%v remote_calls=%d final=%s", err, client.calls, stored.Status)
	if !errors.Is(err, providerassets.ErrConflict) || readErr != nil || stored.Status != "Detached" || client.calls != 0 {
		t.Fatalf("Delete crossed successful Detach: err=%v read=%v status=%s remote=%d", err, readErr, stored.Status, client.calls)
	}
}
func TestProductionAssetDeleteDetachInterleaving(t *testing.T) {
	ctx, row := assetMutationContext(t)
	model := &assetMutationModel{row: row}
	db := sql.OpenDB(assetMutationConnector{model})
	defer db.Close()
	exerciseDetachDelete(t, ctx, NewProviderAssetRepository(db), row)
	if model.commits != 1 || model.rollbacks != 1 {
		t.Fatal("unexpected transaction outcomes", model.commits, model.rollbacks)
	}
}
func TestProductionAssetDeleteDetachTransitionPredicates(t *testing.T) {
	for _, status := range []string{"Active", "Deleting", "Deleted", "Failed", "Detached"} {
		t.Run(status, func(t *testing.T) {
			ctx, row := assetMutationContext(t)
			row.Status = status
			model := &assetMutationModel{row: row}
			db := sql.OpenDB(assetMutationConnector{model})
			defer db.Close()
			got, err := NewProviderAssetRepository(db).BeginDelete(ctx, row.ID)
			if got.Asset.ID != row.Asset.ID {
				t.Fatal("native identity changed", got.Asset.ID)
			}
			if status == "Detached" {
				if !errors.Is(err, providerassets.ErrConflict) || model.row.Status != "Detached" || model.commits != 0 {
					t.Fatal(err, model.row.Status)
				}
			} else if err != nil || model.row.Status != "Deleting" {
				t.Fatal("existing native-ID retry changed", err, model.row.Status)
			}
		})
	}
	for _, mode := range []string{"detach-after-read", "busy", "failed-empty", "unknown-empty"} {
		t.Run(mode, func(t *testing.T) {
			ctx, row := assetMutationContext(t)
			model := &assetMutationModel{row: row}
			switch mode {
			case "detach-after-read":
				model.detachAtLock = true
			case "busy":
				model.busy = true
			case "failed-empty":
				model.row.Asset.ID = ""
				model.row.Status = "Failed"
			case "unknown-empty":
				model.row.Asset.ID = ""
				model.row.Status = "Unknown"
			}
			db := sql.OpenDB(assetMutationConnector{model})
			defer db.Close()
			got, err := NewProviderAssetRepository(db).BeginDelete(ctx, row.ID)
			if mode == "failed-empty" {
				if err != nil || got.Status != "Deleted" || got.Asset.ID != "" {
					t.Fatal(got, err)
				}
			} else if !errors.Is(err, providerassets.ErrConflict) {
				t.Fatal("guard failed", mode, got, err)
			}
			if mode == "detach-after-read" && model.row.Status != "Detached" {
				t.Fatal("stale read overwrote detach")
			}
		})
	}
}

func TestProductionAssetDeleteDetachSequentialControls(t *testing.T) {
	for _, detachFirst := range []bool{true, false} {
		ctx, row := assetMutationContext(t)
		model := &assetMutationModel{row: row}
		db := sql.OpenDB(assetMutationConnector{model})
		client := &assetMutationClient{}
		directory := &assetMutationDirectory{client: client, entered: make(chan struct{}), release: make(chan struct{})}
		svc := providerassets.New(NewProviderAssetRepository(db), directory)
		if detachFirst {
			if err := svc.Detach(ctx, row.Provider, row.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.Delete(ctx, row.Provider, row.ID); !errors.Is(err, providerassets.ErrNotFound) || client.calls != 0 || model.row.Status != "Detached" {
				t.Fatal("sequential detached deletion changed", err, client.calls, model.row.Status)
			}
		} else {
			if err := svc.Delete(ctx, row.Provider, row.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.Detach(ctx, row.Provider, row.ID); !errors.Is(err, providerassets.ErrConflict) || client.calls != 1 || client.lastID != row.Asset.ID || model.row.Status != "Deleted" {
				t.Fatal("reverse ordering changed", err, client.calls, model.row.Status)
			}
		}
		db.Close()
	}
}

// Actual PostgreSQL coverage is intentionally opt-in; never substitute the model
// above for this transaction/predicate execution against a disposable fixture.
func TestPostgresAssetDeleteDetachInterleaving(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("verified disposable PostgreSQL fixture required")
	}
	ctx, row := assetMutationContext(t)
	// Allow schema setup its own bounded budget; no deadline is relaxed in
	// production. This test is never run without the disposable fixture flag.
	fixture, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = identity.WithResourceOwners(identity.WithOwnerID(fixture, row.Owner), row.ProviderOwner, row.ProviderOwner)
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewProviderAssetRepository(db)
	defer db.ExecContext(context.Background(), "DELETE FROM provider_assets WHERE id=$1", row.ID)
	if _, created, err := repo.Reserve(ctx, row); err != nil || !created {
		t.Fatal(created, err)
	}
	exerciseDetachDelete(t, ctx, repo, row)
}
