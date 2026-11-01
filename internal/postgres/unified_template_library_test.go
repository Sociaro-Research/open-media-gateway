package postgres

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

func TestUnifiedTemplateLibraryBindingsAndDeletion(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := identity.WithDataOwnerID(context.Background(), templates.LibraryOwnerID)
	refs := NewReferenceRepository(db)
	repo := NewTemplateRepository(db)
	defer repo.Close()
	asset := references.Image{ID: uuid.NewString(), Name: "Shared portrait", ContentType: "image/png", SizeBytes: 12, StorageKey: "fixture/key", SHA256: "fixture-hash", CreatedAt: time.Now()}
	if err := refs.Insert(ctx, asset); err != nil {
		t.Fatal(err)
	}
	records := []templates.Record{}
	for _, title := range []string{"First template", "Second template"} {
		r, err := repo.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: templates.Definition{Title: title, CoverID: asset.ID, Slots: []templates.Slot{{ID: "image:1", AssetID: asset.ID, Type: "image"}}}}, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		r, err = repo.Save(ctx, r, r.Version, "publish")
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	removed := false
	err := refs.DeleteWithGuard(ctx, asset.ID, func() error { removed = true; return nil })
	var used *references.InUseError
	if !errors.As(err, &used) || len(used.Templates) != 2 || removed {
		t.Fatalf("missing full blockers: %v %+v", err, used)
	}
	for _, use := range used.Templates {
		if use.Name == "" || !strings.Contains(use.URL, use.ID) {
			t.Fatal(use)
		}
	}
	for i, r := range records {
		r.Draft.CoverID = ""
		r.Draft.Slots = nil
		r, err = repo.Save(ctx, r, r.Version, "")
		if err != nil {
			t.Fatal(err)
		}
		// Save replaces the current binding immediately; other templates still protect theirs.
		r, err = repo.Save(ctx, r, r.Version, "unpublish")
		if err != nil {
			t.Fatal(err)
		}
		records[i] = r
	}
	if err = refs.DeleteWithGuard(ctx, asset.ID, func() error { removed = true; return nil }); err != nil || !removed {
		t.Fatal(err)
	}
	if _, err = refs.Get(ctx, asset.ID); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal(err)
	}
	records[0].Draft.CoverID = asset.ID
	if _, err = repo.Save(ctx, records[0], records[0].Version, ""); !errors.Is(err, templates.ErrNotFound) {
		t.Fatal("deleted asset rebound", err)
	}
}

func TestUnifiedTemplateDeletionCommittedBeforeStorageAndRetry(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := identity.WithDataOwnerID(context.Background(), templates.LibraryOwnerID)
	refs := NewReferenceRepository(db)
	repo := NewTemplateRepository(db)
	defer repo.Close()
	a := references.Image{ID: uuid.NewString(), Name: "Retry", ContentType: "image/png", SizeBytes: 1, StorageKey: "fixture/retry", CreatedAt: time.Now()}
	if e := refs.Insert(ctx, a); e != nil {
		t.Fatal(e)
	}
	r, e := repo.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: templates.Definition{Title: "Empty draft"}}, 0, "")
	if e != nil {
		t.Fatal(e)
	}
	failure := errors.New("synthetic storage failure")
	e = refs.DeleteWithGuard(ctx, a.ID, func() error {
		r.Draft.CoverID = a.ID
		if _, err := repo.Save(ctx, r, r.Version, ""); !errors.Is(err, templates.ErrNotFound) {
			t.Fatal("pending deletion rebound", err)
		}
		return failure
	})
	if !errors.Is(e, failure) {
		t.Fatal(e)
	}
	if _, e = refs.Get(ctx, a.ID); !errors.Is(e, storage.ErrObjectNotFound) {
		t.Fatal("deleting file selectable", e)
	}
	rows, e := refs.List(ctx, 48, 0)
	if e != nil || len(rows) != 1 || !rows[0].Deleting {
		t.Fatal("retry state lost", rows, e)
	}
	if e = refs.DeleteWithGuard(ctx, a.ID, func() error { return nil }); e != nil {
		t.Fatal(e)
	}
}

