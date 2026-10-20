package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

// Exercise the production Manager and owner-scoped SQL with both local files
// and the real S3 client against a loopback-only object server.
func TestPrivateArchiveAttemptsRemainDiscoverableWithoutSharedQuota(t *testing.T) {
	for _, organization := range []bool{false, true} {
		for _, remote := range []bool{false, true} {
			t.Run(fmt.Sprintf("organization=%t/s3=%t", organization, remote), func(t *testing.T) {
				dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
				if dsn == "" {
					t.Skip("requires isolated PostgreSQL")
				}
				ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
				resourceOwner := identity.ActorID(ctx)
				if organization {
					ctx = identity.WithOrganization(ctx, uuid.NewString(), true)
					resourceOwner = identity.OwnerID(ctx)
				} else {
					ctx = identity.WithPersonalData(ctx)
				}
				ctx = identity.WithResourceOwners(ctx, identity.ActorID(ctx), resourceOwner)
				db, err := Open(ctx, dsn)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if err := Migrate(db); err != nil {
					t.Fatal(err)
				}
				defer func() {
					for _, table := range []string{"result_objects", "stored_objects", "tasks", "storage_settings", "storage_connections"} {
						_, _ = db.ExecContext(context.Background(), "DELETE FROM "+table+" WHERE owner_id IN ($1,$2)", identity.OwnerID(ctx), resourceOwner)
					}
				}()
				cipher, err := providerconfig.NewCipher("synthetic-private-result-index")
				if err != nil {
					t.Fatal(err)
				}
				settings := NewStorageSettingsRepository(db)
				manager, err := storage.NewManager(settings, cipher, t.TempDir(), http.DefaultClient, 1024)
				if err != nil {
					t.Fatal(err)
				}
				update := storage.Update{Driver: "local"}
				var mu sync.Mutex
				objects := map[string][]byte{}
				if remote {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						switch r.Method {
						case http.MethodPut:
							data, err := io.ReadAll(r.Body)
							if err != nil {
								http.Error(w, "read failed", http.StatusBadRequest)
								return
							}
							objects[r.URL.Path] = data
							w.Header().Set("ETag", `"synthetic"`)
						case http.MethodGet, http.MethodHead:
							data, ok := objects[r.URL.Path]
							if !ok {
								w.WriteHeader(http.StatusNotFound)
								return
							}
							w.Header().Set("Content-Type", "video/mp4")
							w.Header().Set("Content-Length", fmt.Sprint(len(data)))
							if r.Method == http.MethodGet {
								_, _ = w.Write(data)
							}
						case http.MethodDelete:
							delete(objects, r.URL.Path)
							w.WriteHeader(http.StatusNoContent)
						default:
							w.WriteHeader(http.StatusMethodNotAllowed)
						}
					}))
					defer server.Close()
					update = storage.Update{Driver: "s3", Endpoint: server.URL, Region: "test", Bucket: "private-results", PathStyle: true, AccessKeyID: "synthetic", SecretAccessKey: "synthetic"}
				}
				if _, err := manager.Update(ctx, update); err != nil {
					t.Fatal(err)
				}
				shared := identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
				if _, err := manager.StoreReference(shared, uuid.NewString(), []byte("ref"), "image/png"); err != nil {
					t.Fatal(err)
				}
				repo := NewTaskRepository(db)
				id := uuid.NewString()
				now := time.Now().UTC()
				task := core.Task{ID: id, Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "private result ledger", CreatedAt: now, UpdatedAt: now, StorageOwnerID: identity.StorageOwnerID(ctx)}
				if _, _, err := repo.CreateOrGet(ctx, task, "", "private-results"); err != nil {
					t.Fatal(err)
				}
				if err := repo.MarkRunning(ctx, id); err != nil {
					t.Fatal(err)
				}
				var archived []core.StoredMedia
				for _, attempt := range []string{"expired", "winner"} {
					if remote && attempt == "winner" {
						next := update
						next.Bucket = "private-results-next"
						if _, err := manager.Update(ctx, next); err != nil {
							t.Fatal(err)
						}
					}
					for _, artifact := range []string{"result", "last-frame"} {
						media, err := manager.Archive(ctx, id+"/attempts/"+attempt+"/"+artifact, "data:video/mp4;base64,c21hbGwtdmlkZW8=")
						if err != nil {
							t.Fatal(err)
						}
						archived = append(archived, media)
					}
				}
				winner := archived[2]
				winner.LastFrameStorageKey = archived[3].StorageKey
				if err := repo.MarkSucceeded(ctx, id, winner, "/v1/generations/"+id+"/last-frame"); err != nil {
					t.Fatal(err)
				}
				usage, err := manager.SharedUsage(shared)
				if err != nil || !usage.Applies || usage.UsedBytes != 3 || usage.UnknownObjects != 0 {
					t.Fatalf("private archives changed shared quota: %+v %v", usage, err)
				}
				keys, err := repo.StoredResultKeys(ctx, id)
				if err != nil || len(keys) != 4 {
					t.Fatalf("private attempt discovery: got %d keys, want 4; err=%v", len(keys), err)
				}
				var count int
				var bytes int64
				if err := db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0)::bigint FROM result_objects WHERE owner_id=$1 AND task_id=$2 AND deleted_at IS NULL`, identity.OwnerID(ctx), id).Scan(&count, &bytes); err != nil || count != 4 || bytes != 44 {
					t.Fatalf("private index count=%d bytes=%d err=%v", count, bytes, err)
				}
				service := core.NewTaskService(repo, nil, "mock", "mock-video-v1")
				foreign := identity.WithOwnerID(context.Background(), uuid.NewString())
				if err := service.DeleteResult(foreign, id, manager.DeleteResult); !errors.Is(err, core.ErrTaskNotFound) {
					t.Fatalf("foreign task deletion: %v", err)
				}
				if remote {
					oldConnection, _, _ := strings.Cut(strings.TrimPrefix(archived[0].StorageKey, "s3:"), "/")
					if _, err := settings.Connection(foreign, oldConnection); !errors.Is(err, storage.ErrSettingsNotFound) {
						t.Fatalf("foreign owner obtained private storage connection: %v", err)
					}
				}
				lateStore, err := manager.ForArchive(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := manager.Update(ctx, storage.Update{Driver: "local"}); err != nil {
					t.Fatal(err)
				}
				// A new shared-mode key retains the same data owner but no longer
				// names the original private storage owner in its resource context.
				deletionCtx := identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
				if err := service.DeleteResult(deletionCtx, id, manager.DeleteResult); err != nil {
					t.Fatal(err)
				}
				late, err := lateStore.Archive(ctx, id+"/attempts/after-delete", "data:video/mp4;base64,c21hbGwtdmlkZW8=")
				if err != nil {
					t.Fatal(err)
				}
				archived = append(archived, late)
				if err := service.DeleteResult(deletionCtx, id, manager.DeleteResult); err != nil {
					t.Fatalf("repeat private result deletion: %v", err)
				}
				for _, media := range archived {
					if object, err := manager.Open(ctx, media.StorageKey); !errors.Is(err, storage.ErrObjectNotFound) {
						if err == nil {
							object.Reader.Close()
						}
						t.Fatalf("private attempt survived normal deletion: %s %v", media.StorageKey, err)
					}
				}
				if remote {
					mu.Lock()
					remaining := len(objects)
					mu.Unlock()
					if remaining != 0 {
						t.Fatalf("loopback S3 retained %d objects after result deletion", remaining)
					}
				}
				if err := db.QueryRowContext(ctx, `SELECT count(*) FROM result_objects WHERE owner_id=$1 AND task_id=$2 AND deleted_at IS NULL`, identity.OwnerID(ctx), id).Scan(&count); err != nil || count != 0 {
					t.Fatalf("private index retained live objects: %d %v", count, err)
				}
				if keys, err := repo.StoredResultKeys(ctx, id); err != nil || len(keys) != 0 {
					t.Fatalf("already-deleted winner keys replayed: %v %v", keys, err)
				}
				usage, err = manager.SharedUsage(shared)
				if err != nil || usage.UsedBytes != 3 {
					t.Fatalf("result deletion changed shared reference usage: %+v %v", usage, err)
				}
				t.Log("four initial private artifacts and one post-delete archive removed; shared quota remains 3 bytes; deleted winner keys not replayed")
			})
		}
	}
}

// Application rollback runs the previous embedded migration set against the
// upgraded database. It must leave the private index and old quota query intact.
func TestPrivateResultIndexPreservesPreviousMigrationSet(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	owner, task := identity.OwnerID(ctx), uuid.NewString()
	defer db.ExecContext(context.Background(), `DELETE FROM result_objects WHERE owner_id=$1`, owner)
	settings := NewStorageSettingsRepository(db)
	if err := settings.RecordResultObject(ctx, "private-result", task, 11); err != nil {
		t.Fatal(err)
	}
	previous := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "065_" {
			continue
		}
		name := "migrations/" + entry.Name()
		data, err := migrations.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		previous[name] = &fstest.MapFile{Data: data}
	}
	goose.SetBaseFS(previous)
	defer goose.SetBaseFS(migrations)
	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("previous application migration set cannot start on version 65: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM result_objects WHERE owner_id=$1`, owner).Scan(&count); err != nil || count != 1 {
		t.Fatalf("application rollback lost private index: %d %v", count, err)
	}
	var legacyBytes int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes),0)::bigint FROM stored_objects WHERE owner_id=$1 AND deleted_at IS NULL`, owner).Scan(&legacyBytes); err != nil || legacyBytes != 0 {
		t.Fatalf("previous quota query counts private result bytes: %d %v", legacyBytes, err)
	}
	t.Log("previous 001–064 migration set accepts database version 65; private index retained; old quota SQL unchanged")
}

func TestPrivateResultIndexUpgradePreservesExistingResults(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	// Match the existing migration suites: only this new disposable schema is
	// removed on cleanup; no pre-existing schema is downgraded or overwritten.
	schema := "result_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := base.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 64); err != nil {
		t.Fatal(err)
	}
	if version, err := goose.GetDBVersion(db); err != nil || version != 64 {
		t.Fatalf("upgrade fixture is not on current main schema 64: %d %v", version, err)
	}
	settings := NewStorageSettingsRepository(db)
	root := t.TempDir()
	local, err := storage.NewLocal(root, http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	type legacyResult struct {
		id, name string
		media    core.StoredMedia
	}
	var results []legacyResult
	for _, name := range []string{"private-canonical", "shared-canonical", "private-bare"} {
		id := uuid.NewString()
		prefix := identity.OwnerID(ctx) + "/results/" + id
		if name == "private-bare" {
			prefix = id
		}
		media, err := local.Archive(ctx, prefix, "data:video/mp4;base64,c21hbGwtdmlkZW8=")
		if err != nil {
			t.Fatal(err)
		}
		frame, err := local.Archive(ctx, prefix+"/last-frame", "data:video/mp4;base64,c21hbGwtdmlkZW8=")
		if err != nil {
			t.Fatal(err)
		}
		media.LastFrameStorageKey = frame.StorageKey
		storageOwner := identity.OwnerID(ctx)
		if name == "shared-canonical" {
			storageOwner = identity.SharedOwnerID
			shared := identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
			if err := settings.RecordObject(shared, media.StorageKey, id, media.SizeBytes); err != nil {
				t.Fatal(err)
			}
			if err := settings.RecordObject(shared, frame.StorageKey, id, frame.SizeBytes); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO tasks(id,owner_id,status,provider,model,media_type,prompt,storage_owner_id,provider_owner_id,storage_key,last_frame_storage_key,result_size_bytes,result_sha256) VALUES($1,$2,'succeeded','mock','mock-video-v1','video',$3,$4,$9,$5,$6,$7,$8)`, id, identity.OwnerID(ctx), name, storageOwner, media.StorageKey, frame.StorageKey, media.SizeBytes, media.SHA256, identity.SharedOwnerID); err != nil {
			t.Fatal(err)
		}
		results = append(results, legacyResult{id: id, name: name, media: media})
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO stored_objects(owner_id,storage_key) VALUES($1,'legacy-unknown-reference')`, identity.OwnerID(ctx)); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if version, err := goose.GetDBVersion(db); err != nil || version < 65 {
		t.Fatalf("private index upgrade did not reach at least schema 65: %d %v", version, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM result_objects`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("upgrade unexpectedly backfilled the private index: %d %v", count, err)
	}
	cipher, err := providerconfig.NewCipher("synthetic-legacy-result-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := storage.NewManager(settings, cipher, root, http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	shared := identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
	usage, err := manager.SharedUsage(shared)
	if err != nil || usage.UsedBytes != 22 || usage.UnknownObjects != 1 {
		t.Fatalf("upgrade changed existing quota rows: %+v %v", usage, err)
	}
	repo := NewTaskRepository(db)
	service := core.NewTaskService(repo, nil, "mock", "mock-video-v1")
	for _, original := range results {
		task, err := repo.Get(ctx, original.id)
		if err != nil || task.StorageKey == nil || *task.StorageKey != original.media.StorageKey || task.LastFrameStorageKey == nil || *task.LastFrameStorageKey != original.media.LastFrameStorageKey || task.Prompt != original.name {
			t.Fatalf("upgrade changed legacy result metadata: %+v %v", task, err)
		}
		for i := 0; i < 2; i++ {
			if err := service.DeleteResult(ctx, original.id, manager.DeleteResult); err != nil {
				t.Fatalf("legacy %s deletion #%d: %v", original.name, i+1, err)
			}
		}
		for _, key := range []string{original.media.StorageKey, original.media.LastFrameStorageKey} {
			if object, err := manager.Open(ctx, key); !errors.Is(err, storage.ErrObjectNotFound) {
				if err == nil {
					object.Reader.Close()
				}
				t.Fatalf("legacy artifact survived deletion: %s %v", key, err)
			}
		}
		task, err = repo.Get(ctx, original.id)
		if err != nil || task.ResultDeletedAt == nil || task.Prompt != original.name {
			t.Fatalf("legacy history lost: %+v %v", task, err)
		}
	}
	usage, err = manager.SharedUsage(shared)
	if err != nil || usage.UsedBytes != 0 || usage.UnknownObjects != 1 {
		t.Fatalf("legacy deletion changed unrelated quota rows: %+v %v", usage, err)
	}
	t.Log("64→65 preserves canonical and bare result/frame keys, history and quota; no private backfill; repeated DeleteResult succeeds")
}
