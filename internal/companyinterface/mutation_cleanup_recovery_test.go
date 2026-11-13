package companyinterface_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func TestCompanyMutationAbandonedDeletionLeaseExpiresAfterRestart(t *testing.T) {
	f, library, store, ctx := mutationReferenceFixture(t)
	v := f.current(t)
	item, e := library.Upload(ctx, "abandoned.png", bytes.NewReader(testmedia.PNG512("abandoned lease")))
	if e != nil {
		t.Fatal(e)
	}
	// Unwind after the production admission COMMIT, before physical I/O or
	// FinishDeletion. Unlike an error return, this leaves the original lease live.
	crashed := errors.New("fixture process stopped after admission")
	store.hook = func(context.Context, string) { panic(crashed) }
	w := customerRequest(t, f.central, v, true, "DELETE", "/v1/references/"+item.ID, "", v.Tokens.PlaygroundKey, nil)
	if w.Code != 500 || store.count() != 1 {
		t.Fatal("HTTP recovery did not retain abandoned admission", w.Code, store.count())
	}
	if o, e := store.Open(ctx, item.StorageKey); e != nil {
		t.Fatal("crash unexpectedly deleted bytes", e)
	} else {
		o.Reader.Close()
	}

	var lease, state, key string
	var expiry time.Time
	if e = f.centralDB.QueryRow(`SELECT lease_token::text,lease_until,state,storage_key FROM reference_deletion_admissions WHERE reference_id=$1`, item.ID).Scan(&lease, &expiry, &state, &key); e != nil || lease == "" || state != "pending" || key != item.StorageKey || time.Until(expiry) <= 0 {
		t.Fatal("live abandoned lease absent", e)
	}
	revokeMutationFixture(t, f, v, true)
	restartedRepo := postgres.NewReferenceRepository(f.centralDB)
	if claims, e := restartedRepo.ClaimDeletions(context.Background(), 1); e != nil || len(claims) != 0 {
		t.Fatal("live lease was stolen", e)
	}
	store.hook = nil
	restarted := references.New(restartedRepo, store)
	if e = restarted.CleanupDeletions(context.Background()); e != nil || store.count() != 1 {
		t.Fatal("worker ignored live lease", e, store.count())
	}
	// Exercise the actual SQL clock/lease predicate, without rewriting custody or
	// shortening the lease in the fixture.
	t.Log("waiting for real abandoned lease expiry", expiry.UTC())
	time.Sleep(time.Until(expiry))
	// Go timers are monotonic but the lease is wall-clock. Wait on the database
	// clock itself: hosts that step the wall clock (WSL time sync) lag by seconds.
	for start := time.Now(); ; time.Sleep(100 * time.Millisecond) {
		var expired bool
		if e = f.centralDB.QueryRow(`SELECT lease_until<clock_timestamp() FROM reference_deletion_admissions WHERE reference_id=$1`, item.ID).Scan(&expired); e != nil {
			t.Fatal(e)
		}
		if expired {
			break
		}
		if time.Since(start) > time.Minute {
			t.Fatal("database clock did not pass the lease expiry")
		}
	}
	if e = references.New(postgres.NewReferenceRepository(f.centralDB), store).CleanupDeletions(context.Background()); e != nil {
		t.Fatal(e)
	}
	var terminalLease string
	if e = f.centralDB.QueryRow(`SELECT state,lease_token::text,storage_key FROM reference_deletion_admissions WHERE reference_id=$1`, item.ID).Scan(&state, &terminalLease, &key); e != nil || state != "completed" || terminalLease == lease || key != item.StorageKey {
		t.Fatal("expired claim not replaced/settled exactly", e)
	}
	if store.count() != 2 {
		t.Fatal("unexpected physical calls", store.count())
	}
	if _, e = store.Open(ctx, item.StorageKey); !errors.Is(e, storage.ErrObjectNotFound) {
		t.Fatal("old bytes remain", e)
	}
	if e = restartedRepo.FinishDeletion(context.Background(), references.Deletion{Owner: v.Tokens.Company, Item: item, Lease: lease}, true); e == nil {
		t.Fatal("abandoned claimant settled successor lease")
	}
}

type cleanupS3Transport struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	deletes []string
}

