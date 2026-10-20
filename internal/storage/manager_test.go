package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
)

type settingsMemory struct {
	active      map[string]SettingsRecord
	connections map[string]SettingsRecord
}

func newSettingsMemory() *settingsMemory {
	return &settingsMemory{active: map[string]SettingsRecord{}, connections: map[string]SettingsRecord{}}
}
func (r *settingsMemory) Get(ctx context.Context) (SettingsRecord, error) {
	v, ok := r.active[identity.StorageOwnerID(ctx)]
	if !ok {
		return v, ErrSettingsNotFound
	}
	return v, nil
}
func (r *settingsMemory) Connection(ctx context.Context, id string) (SettingsRecord, error) {
	v, ok := r.connections[identity.OwnerID(ctx)+"/"+id]
	if !ok {
		v, ok = r.connections[identity.SharedOwnerID+"/"+id]
	}
	if !ok {
		return v, ErrSettingsNotFound
	}
	return v, nil
}
func (r *settingsMemory) Save(ctx context.Context, v SettingsRecord) error {
	owner := identity.StorageOwnerID(ctx)
	r.active[owner] = v
	if v.ConnectionID != "" {
		r.connections[owner+"/"+v.ConnectionID] = v
	}
	return nil
}
func newTestManager(t *testing.T, repo *settingsMemory, remote *memoryS3) *Manager {
	t.Helper()
	cipher, _ := providerconfig.NewCipher("storage-test-encryption-key")
	m, err := NewManager(repo, cipher, t.TempDir(), &http.Client{Transport: remote}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func s3Update() Update {
	c := testS3Config()
	return Update{Driver: "s3", Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix, PathStyle: c.PathStyle, AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey}
}
func TestManagerSwitchHistoryEncryptionAndIsolation(t *testing.T) {
	ctx := identity.WithOwnerID(context.Background(), "alice")
	repo := newSettingsMemory()
	remote := newMemoryS3()
	m := newTestManager(t, repo, remote)
	source := "data:video/mp4;base64,c21hbGwtdmlkZW8="
	local, err := m.Archive(ctx, "local-task", source)
	if err != nil {
		t.Fatal(err)
	}
	view, err := m.Update(ctx, s3Update())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	if view.AccessKeyID != "test-access" || !strings.Contains(string(raw), `"access_key_id":"test-access"`) {
		t.Fatal("saved key identifier missing")
	}
	if strings.Contains(string(raw), "test-secret") || strings.Contains(string(raw), "secret_access_key") {
		t.Fatal("secret exposed")
	}
	record, _ := repo.Get(ctx)
	if strings.Contains(string(record.Ciphertext), "test-secret") {
		t.Fatal("unencrypted credentials")
	}
	// A separate worker instance sees settings immediately, without restart/cache invalidation.
	worker := newTestManager(t, repo, remote)
	first, err := worker.Archive(ctx, "task-1", source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.StorageKey, "s3:") || !strings.Contains(first.StorageKey, "/alice/results/task-1/") {
		t.Fatal(first.StorageKey)
	}
	pinned, err := worker.ForArchive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	update := s3Update()
	update.Bucket = "another-bucket"
	update.AccessKeyID = ""
	update.SecretAccessKey = ""
	if _, err := m.Update(ctx, update); err != nil {
		t.Fatal(err)
	}
	second, err := m.Archive(ctx, "task-2", source)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := pinned.Archive(ctx, "task-1/last-frame", source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Split(first.StorageKey, "/")[0] != strings.Split(frame.StorageKey, "/")[0] {
		t.Fatal("task artifacts changed destination")
	}
	if _, err := m.Update(ctx, Update{Driver: "local"}); err != nil {
		t.Fatal(err)
	}
	for _, media := range []string{local.StorageKey, first.StorageKey, second.StorageKey, frame.StorageKey} {
		obj, err := m.Open(ctx, media)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(obj.Reader)
		obj.Reader.Close()
		if err != nil || string(data) != "small-video" {
			t.Fatalf("history lost for %s: %v", media, err)
		}
	}
	bob := identity.WithOwnerID(context.Background(), "bob")
	if _, err := m.Open(bob, first.StorageKey); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("cross-owner read: %v", err)
	}
	view, err = m.Get(bob)
	if err != nil || view.Driver != "local" || view.CredentialsConfigured || view.AccessKeyID != "" {
		t.Fatal("settings crossed owners")
	}
}
func TestManagerFailedCheckDoesNotChangeSettings(t *testing.T) {
	ctx := context.Background()
	repo := newSettingsMemory()
	remote := newMemoryS3()
	m := newTestManager(t, repo, remote)
	if err := m.Check(ctx, s3Update()); err != nil {
		t.Fatal(err)
	}
	if len(repo.active) != 0 {
		t.Fatal("check activated storage")
	}
	if _, err := m.Update(ctx, s3Update()); err != nil {
		t.Fatal(err)
	}
	initial, _ := repo.Get(ctx)
	// Pasting the same endpoint without its scheme must preserve credentials and history.
	bare := s3Update()
	bare.Endpoint = strings.TrimPrefix(bare.Endpoint, "https://")
	bare.AccessKeyID, bare.SecretAccessKey = "", ""
	view, err := m.Update(ctx, bare)
	if err != nil || view.Endpoint != s3Update().Endpoint || view.AccessKeyID != "test-access" || !view.CredentialsConfigured {
		t.Fatalf("bare endpoint failed to preserve saved connection: %+v, %v", view, err)
	}
	normalized, _ := repo.Get(ctx)
	if normalized.ConnectionID != initial.ConnectionID {
		t.Fatal("equivalent endpoint created a new connection")
	}
	update := s3Update()
	update.Endpoint = "https://other.example"
	update.AccessKeyID = ""
	update.SecretAccessKey = ""
	if _, err := m.Update(ctx, update); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("sent saved credentials to another host: %v", err)
	}
	remote.deny = "PUT"
	update = s3Update()
	update.Bucket = "other-bucket"
	if _, err := m.Update(ctx, update); err == nil {
		t.Fatal("enabled inaccessible bucket")
	}
	final, _ := repo.Get(ctx)
	if final.ConnectionID != initial.ConnectionID {
		t.Fatal("failed save changed destination")
	}
}

func TestSharedStoragePreservesUserIsolationAndOldFiles(t *testing.T) {
	repo := newSettingsMemory()
	remote := newMemoryS3()
	m := newTestManager(t, repo, remote)
	alice := identity.WithOwnerID(context.Background(), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	shared := identity.WithResourceOwners(alice, identity.SharedOwnerID, identity.SharedOwnerID)
	if _, err := m.Update(shared, s3Update()); err != nil {
		t.Fatal(err)
	}
	media, err := seedStoredS3Reference(m, remote, shared, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", []byte("reference"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := m.Open(alice, media.StorageKey)
	if err != nil {
		t.Fatal("old shared file unavailable with personal storage:", err)
	}
	obj.Reader.Close()
	bob := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc"), identity.SharedOwnerID, identity.SharedOwnerID)
	if _, err = m.Open(bob, media.StorageKey); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("foreign shared file read: %v", err)
	}
	if err = m.DeleteReference(bob, media.StorageKey); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("foreign shared file delete: %v", err)
	}
	if err = m.DeleteReference(alice, media.StorageKey); err != nil {
		t.Fatal(err)
	}
}

func TestResultDeletionUsesOriginalSharedDestination(t *testing.T) {
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "alice"), identity.SharedOwnerID, identity.SharedOwnerID)
	remote := newMemoryS3()
	m := newTestManager(t, newSettingsMemory(), remote)
	if _, e := m.Update(ctx, s3Update()); e != nil {
		t.Fatal(e)
	}
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	result, e := m.Archive(ctx, id, "data:video/mp4;base64,c21hbGwtdmlkZW8=")
	if e != nil {
		t.Fatal(e)
	}
	next := s3Update()
	next.Bucket = "changed-bucket"
	if _, e = m.Update(ctx, next); e != nil {
		t.Fatal(e)
	}
	bob := identity.WithOwnerID(context.Background(), "bob")
	if e = m.DeleteResult(bob, id, result.StorageKey); !errors.Is(e, ErrObjectNotFound) {
		t.Fatal("cross-user deletion allowed", e)
	}
	remote.deny = "DELETE"
	if e = m.DeleteResult(ctx, id, result.StorageKey); e == nil {
		t.Fatal("failed deletion hidden")
	}
	object, e := m.Open(ctx, result.StorageKey)
	if e != nil {
		t.Fatal("failed delete lost result")
	}
	object.Reader.Close()
	remote.deny = ""
	if e = m.DeleteResult(ctx, id, result.StorageKey); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Open(ctx, result.StorageKey); !errors.Is(e, ErrObjectNotFound) {
		t.Fatal("result not deleted", e)
	}
	if e = m.DeleteResult(ctx, id, result.StorageKey); e != nil {
		t.Fatal("retry failed", e)
	}
}

