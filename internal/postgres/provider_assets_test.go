package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/google/uuid"
)

func TestProviderAssetIsolationPersistenceAndDeletionGuard(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	owner, other, providerOwner := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, owner), providerOwner, providerOwner)
	r := NewProviderAssetRepository(db)
	defer db.Exec(`DELETE FROM provider_assets WHERE provider_owner_id=$1`, providerOwner)
	defer db.Exec(`DELETE FROM provider_asset_groups WHERE provider_owner_id=$1`, providerOwner)
	group := providerassets.Group{ID: "group-test", Name: "Characters"}
	if err = r.SaveGroup(ctx, "bytedance", "project", group); err != nil {
		t.Fatal(err)
	}
	item := providerassets.Record{ID: uuid.NewString(), Provider: "bytedance", Scope: "project", SourceID: uuid.NewString(), Asset: providerassets.Asset{ID: "asset-test", URI: "asset://asset-test", GroupID: group.ID, Name: "Character", Type: "Image", Status: "Processing", PreviewURL: "https://preview.example/temporary"}}
	if _, err = db.Exec(`INSERT INTO reference_images(owner_id,id,name,content_type,size_bytes,storage_key,created_at) VALUES($1,$2,'Character','image/png',100,'fixture',now())`, owner, item.SourceID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM reference_images WHERE owner_id=$1`, owner)
	a, created, err := r.Reserve(ctx, item)
	if err != nil || !created {
		t.Fatal(a, created, err)
	}
	duplicate := item
	duplicate.ID = uuid.NewString()
	a, created, err = r.Reserve(ctx, duplicate)
	if err != nil || created || a.ID != item.ID {
		t.Fatal(a, created, err)
	}
	var raw []byte
	if err = db.QueryRow(`SELECT data FROM provider_assets WHERE id=$1`, item.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	_ = json.Unmarshal(raw, &stored)
	if stored["preview_url"] != nil {
		t.Fatal("expiring capability persisted")
	}
	restarted := NewProviderAssetRepository(db)
	a, err = restarted.Get(ctx, item.ID)
	if err != nil || a.Asset.ID != "asset-test" {
		t.Fatal(a, err)
	}
	foreign := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), other), providerOwner, providerOwner)
	if _, err = r.Get(foreign, item.ID); !errors.Is(err, providerassets.ErrNotFound) {
		t.Fatal("cross-owner asset", err)
	}
	if list, err := r.List(foreign, "bytedance", "project", 0); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	a.Status = "Active"
	if err = r.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	task := core.Task{ID: taskID, Status: core.TaskQueued, Provider: "bytedance", ProviderOwnerID: providerOwner, Model: "seedance", MediaType: "video", Prompt: "scene", Options: core.MediaOptions{ImageURLs: []string{"asset://asset-test"}}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	tasks := NewTaskRepository(db)
	if _, _, err = tasks.CreateOrGet(ctx, task, "", ""); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM tasks WHERE id=$1`, taskID)
	defer db.Exec(`DELETE FROM task_queue_outbox WHERE task_id=$1`, taskID)
	if _, err = r.BeginDelete(ctx, item.ID); !errors.Is(err, providerassets.ErrConflict) {
		t.Fatal("queued asset deleted", err)
	}
	if err = tasks.MarkFailed(ctx, taskID, "fixture complete"); err != nil {
		t.Fatal(err)
	}
	a, err = r.BeginDelete(ctx, item.ID)
	if err != nil || a.Status != "Deleting" {
		t.Fatal(a, err)
	}
	task.ID = uuid.NewString()
	if _, _, err = tasks.CreateOrGet(ctx, task, "", ""); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("admitted deleting asset", err)
	}
	a.Status = "Deleted"
	if err = r.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	if list, err := r.List(ctx, "bytedance", "project", 0); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
}

type assetDirectoryFixture struct{ client *assetClientFixture }

func (d *assetDirectoryFixture) AssetLibrary(context.Context, string) (providerassets.Client, error) {
	return d.client, nil
}

type assetClientFixture struct {
	scope               string
	creates             int
	uncertain, rejected bool
	remote              map[string]providerassets.Asset
}

func (f *assetClientFixture) Scope() string { return f.scope }
func (f *assetClientFixture) Groups(context.Context) ([]providerassets.Group, error) {
	return []providerassets.Group{{ID: "group-fixture", Name: "Characters"}}, nil
}
func (f *assetClientFixture) CreateGroup(context.Context, string) (providerassets.Group, error) {
	return providerassets.Group{ID: "group-fixture", Name: "Characters"}, nil
}
func (f *assetClientFixture) List(_ context.Context, _, _, name string) (providerassets.Page, error) {
	p := providerassets.Page{Items: []providerassets.Asset{}}
	for _, a := range f.remote {
		if name == "" || a.Name == name {
			p.Items = append(p.Items, a)
		}
	}
	return p, nil
}
func (f *assetClientFixture) Get(_ context.Context, id string) (providerassets.Asset, error) {
	a, ok := f.remote[id]
	if !ok {
		return a, providerassets.ErrNotFound
	}
	return a, nil
}
func (f *assetClientFixture) Create(_ context.Context, group, url, name, kind string) (providerassets.Asset, error) {
	f.creates++
	if f.rejected {
		return providerassets.Asset{}, providerassets.ErrRejected
	}
	id := "asset-" + uuid.NewString()
	a := providerassets.Asset{ID: id, GroupID: group, Name: name, Type: kind, Status: "Processing", URI: "asset://" + id, PreviewURL: "https://preview.example/temporary"}
	f.remote[id] = a
	if f.uncertain {
		return providerassets.Asset{}, errors.New("response lost")
	}
	return a, nil
}
func (f *assetClientFixture) Delete(_ context.Context, id string) error {
	delete(f.remote, id)
	return nil
}

