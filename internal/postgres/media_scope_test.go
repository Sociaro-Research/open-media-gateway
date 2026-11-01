package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/google/uuid"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestMediaPreparationChecksConsumerUnderAdmissionLock(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "media_scope_" + uuid.NewString()
	if _, err = base.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewSystemRepository(db)
	if err = repo.Ensure(ctx, []modelaccess.Ref{{Provider: "fixture", Model: "media", Name: "Media"}, {Provider: "fixture", Model: "prepare", Name: "Historical helper"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE model_settings SET enabled_by_default=false WHERE provider='fixture' AND model='prepare'`); err != nil {
		t.Fatal(err)
	}
	task := core.Task{Provider: "fixture", Model: "prepare", PreparationConsumer: "media", PreparationConsumerPublic: "fixture/media"}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkTaskModelAccess(ctx, tx, &task); err != nil {
		tx.Rollback()
		t.Fatal("disabled helper replaced consumer authorization", err)
	}
	if task.Model != "prepare" || task.PublicModel != "fixture/prepare" {
		tx.Rollback()
		t.Fatal("native dispatch/history identity changed")
	}
	changed := make(chan error, 1)
	go func() {
		_, e := db.Exec(`UPDATE model_settings SET enabled_by_default=false WHERE provider='fixture' AND model='media'`)
		changed <- e
	}()
	select {
	case e := <-changed:
		tx.Rollback()
		t.Fatal("revocation did not wait for consumer admission lock", e)
	case <-time.After(80 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-changed; err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	task = core.Task{Provider: "fixture", Model: "prepare", PreparationConsumer: "media", PreparationConsumerPublic: "fixture/media"}
	if err = checkTaskModelAccess(ctx, tx, &task); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal("revoked consumer admitted preparation", err)
	}
}
