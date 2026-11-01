package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplateReferenceLibraryIsolationAuditAndRetainedDeletion(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewReferenceRepository(db)
	tr := NewTemplateRepository(db)
	defer tr.Close()
	actor := uuid.NewString()
	ctx := identity.WithOwnerID(context.Background(), actor)
	shared := identity.WithDataOwnerID(ctx, templates.LibraryOwnerID)
	private := identity.WithPersonalData(ctx)
	org := identity.WithOrganization(ctx, uuid.NewString(), true)
	item := func(name string) references.Image {
		return references.Image{ID: uuid.NewString(), Name: name, ContentType: "image/png", SizeBytes: 10, StorageKey: uuid.NewString(), CreatedAt: time.Now(), SHA256: uuid.NewString()}
	}
	source, personal, company := item("shared.png"), item("personal.png"), item("company.png")
	for _, pair := range []struct {
		ctx  context.Context
		item references.Image
	}{{shared, source}, {private, personal}, {org, company}} {
		if e := repo.Insert(pair.ctx, pair.item); e != nil {
			t.Fatal(e)
		}
	}
	otherCreator := identity.WithDataOwnerID(identity.WithOwnerID(context.Background(), uuid.NewString()), templates.LibraryOwnerID)
	got, e := repo.List(otherCreator, 48, 0)
	if e != nil || len(got) != 1 || got[0].ID != source.ID {
		t.Fatal(got, e)
	}
	for _, c := range []context.Context{private, org} {
		if _, e = repo.Get(c, source.ID); !errors.Is(e, storage.ErrObjectNotFound) {
			t.Fatal("shared source exposed privately", e)
		}
	}
	var auditActor string
	if e = db.QueryRow(`SELECT actor_id FROM reference_images WHERE id=$1`, source.ID).Scan(&auditActor); e != nil || auditActor != actor {
		t.Fatal("creator audit lost", auditActor, e)
	}
	id := uuid.NewString()
	if _, e = tr.Save(ctx, templates.Record{ID: id, Draft: templates.Definition{Title: "Fixture"}}, 0, ""); e != nil {
		t.Fatal(e)
	}
	record, e := tr.Get(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	record.Draft.CoverID = source.ID
	if _, e = tr.Save(ctx, record, record.Version, ""); e != nil {
		t.Fatal(e)
	}
	read, e := tr.Asset(ctx, source.ID)
	if e != nil || read.ID != source.ID || read.Name != source.Name {
		t.Fatal(read, e)
	}
	removed := false
	e = repo.DeleteWithGuard(shared, source.ID, func() error { removed = true; return nil })
	if !errors.Is(e, references.ErrInUse) || removed {
		t.Fatal("bound asset removed", e, removed)
	}
	unused := item("unused.png")
	if e = repo.Insert(shared, unused); e != nil {
		t.Fatal(e)
	}
	if e = repo.DeleteWithGuard(shared, unused.ID, func() error { removed = true; return nil }); e != nil || !removed {
		t.Fatal(e)
	}
	// A deleted shared ID or a private ID cannot be saved as a binding.
	for _, invalid := range []string{unused.ID, personal.ID} {
		record.Draft.CoverID = invalid
		if _, e = tr.Save(ctx, record, record.Version+1, ""); !errors.Is(e, templates.ErrNotFound) {
			t.Fatal("unavailable asset bound", e)
		}
	}

}

func TestTemplateReferenceSourceCopyDeleteSerialization(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewReferenceRepository(db)
	tr := NewTemplateRepository(db)
	defer tr.Close()
	ctx := identity.WithDataOwnerID(context.Background(), templates.LibraryOwnerID)
	source := references.Image{ID: uuid.NewString(), Name: "race.png", ContentType: "image/png", SizeBytes: 10, StorageKey: uuid.NewString(), CreatedAt: time.Now()}
	if e := repo.Insert(ctx, source); e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	if _, e := tr.Save(ctx, templates.Record{ID: id, Draft: templates.Definition{Title: "Race"}}, 0, ""); e != nil {
		t.Fatal(e)
	}
	// Reproduce a copy holding the shared source lock while deletion waits.
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(199079)`); e != nil {
		t.Fatal(e)
	}
	deleted := make(chan error, 1)
	removed := false
	go func() { deleted <- repo.DeleteWithGuard(ctx, source.ID, func() error { removed = true; return nil }) }()
	select {
	case e := <-deleted:
		t.Fatal("delete passed in-flight source lock", e)
	case <-time.After(50 * time.Millisecond):
	}
	if _, e = tx.ExecContext(ctx, `UPDATE generation_templates SET content=jsonb_set(content,'{cover_id}',to_jsonb($1::text)) WHERE id=$2`, source.ID, id); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-deleted:
		if !errors.Is(e, references.ErrInUse) || removed {
			t.Fatal(e, removed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("guard did not resume")
	}
}

func TestTemplateSourceAndCopyShareExistingStorageAllowance(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewStorageSettingsRepository(db)
	ctx := context.Background()
	source := identity.WithDataOwnerID(ctx, templates.LibraryOwnerID)
	copies := identity.WithDataOwnerID(ctx, identity.SharedOwnerID)
	private := identity.WithDataOwnerID(ctx, uuid.NewString())
	for _, pair := range []struct {
		ctx  context.Context
		size int64
	}{{source, 30}, {copies, 70}, {private, 900}} {
		if e := repo.RecordObject(pair.ctx, uuid.NewString(), "", pair.size); e != nil {
			t.Fatal(e)
		}
	}
	for _, c := range []context.Context{source, copies} {
		u, e := repo.SharedUsage(c)
		if e != nil || u.UsedBytes != 100 {
			t.Fatal("source/copy allowance split", u, e)
		}
	}
	if u, e := repo.SharedUsage(private); e != nil || u.UsedBytes != 900 {
		t.Fatal("private accounting changed", u, e)
	}
}
