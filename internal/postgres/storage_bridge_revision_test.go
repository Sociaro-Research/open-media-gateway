package postgres

import (
	"context"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

// Real legacy writes and rollback/re-upgrade must not reuse a captured storage
// epoch, including mutation of ciphertext under the same connection ID.
func TestStorageBridgeRevisionLegacyWritesAndMigrationCycle(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 92); err != nil {
		t.Fatal(err)
	}
	owner, connection := uuid.NewString(), uuid.NewString()
	ctx := identity.WithOwnerID(context.Background(), owner)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO storage_connections(owner_id,id,config_ciphertext) VALUES($1,$2,$3)`, owner, connection, []byte("fixture-original"))
	exec(`INSERT INTO storage_settings(owner_id,driver,connection_id) VALUES($1,'s3',$2)`, owner, connection)
	if err := goose.UpTo(db, "migrations", 93); err != nil {
		t.Fatal(err)
	}
	repo := NewStorageSettingsRepository(db)
	read := func() storage.SettingsRecord {
		t.Helper()
		r, e := repo.Get(ctx)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	initial := read()
	if initial.Revision <= 0 || initial.ConnectionRevision <= 0 || string(initial.Ciphertext) != "fixture-original" {
		t.Fatal("legacy data or epochs missing")
	}
	// The same legacy UPSERT used by Save changes both epochs without caller CAS.
	if err := repo.Save(ctx, storage.SettingsRecord{Driver: "s3", ConnectionID: connection, Ciphertext: []byte("fixture-rotated"), Revision: initial.Revision, ConnectionRevision: initial.ConnectionRevision}); err != nil {
		t.Fatal(err)
	}
	changed := read()
	if changed.Revision <= initial.Revision || changed.ConnectionRevision <= initial.ConnectionRevision {
		t.Fatal("same-ID rotation reused epoch")
	}
	historical, e := repo.Connection(ctx, connection)
	if e != nil || historical.ConnectionRevision != changed.ConnectionRevision {
		t.Fatal("connection projection lost revision", e)
	}
	// Every SQL mutation path is covered, including old executables and imports.
	exec(`UPDATE storage_connections SET config_ciphertext=$2,revision=1 WHERE id=$1`, connection, []byte("fixture-import"))
	imported := read()
	if imported.ConnectionRevision <= changed.ConnectionRevision || imported.Revision != changed.Revision {
		t.Fatal("raw writer bypassed connection epoch")
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE storage_settings SET driver='local',connection_id=NULL WHERE owner_id=$1`, owner); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	if got := read(); got.Revision != imported.Revision || got.Driver != "s3" {
		t.Fatal("rolled-back settings became visible")
	}
	if err := goose.DownTo(db, "migrations", 92); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE storage_connections SET config_ciphertext=$2 WHERE id=$1`, connection, []byte("fixture-old-writer"))
	downgraded := read()
	if downgraded.ConnectionRevision <= imported.ConnectionRevision {
		t.Fatal("rollback disabled old-writer fencing")
	}
	if err := goose.UpTo(db, "migrations", 93); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Revision != downgraded.Revision || got.ConnectionRevision != downgraded.ConnectionRevision || string(got.Ciphertext) != "fixture-old-writer" {
		t.Fatal("re-upgrade lost data/epochs")
	}
	exec(`DELETE FROM storage_settings WHERE owner_id=$1`, owner)
	exec(`DELETE FROM storage_connections WHERE owner_id=$1 AND id=$2`, owner, connection)
	exec(`INSERT INTO storage_connections(owner_id,id,config_ciphertext,revision) VALUES($1,$2,$3,1)`, owner, connection, []byte("fixture-replacement"))
	exec(`INSERT INTO storage_settings(owner_id,driver,connection_id,revision) VALUES($1,'s3',$2,1)`, owner, connection)
	replaced := read()
	if replaced.Revision <= downgraded.Revision || replaced.ConnectionRevision <= downgraded.ConnectionRevision {
		t.Fatal("delete/reinsert resurrected old epoch")
	}
}