func TestOrganizationStorageSwitchPreservesFilesAndConnection(t *testing.T) {
	ctx := identity.WithOrganization(identity.WithOwnerID(context.Background(), "alice"), "company", true)
	ctx = identity.WithResourceOwners(ctx, identity.SharedOwnerID, "company")
	repo, remote := newSettingsMemory(), newMemoryS3()
	m := newTestManager(t, repo, remote)
	view, err := m.Get(ctx)
	if err != nil || view.Driver != "gateway" {
		t.Fatalf("default: %+v %v", view, err)
	}
	source := "data:video/mp4;base64,c21hbGwtdmlkZW8="
	gateway, err := m.Archive(ctx, "before", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Update(ctx, s3Update()); err != nil {
		t.Fatal(err)
	}
	company, err := m.Archive(ctx, "company", source)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := m.ForArchive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Update(ctx, Update{Driver: "gateway"}); err != nil {
		t.Fatal(err)
	}
	frame, err := pinned.Archive(ctx, "company/last-frame", source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := m.Archive(ctx, "after", source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(gateway.StorageKey, "s3:") || strings.HasPrefix(after.StorageKey, "s3:") || !strings.HasPrefix(company.StorageKey, "s3:") {
		t.Fatal("wrong destinations")
	}
	for _, key := range []string{gateway.StorageKey, company.StorageKey, frame.StorageKey, after.StorageKey} {
		obj, err := m.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		obj.Reader.Close()
	}
	saved, err := m.Get(ctx)
	if err != nil || !saved.CredentialsConfigured {
		t.Fatal("switch discarded credentials", err)
	}
	// Re-enable the same company destination with the saved credentials.
	update := s3Update()
	update.AccessKeyID = ""
	update.SecretAccessKey = ""
	if _, err = m.Update(ctx, update); err != nil {
		t.Fatal(err)
	}
	again, err := m.Archive(ctx, "again", source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Split(again.StorageKey, "/")[0] != strings.Split(company.StorageKey, "/")[0] {
		t.Fatal("connection lost")
	}
}
