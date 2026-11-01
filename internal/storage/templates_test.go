package storage

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
	"io"
	"strings"
	"testing"
)

func TestTemplateCopySurvivesStorageSwitchWithoutDeletingSource(t *testing.T) {
	ctx := identity.WithDataOwnerID(identity.WithUser(context.Background(), identity.User{ID: "administrator"}), identity.SharedOwnerID)
	ctx = identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
	repo := newSettingsMemory()
	remote := newMemoryS3()
	m := newTestManager(t, repo, remote)
	if _, e := m.Update(ctx, s3Update()); e != nil {
		t.Fatal(e)
	}
	data := []byte("synthetic template media")
	copy, e := m.StoreTemplateMedia(ctx, uuid.NewString(), data, "image/png")
	if e != nil || !strings.HasPrefix(copy.StorageKey, "s3:") || copy.SizeBytes != int64(len(data)) || copy.SHA256 == "" {
		t.Fatal(copy, e)
	}
	update := s3Update()
	update.Bucket = "different-bucket"
	if _, e = m.Update(ctx, update); e != nil {
		t.Fatal(e)
	}
	object, e := m.Open(ctx, copy.StorageKey)
	if e != nil {
		t.Fatal("old template copy lost after storage configuration change", e)
	}
	defer object.Reader.Close()
	got, e := io.ReadAll(object.Reader)
	if e != nil || string(got) != string(data) {
		t.Fatal(string(got), e)
	}
	local, e := m.Update(ctx, Update{Driver: "local"})
	if e != nil || local.Driver != "local" {
		t.Fatal(local, e)
	}
	another, e := m.StoreTemplateMedia(ctx, uuid.NewString(), data, "image/png")
	if e != nil || strings.HasPrefix(another.StorageKey, "s3:") {
		t.Fatal(another, e)
	}
	// Sharing is a deliberate new namespace, not the administrator's private data.
	if !strings.Contains(another.StorageKey, identity.SharedOwnerID+"/references/") {
		t.Fatal(another.StorageKey)
	}
}

func TestTemplateLibraryS3NamespaceAndPrivateIsolation(t *testing.T) {
	const libraryOwner = "00000000-0000-0000-0000-000000000002"
	actor := identity.WithOwnerID(context.Background(), uuid.NewString())
	ctx := identity.WithResourceOwners(identity.WithDataOwnerID(actor, libraryOwner), identity.SharedOwnerID, identity.SharedOwnerID)
	repo := newSettingsMemory()
	remote := newMemoryS3()
	manager := newTestManager(t, repo, remote)
	if _, e := manager.Update(ctx, s3Update()); e != nil {
		t.Fatal(e)
	}
	media, e := manager.StoreTemplateMedia(ctx, uuid.NewString(), []byte("synthetic shared reference"), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(media.StorageKey, libraryOwner+"/references/") {
		t.Fatal("shared source namespace missing", media.StorageKey)
	}
	object, e := manager.Open(ctx, media.StorageKey)
	if e != nil {
		t.Fatal(e)
	}
	object.Reader.Close()
	private := identity.WithResourceOwners(identity.WithPersonalData(actor), identity.SharedOwnerID, identity.SharedOwnerID)
	if e = manager.DeleteReference(private, media.StorageKey); e == nil {
		t.Fatal("private context deleted shared source")
	}
	object, e = manager.Open(ctx, media.StorageKey)
	if e != nil {
		t.Fatal("private delete damaged source", e)
	}
	object.Reader.Close()
	if e = manager.DeleteReference(ctx, media.StorageKey); e != nil {
		t.Fatal(e)
	}
}