func (s *cleanupS3Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") && r.URL.Query().Get("X-Amz-Signature") == "" {
		return nil, errors.New("missing synthetic SigV4")
	}
	key := r.URL.Host + r.URL.Path
	w := httptest.NewRecorder()
	q := r.URL.Query()
	if r.Method == "POST" && q.Has("uploads") {
		s.types[key] = r.Header.Get("Content-Type")
		fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>fixture-upload</UploadId></InitiateMultipartUploadResult>`)
		return w.Result(), nil
	}
	if r.Method == "GET" && q.Get("uploadId") != "" {
		fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><Size>%d</Size><ETag>"fixture"</ETag></Part></ListPartsResult>`, len(s.objects[key]))
		return w.Result(), nil
	}
	if r.Method == "POST" && q.Get("uploadId") != "" {
		fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"fixture"</ETag></CompleteMultipartUploadResult>`)
		return w.Result(), nil
	}
	switch r.Method {
	case "PUT":
		data, e := io.ReadAll(r.Body)
		if e != nil {
			return nil, e
		}
		s.objects[key] = data
		if r.Header.Get("Content-Type") != "" {
			s.types[key] = r.Header.Get("Content-Type")
		}
		w.Header().Set("ETag", `"fixture"`)
	case "DELETE":
		s.deletes = append(s.deletes, key)
		delete(s.objects, key)
		delete(s.types, key)
		w.WriteHeader(204)
	case "GET", "HEAD":
		data, ok := s.objects[key]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			break
		}
		w.Header().Set("Content-Type", s.types[key])
		sum := sha256.Sum256(data)
		w.Header().Set("X-Amz-Meta-Sha256", hex.EncodeToString(sum[:]))
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("ETag", `"fixture"`)
		if r.Method == "GET" {
			w.Write(data)
		}
	default:
		return nil, fmt.Errorf("unexpected synthetic S3 method %s", r.Method)
	}
	return w.Result(), nil
}

type cleanupManagerStore struct {
	*storage.Manager
	fail bool
}

func (s *cleanupManagerStore) DeleteReference(ctx context.Context, key string) error {
	if s.fail {
		return errors.New("fixture process lost storage access after admission")
	}
	return s.Manager.DeleteReference(ctx, key)
}
func TestCompanyMutationCleanupUsesHistoricalStorageAfterDestinationSwitch(t *testing.T) {
	f, _, _, ctx := mutationReferenceFixture(t)
	v := f.current(t)
	remote := &cleanupS3Transport{objects: map[string][]byte{}, types: map[string]string{}}
	client := &http.Client{Transport: remote}
	cipher, e := providerconfig.NewCipher("synthetic-deletion-storage-key")
	if e != nil {
		t.Fatal(e)
	}
	settings := postgres.NewStorageSettingsRepository(f.centralDB)
	localPath := t.TempDir()
	manager, e := storage.NewManager(settings, cipher, localPath, client, 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	update := storage.Update{Driver: "s3", Endpoint: "https://old.storage.fixture.test", Region: "test", Bucket: "old-bucket", Prefix: "old-prefix", PathStyle: true, AccessKeyID: "old-fixture-key", SecretAccessKey: "old-fixture-secret"}
	if _, e = manager.Update(ctx, update); e != nil {
		t.Fatal(e)
	}
	oldSettings, e := settings.Get(ctx)
	if e != nil {
		t.Fatal(e)
	}
	store := &cleanupManagerStore{Manager: manager}
	library := references.New(postgres.NewReferenceRepository(f.centralDB), store)
	f.central = mutationGateway(t, f, func(d *api.Dependencies) {
		d.Storage = store
		d.References = library
		d.SigningSecret = fixtureMaster
		d.AssetPublicURL = fixtureCentral
		d.MediaURLTTL = time.Hour
	})
	item, e := cleanupDirectUpload(t, ctx, library, client, "old.png", testmedia.PNG512("historical deletion"))
	if e != nil {
		t.Fatal(e)
	}
	untouched, e := cleanupDirectUpload(t, ctx, library, client, "untouched.png", testmedia.PNG512("old unrelated bytes"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(item.StorageKey, "s3:"+oldSettings.ConnectionID+"/") {
		t.Fatal("old connection not captured")
	}
	store.fail = true
	w := customerRequest(t, f.central, v, true, "DELETE", "/v1/references/"+item.ID, "", v.Tokens.PlaygroundKey, nil)
	if w.Code < 400 {
		t.Fatal("deletion failure not reached", w.Code)
	}
	var savedKey, savedOwner, state string
	if e = f.centralDB.QueryRow(`SELECT storage_key,storage_owner_id::text,state FROM reference_deletion_admissions WHERE reference_id=$1`, item.ID).Scan(&savedKey, &savedOwner, &state); e != nil || savedKey != item.StorageKey || savedOwner != item.StorageOwnerID || state != "pending" {
		t.Fatal("historical custody missing", e)
	}
	update.Endpoint = "https://new.storage.fixture.test"
	update.Bucket = "new-bucket"
	update.Prefix = "new-prefix"
	update.AccessKeyID = "new-fixture-key"
	update.SecretAccessKey = "new-fixture-secret"
	if _, e = manager.Update(ctx, update); e != nil {
		t.Fatal(e)
	}
	newSettings, e := settings.Get(ctx)
	if e != nil || newSettings.ConnectionID == oldSettings.ConnectionID {
		t.Fatal("actual persisted connection did not change", e)
	}
	replacement, e := cleanupDirectUpload(t, ctx, library, client, "new.png", testmedia.PNG512("new destination bytes"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(replacement.StorageKey, "s3:"+newSettings.ConnectionID+"/") {
		t.Fatal("new destination not used")
	}
	revokeMutationFixture(t, f, v, true)
	remote.mu.Lock()
	before := len(remote.deletes)
	remote.mu.Unlock()
	// Fresh manager/repository/library reconstruct historical credentials solely
	// from persisted connection custody, despite a different current destination.
	restartedManager, e := storage.NewManager(postgres.NewStorageSettingsRepository(f.centralDB), cipher, localPath, client, 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	restarted := references.New(postgres.NewReferenceRepository(f.centralDB), restartedManager)
	if e = restarted.CleanupDeletions(context.Background()); e != nil {
		t.Fatal(e)
	}
	remote.mu.Lock()
	deleted := append([]string(nil), remote.deletes[before:]...)
	remote.mu.Unlock()
	if len(deleted) != 1 || !strings.HasPrefix(deleted[0], "old.storage.fixture.test/old-bucket/old-prefix/") || !strings.Contains(deleted[0], item.ID) {
		t.Fatal("cleanup used wrong physical target", deleted)
	}
	if _, e = restartedManager.Open(ctx, item.StorageKey); !errors.Is(e, storage.ErrObjectNotFound) {
		t.Fatal("old target retained", e)
	}
	for _, keep := range []references.Image{untouched, replacement} {
		o, e := restartedManager.Open(ctx, keep.StorageKey)
		if e != nil {
			t.Fatal("unrelated/successor lost", e)
		}
		o.Reader.Close()
	}
	if e = f.centralDB.QueryRow(`SELECT state,storage_key,storage_owner_id::text FROM reference_deletion_admissions WHERE reference_id=$1`, item.ID).Scan(&state, &savedKey, &savedOwner); e != nil || state != "completed" || savedKey != item.StorageKey || savedOwner != item.StorageOwnerID {
		t.Fatal("terminal historical binding changed", e)
	}
	current, e := settings.Get(ctx)
	if e != nil || current.ConnectionID != newSettings.ConnectionID {
		t.Fatal("cleanup changed active settings", e)
	}
	if e = restarted.CleanupDeletions(context.Background()); e != nil {
		t.Fatal(e)
	}
	remote.mu.Lock()
	after := len(remote.deletes)
	remote.mu.Unlock()
	if after != before+1 {
		t.Fatal("terminal cleanup repeated physical call")
	}
}

func cleanupDirectUpload(t *testing.T, ctx context.Context, library *references.Library, client *http.Client, name string, data []byte) (references.Image, error) {
	t.Helper()
	sum := sha256.Sum256(data)
	plan, e := library.BeginUpload(ctx, references.UploadInput{Name: name, ContentType: "image/png", SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	if e != nil {
		return references.Image{}, e
	}
	if plan.Mode != "s3" || plan.Target == nil {
		return references.Image{}, errors.New("missing direct S3 plan")
	}
	req, e := http.NewRequestWithContext(ctx, "PUT", plan.Target.URL, bytes.NewReader(data))
	if e != nil {
		return references.Image{}, e
	}
	for k, v := range plan.Target.Headers {
		req.Header.Set(k, v)
	}
	response, e := client.Do(req)
	if e != nil {
		return references.Image{}, e
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return references.Image{}, fmt.Errorf("fixture PUT %d", response.StatusCode)
	}
	item, e := library.FinishUpload(ctx, plan.ID)
	if e != nil {
		return references.Image{}, e
	}
	return library.Get(ctx, item.ID)
}