func TestProviderAssetServiceRecoverySharedBindingsAndSources(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	owner, other, po := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), owner), po, po)
	otherCtx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), other), po, po)
	defer db.Exec(`DELETE FROM provider_assets WHERE provider_owner_id=$1`, po)
	defer db.Exec(`DELETE FROM provider_asset_groups WHERE provider_owner_id=$1`, po)
	defer db.Exec(`DELETE FROM reference_images WHERE owner_id=$1`, owner)
	repo := NewProviderAssetRepository(db)
	f := &assetClientFixture{scope: "project-a", remote: map[string]providerassets.Asset{}}
	directory := &assetDirectoryFixture{f}
	svc := providerassets.New(repo, directory)
	g, err := svc.CreateGroup(ctx, "bytedance", "Characters")
	if err != nil {
		t.Fatal(err)
	}
	source := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO reference_images(owner_id,id,name,content_type,size_bytes,storage_key,created_at) VALUES($1,$2,'Character.png','image/png',100,'fixture',now())`, owner, source); err != nil {
		t.Fatal(err)
	}
	f.uncertain = true
	a, err := svc.Register(ctx, "bytedance", source, "https://storage.example/source", "Character.png", "Image", g.ID)
	if err != nil || a.Status != "Unknown" || a.Asset.ID != "" {
		t.Fatal(a, err)
	}
	duplicate, err := svc.Register(ctx, "bytedance", source, "https://storage.example/source", "Character.png", "Image", g.ID)
	if err != nil || duplicate.ID != a.ID || f.creates != 1 {
		t.Fatal("create replay", duplicate, err, f.creates)
	}
	referenceRepo := NewReferenceRepository(db)
	removed := false
	if err = referenceRepo.DeleteWithGuard(ctx, source, func() error { removed = true; return nil }); !errors.Is(err, references.ErrInUse) || removed {
		t.Fatal("preparing source deleted", err)
	}
	// A restarted service reconciles the unique registration name, never replaying CreateAsset.
	svc = providerassets.New(repo, directory)
	a, err = svc.Get(ctx, "bytedance", a.ID)
	if err != nil || a.Asset.ID == "" || a.Status != "Processing" || a.Name != "Character.png" {
		t.Fatal(a, err)
	}
	stale := a
	stale.Asset.ID = ""
	stale.Status = "Unknown"
	if err = repo.Save(ctx, stale); !errors.Is(err, providerassets.ErrConflict) {
		t.Fatal("late unknown overwrote native ID", err)
	}
	remote := f.remote[a.Asset.ID]
	remote.Status = "Active"
	f.remote[a.Asset.ID] = remote
	svc = providerassets.New(repo, directory)
	a, err = svc.Get(ctx, "bytedance", a.ID)
	if err != nil || a.Status != "Active" {
		t.Fatal(a, err)
	}
	shared, err := svc.Import(otherCtx, "bytedance", a.Asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Delete(ctx, "bytedance", a.ID); !errors.Is(err, providerassets.ErrConflict) {
		t.Fatal("shared file deleted", err)
	}
	if err = svc.Detach(otherCtx, "bytedance", shared.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(otherCtx, "bytedance", shared.ID); !errors.Is(err, providerassets.ErrNotFound) {
		t.Fatal("detached visible", err)
	}
	if err = svc.Delete(ctx, "bytedance", a.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.remote) != 0 {
		t.Fatal("remote retained")
	}
	if err = referenceRepo.DeleteWithGuard(ctx, source, func() error { removed = true; return nil }); err != nil || !removed {
		t.Fatal("finished source blocked", err)
	}
	f.scope = "different-project"
	if list, _, err := svc.List(ctx, "bytedance", 0); err != nil || len(list) != 0 {
		t.Fatal("namespace leak", list, err)
	}
	// A definitive rejection remains Failed and can be removed locally; no ID is invented.
	f.scope = "project-a"
	f.uncertain = false
	f.rejected = true
	source = uuid.NewString()
	if _, err = db.Exec(`INSERT INTO reference_images(owner_id,id,name,content_type,size_bytes,storage_key,created_at) VALUES($1,$2,'Bad.png','image/png',100,'bad',now())`, owner, source); err != nil {
		t.Fatal(err)
	}
	failed, err := svc.Register(ctx, "bytedance", source, "https://storage.example/source", "Bad.png", "Image", g.ID)
	if err != nil || failed.Status != "Failed" {
		t.Fatal(failed, err)
	}
	failed, err = svc.Get(ctx, "bytedance", failed.ID)
	if err != nil || failed.Status != "Failed" {
		t.Fatal("rejected became uncertain", failed, err)
	}
	if err = svc.Delete(ctx, "bytedance", failed.ID); err != nil {
		t.Fatal(err)
	}
}
