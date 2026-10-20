package postgres

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

func TestReferenceLibraryPersistsAndIsolatesOwners(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	alice := identity.WithOwnerID(ctx, uuid.NewString())
	bob := identity.WithOwnerID(ctx, uuid.NewString())
	defer db.ExecContext(ctx, `DELETE FROM reference_images WHERE owner_id=$1`, identity.OwnerID(alice))
	cipher, _ := providerconfig.NewCipher("reference-test-secret")
	dir := t.TempDir()
	manager, err := storage.NewManager(NewStorageSettingsRepository(db), cipher, dir, http.DefaultClient, 30<<20)
	if err != nil {
		t.Fatal(err)
	}
	lib := references.New(NewReferenceRepository(db), manager)
	// Minimal GIF content is sufficient to exercise storage routing and metadata.
	payload := []byte("GIF89a-reference-integration-test")
	first, err := lib.Upload(alice, "first.gif", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Upload(alice, "second.gif", bytes.NewReader(append(bytes.Clone(payload), byte(1))))
	if err != nil {
		t.Fatal(err)
	}
	manager, err = storage.NewManager(NewStorageSettingsRepository(db), cipher, dir, http.DefaultClient, 30<<20)
	if err != nil {
		t.Fatal(err)
	}
	lib = references.New(NewReferenceRepository(db), manager)
	list, err := lib.List(alice, 1, 0)
	if err != nil || len(list) != 1 || list[0].ID != second.ID {
		t.Fatal("newest-first pagination", list, err)
	}
	list, err = lib.List(alice, 1, 1)
	if err != nil || len(list) != 1 || list[0].ID != first.ID {
		t.Fatal("second page", list, err)
	}
	list, err = lib.List(bob, 10, 0)
	if err != nil || len(list) != 0 {
		t.Fatal("cross-owner list")
	}
	if _, _, err := lib.Open(bob, first.ID); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("cross-owner read")
	}
	if err := lib.Delete(bob, first.ID); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("cross-owner delete")
	}
	item, object, err := lib.Open(alice, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(object.Reader)
	object.Reader.Close()
	if err != nil || !bytes.Equal(data, payload) || item.Name != "first.gif" {
		t.Fatal("restart lost content")
	}
	if err := lib.Delete(alice, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lib.Open(alice, first.ID); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("delete did not persist")
	}
	if _, err := manager.Open(alice, item.StorageKey); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("file was not physically removed")
	}
	if _, remaining, err := lib.Open(alice, second.ID); err != nil {
		t.Fatal("delete affected another reference")
	} else {
		remaining.Reader.Close()
	}
}

func TestConcurrentReferenceUploadsReuseContent(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	defer db.ExecContext(ctx, `DELETE FROM reference_images WHERE owner_id=$1`, identity.OwnerID(ctx))
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 30<<20)
	if err != nil {
		t.Fatal(err)
	}
	library := references.New(NewReferenceRepository(db), store)
	var wg sync.WaitGroup
	results := make(chan references.Image, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := library.Upload(ctx, "same.gif", bytes.NewReader([]byte("GIF89a-concurrent-content")))
			if err != nil {
				failures <- err
			} else {
				results <- item
			}
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var first string
	created := 0
	for item := range results {
		if first == "" {
			first = item.ID
		}
		if item.ID != first {
			t.Fatal("duplicate records created")
		}
		if !item.Duplicate {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d copies", created)
	}
	list, err := library.List(ctx, 20, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("rows: %d %v", len(list), err)
	}
}
