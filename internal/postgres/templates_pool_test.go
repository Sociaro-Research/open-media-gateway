package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplatePublicationPoolLeavesDownstreamCapacity(t *testing.T) {
	db := auditDatabase(t, true)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	repo := NewTemplateRepository(db)
	t.Cleanup(func() { _ = repo.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	item, err := repo.Save(ctx, templates.Record{ID: uuid.NewString(), Draft: templates.Definition{Title: "Bounded locks", CoverID: ""}}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	asset := templates.Asset{ID: uuid.NewString(), TemplateID: item.ID, ContentType: "image/png", SizeBytes: 1, StorageKey: "fixture/" + uuid.NewString(), SHA256: "fixture"}
	if err = repo.PutAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	item.Draft.CoverID = asset.ID
	foreign := asset
	foreign.ID, foreign.TemplateID, foreign.StorageKey = uuid.NewString(), uuid.NewString(), "fixture/"+uuid.NewString()
	if _, err = repo.Save(ctx, templates.Record{ID: foreign.TemplateID, Draft: templates.Definition{Title: "Another template"}}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err = repo.PutAsset(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	// Membership validation must still roll back the entire publication.
	bad := item
	bad.Draft.Slots = []templates.Slot{{AssetID: uuid.NewString()}}
	if _, err = repo.Save(ctx, bad, item.Version, "publish"); !errors.Is(err, templates.ErrNotFound) {
		t.Fatal(err)
	}
	stored, err := repo.Get(ctx, item.ID)
	if err != nil || stored.Version != 1 || stored.Published != nil {
		t.Fatal(stored, err)
	}
	storedAsset, err := repo.Asset(ctx, asset.ID)
	if err != nil || storedAsset.Published {
		t.Fatal("partial visibility committed", storedAsset, err)
	}
	if other, err := repo.Asset(ctx, foreign.ID); err != nil || other.Published {
		t.Fatal("foreign asset visibility changed", other, err)
	}
	item, err = repo.Save(ctx, item, item.Version, "publish")
	if err != nil {
		t.Fatal(err)
	}
	const clients = 20
	errorsOut := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsOut <- repo.WithPublished(ctx, item.ID, item.Revision, func(current templates.Record) error {
				if current.Revision != item.Revision {
					return templates.ErrConflict
				}
				_, err := repo.Asset(ctx, asset.ID)
				return err
			})
		}()
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal("callback starved downstream pool", err)
		}
	}
	if db.Stats().MaxOpenConnections != 1 || repo.locks.Stats().MaxOpenConnections != 2 {
		t.Fatal("pool capacity was increased", db.Stats(), repo.locks.Stats())
	}
	// Waiting writers must use the isolated pool too; otherwise they can fill
	// the downstream pool while the active reader needs its next asset lookup.
	// A distinct repository also verifies that the lock is held by PostgreSQL,
	// rather than only coordinating callers through an in-process mutex.
	editor := NewTemplateRepository(db)
	t.Cleanup(func() { _ = editor.Close() })
	editorPool, err := editor.publicationDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, readerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		readerDone <- repo.WithPublished(ctx, item.ID, item.Revision, func(templates.Record) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := repo.Asset(ctx, asset.ID)
			return err
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	writes := make(chan error, clients)
	for i := 0; i < clients; i++ {
		go func() { _, err := editor.Save(ctx, item, item.Version, "unpublish"); writes <- err }()
	}
	for editorPool.Stats().InUse < 2 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if _, err = repo.Asset(ctx, asset.ID); err != nil {
		t.Fatal("waiting editors consumed downstream capacity", err)
	}
	select {
	case err := <-writes:
		t.Fatal("publication changed under active admission", err)
	default:
	}
	unblock()
	if err = <-readerDone; err != nil {
		t.Fatal(err)
	}
	wins := 0
	for i := 0; i < clients; i++ {
		err := <-writes
		if err == nil {
			wins++
		} else if !errors.Is(err, templates.ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("publication CAS lost", wins)
	}
	if err = repo.WithPublished(ctx, item.ID, item.Revision, func(templates.Record) error { t.Error("unpublished revision admitted"); return nil }); !errors.Is(err, templates.ErrNotFound) {
		t.Fatal(err)
	}
}