func TestUnifiedTemplateMigrationPreservesLegacyIdentity(t *testing.T) {
	db := auditDatabase(t, true)
	id, templateID := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO generation_templates(id,content) VALUES($1,'{"title":"Legacy","slots":null}')`, templateID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO template_media(id,template_id,name,content_type,size_bytes,storage_key,sha256) VALUES($1,$2,'legacy.png','image/png',12,'unchanged-legacy-key','legacy-hash')`, id, templateID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("migrations/079_unified_template_library.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration := strings.Split(string(data), "-- +goose Down")[0]
	for i := 0; i < 2; i++ {
		if _, err = db.Exec(migration); err != nil {
			t.Fatal(err)
		}
	}
	a, err := NewTemplateRepository(db).Asset(context.Background(), id)
	if err != nil || a.ID != id || a.StorageKey != "unchanged-legacy-key" || a.SHA256 != "legacy-hash" {
		t.Fatal(a, err)
	}
	ctx := identity.WithDataOwnerID(context.Background(), templates.LibraryOwnerID)
	if err = NewReferenceRepository(db).DeleteWithGuard(ctx, id, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration); err != nil {
		t.Fatal(err)
	}
	if _, err = NewTemplateRepository(db).Asset(ctx, id); !errors.Is(err, templates.ErrNotFound) {
		t.Fatal("migration resurrected deleted asset", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM reference_images WHERE owner_id=$1 AND id=$2`, templates.LibraryOwnerID, id).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestUnifiedLegacyBytesAndPrivateIsolation(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	legacyCtx := identity.WithDataOwnerID(ctx, identity.SharedOwnerID)
	shared := identity.WithDataOwnerID(ctx, templates.LibraryOwnerID)
	private := identity.WithDataOwnerID(ctx, uuid.NewString())
	data := []byte("synthetic retained bytes")
	privateFile, err := local.StoreReference(private, uuid.NewString(), data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	templateID := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO generation_templates(id,content) VALUES($1,'{"title":"Legacy","slots":null}')`, templateID); err != nil {
		t.Fatal(err)
	}
	ids := []string{uuid.NewString(), uuid.NewString()}
	for _, id := range ids {
		stored, e := local.StoreReference(legacyCtx, id, data, "image/png")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO template_media(id,template_id,name,content_type,size_bytes,storage_key,sha256) VALUES($1,$2,'retained.png','image/png',$3,$4,$5)`, id, templateID, len(data), stored.StorageKey, stored.SHA256); e != nil {
			t.Fatal(e)
		}
	}
	source, err := os.ReadFile("migrations/079_unified_template_library.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(source), "-- +goose Down")[0]
	for i := 0; i < 2; i++ {
		if _, err = db.Exec(up); err != nil {
			t.Fatal(err)
		}
	}
	library := references.New(NewReferenceRepository(db), local)
	for _, id := range ids {
		item, object, e := library.Open(shared, id)
		if e != nil {
			t.Fatal(e)
		}
		actual, e := io.ReadAll(object.Reader)
		object.Reader.Close()
		if e != nil || !bytes.Equal(actual, data) || item.StorageOwnerID != identity.SharedOwnerID {
			t.Fatal("legacy bytes/owner changed", item, e)
		}
		if e = library.Delete(shared, id); e != nil {
			t.Fatal("legacy physical deletion failed", e)
		}
	}
	object, err := local.Open(private, privateFile.StorageKey)
	if err != nil {
		t.Fatal("private file affected", err)
	}
	object.Reader.Close()
	if _, err = db.Exec(up); err != nil {
		t.Fatal(err)
	}
	if rows, e := library.List(shared, 48, 0); e != nil || len(rows) != 0 {
		t.Fatal("upgrade resurrected deleted media", rows, e)
	}
}

func TestUnifiedUploadDeduplicatesAcrossLibraryInstances(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewReferenceRepository(db)
	local, e := storage.NewLocal(t.TempDir(), http.DefaultClient, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	data := testmedia.PNG512("concurrent shared upload")
	start := make(chan struct{})
	results := make(chan references.Image, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ctx := identity.WithDataOwnerID(identity.WithOwnerID(context.Background(), uuid.NewString()), templates.LibraryOwnerID)
			library := references.New(repo, local)
			<-start
			item, err := library.Upload(ctx, "shared.png", bytes.NewReader(data))
			results <- item
			failures <- err
		}()
	}
	close(start)
	a, b := <-results, <-results
	for i := 0; i < 2; i++ {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if a.ID != b.ID || a.SHA256 != b.SHA256 {
		t.Fatal("concurrent upload duplicated shared identity", a, b)
	}
}

func TestUnifiedCreateRetryAndEditUseConsistentLockOrder(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewTemplateRepository(db)
	defer repo.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, e := repo.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: templates.Definition{Title: "Concurrent create retry"}}, 0, "")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(199079)`); e != nil {
		t.Fatal(e)
	}
	edited, retried := make(chan error, 1), make(chan error, 1)
	go func() { _, err := repo.Save(ctx, r, r.Version, ""); edited <- err }()
	// Let the editor reach the held asset lock before a delayed create retry.
	time.Sleep(30 * time.Millisecond)
	go func() { _, err := repo.Save(ctx, r, 0, ""); retried <- err }()
	time.Sleep(30 * time.Millisecond)
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = <-edited; e != nil {
		t.Fatal("editor deadlocked", e)
	}
	if e = <-retried; !errors.Is(e, templates.ErrConflict) {
		t.Fatal("create retry did not conflict safely", e)
	}
}
