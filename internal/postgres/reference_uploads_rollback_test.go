package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Multipart responses reuse the direct-upload fixture in internal/storage/direct_test.go.
type rollbackMultipartS3 struct {
	mu                          sync.Mutex
	requests                    []string
	size                        int64
	digest                      string
	data                        []byte
	badSize, badHash, completed bool
	omitChecksum                bool
}

func (m *rollbackMultipartS3) RoundTrip(r *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := r.URL.Query()
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/xml")
	m.requests = append(m.requests, r.Method+" "+r.URL.RequestURI())
	switch {
	case r.Method == "POST" && q.Has("uploads"):
		if r.Header.Get("X-Amz-Checksum-Algorithm") != "" {
			return nil, fmt.Errorf("optional multipart checksum extension is incompatible with R2")
		}
		m.digest = r.Header.Get("X-Amz-Meta-Sha256")
		if m.data == nil {
			m.data = testmedia.PNG512("rollback")
		}
		fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`)
	case r.Method == "GET" && q.Get("uploadId") != "":
		if m.completed {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			break
		}
		size := m.size
		if m.badSize {
			size++
		}
		digest, _ := hex.DecodeString(m.digest)
		if m.badHash {
			digest = []byte("wrong")
		}
		encoded := base64.StdEncoding.EncodeToString(digest)
		if m.omitChecksum {
			encoded = ""
		}
		fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><Size>%d</Size><ETag>"part-etag"</ETag><ChecksumSHA256>%s</ChecksumSHA256></Part></ListPartsResult>`, size, encoded)
	case r.Method == "POST" && q.Get("uploadId") == "upload-1":
		body, _ := io.ReadAll(r.Body)
		if (!m.omitChecksum && !strings.Contains(string(body), "ChecksumSHA256")) || !strings.Contains(string(body), "part-etag") {
			return nil, fmt.Errorf("missing pinned part metadata")
		}
		m.completed = true
		fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"done"</ETag></CompleteMultipartUploadResult>`)
	case r.Method == "GET" && q.Get("uploadId") == "" && !q.Has("uploads"):
		w.Header().Set("Content-Type", "image/png")
		w.Write(m.data)
	case r.Method == "HEAD":
		w.Header().Set("Content-Length", fmt.Sprint(m.size))
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-Amz-Meta-Sha256", m.digest)
	case r.Method == "GET" && q.Has("uploads"):
		fmt.Fprint(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	default:
		return nil, fmt.Errorf("unexpected S3 byte transfer: %s", r.Method+" "+r.URL.String())
	}
	return w.Result(), nil
}

// Extend the existing fixture with keyed objects, one client PUT, and deletion.
// Every HTTP request is handled locally; no default transport is reachable.
type rollbackRemote struct {
	objects                            map[string]*rollbackMultipartS3
	uploaded                           map[string]bool
	deleted                            map[string]bool
	requests                           []string
	puts, completes, recoveries, heads int
}

func (r *rollbackRemote) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.Host + req.URL.Path
	r.requests = append(r.requests, req.Method+" "+req.URL.Host+req.URL.RequestURI())
	if req.URL.Host != "a.example" {
		return nil, fmt.Errorf("unexpected destination: %s", req.URL.Host)
	}
	q := req.URL.Query()
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/xml")
	if req.Method == "GET" && q.Has("uploads") {
		fmt.Fprint(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
		return w.Result(), nil
	}
	f := r.objects[key]
	if req.Method == "POST" && q.Has("uploads") {
		if f != nil {
			return nil, errors.New("reinitiated multipart")
		}
		f = &rollbackMultipartS3{size: 512}
		r.objects[key] = f
	}
	if f == nil {
		return nil, fmt.Errorf("unknown object: %s", key)
	}
	if req.Method == "PUT" && q.Get("partNumber") == "1" {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		if len(data) != 512 || hex.EncodeToString(sum[:]) != f.digest || req.Header.Get("X-Amz-Content-Sha256") != f.digest {
			return nil, errors.New("client payload mismatch")
		}
		if r.uploaded[key] {
			return nil, errors.New("repeated byte upload")
		}
		f.data = append([]byte{}, data...)
		r.uploaded[key] = true
		r.puts++
		w.Header().Set("ETag", `"part-etag"`)
		return w.Result(), nil
	}
	if req.Method == "DELETE" && q.Get("uploadId") == "" {
		if !f.completed || r.deleted[key] {
			return nil, errors.New("unexpected object deletion")
		}
		r.deleted[key] = true
		return w.Result(), nil
	}
	if req.Method == "HEAD" {
		r.heads++
		if !f.completed || r.deleted[key] {
			w.WriteHeader(404)
			return w.Result(), nil
		}
	}
	if req.Method == "GET" && q.Get("uploadId") != "" {
		if !r.uploaded[key] {
			return nil, errors.New("part not uploaded")
		}
		if f.completed {
			r.recoveries++
		}
	}
	if req.Method == "POST" && q.Get("uploadId") != "" {
		r.completes++
	}
	return f.RoundTrip(req)
}

type rollbackSettings struct {
	*StorageSettingsRepository
	connections []string
}

func (r *rollbackSettings) Connection(ctx context.Context, id string) (storage.SettingsRecord, error) {
	r.connections = append(r.connections, identity.StorageOwnerID(ctx)+":"+id)
	return r.StorageSettingsRepository.Connection(ctx, id)
}
func configureRollbackStorage(t *testing.T, db *sql.DB, ctx context.Context, endpoint, bucket string) string {
	t.Helper()
	cipher, _ := providerconfig.NewCipher("research-direct-only")
	config := storage.S3Config{Endpoint: endpoint, Region: "auto", Bucket: bucket, Prefix: "fixture", PathStyle: true, AccessKeyID: "fixture", SecretAccessKey: "fixture"}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt(raw)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err = NewStorageSettingsRepository(db).Save(ctx, storage.SettingsRecord{Driver: "s3", ConnectionID: id, Ciphertext: encrypted}); err != nil {
		t.Fatal(err)
	}
	return id
}
func rollbackSession(t *testing.T, db *sql.DB, owner, id string) references.UploadSession {
	t.Helper()
	var raw []byte
	var s references.UploadSession
	if err := db.QueryRow(`SELECT state FROM reference_uploads WHERE owner_id=$1 AND id=$2`, owner, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func rollbackCounts(t *testing.T, db *sql.DB, owner string) (refs, total, live int) {
	t.Helper()
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM reference_images WHERE owner_id=$1),(SELECT count(*) FROM stored_objects WHERE owner_id=$1),(SELECT count(*) FROM stored_objects WHERE owner_id=$1 AND deleted_at IS NULL)`, owner).Scan(&refs, &total, &live); err != nil {
		t.Fatal(err)
	}
	return
}
func putRollbackPart(t *testing.T, client *http.Client, p references.UploadPlan, data []byte) {
	t.Helper()
	if p.Target == nil {
		t.Fatal("no upload target")
	}
	req, err := http.NewRequest("PUT", p.Target.URL, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range p.Target.Headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
}
func TestDirectDuplicateCompletionCommitRollback(t *testing.T) {
	for _, phase := range []string{"result_commit", "cleanup_commit"} {
		t.Run(phase, func(t *testing.T) {
			db, _, _, base := uploadFixture(t)
			db.SetMaxOpenConns(1)
			owner := identity.OwnerID(base)
			ctx := identity.WithResourceOwners(base, "", identity.SharedOwnerID)
			a := configureRollbackStorage(t, db, ctx, "https://a.example", "bucket-a")
			bOwner := uuid.NewString()
			retryCtx := identity.WithResourceOwners(base, "", bOwner)
			b := configureRollbackStorage(t, db, retryCtx, "https://b.example", "bucket-b")
			t.Cleanup(func() {
				db.Exec(`DELETE FROM storage_settings WHERE owner_id IN ($1,$2)`, identity.SharedOwnerID, bOwner)
				db.Exec(`DELETE FROM storage_connections WHERE id IN ($1,$2)`, a, b)
			})
			remote := &rollbackRemote{objects: map[string]*rollbackMultipartS3{}, uploaded: map[string]bool{}, deleted: map[string]bool{}}
			client := &http.Client{Transport: remote}
			settings := &rollbackSettings{StorageSettingsRepository: NewStorageSettingsRepository(db)}
			cipher, _ := providerconfig.NewCipher("research-direct-only")
			manager, err := storage.NewManager(settings, cipher, t.TempDir(), client, 200<<20)
			if err != nil {
				t.Fatal(err)
			}
			library := references.New(NewReferenceRepository(db), manager)
			payload := testmedia.PNG512("rollback")
			sum := sha256.Sum256(payload)
			input := references.UploadInput{Name: "file.png", ContentType: "image/png", SizeBytes: 512, SHA256: hex.EncodeToString(sum[:])}
			first, err := library.BeginUpload(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			second, err := library.BeginUpload(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			putRollbackPart(t, client, first, payload)
			putRollbackPart(t, client, second, payload)
			published, err := library.FinishUpload(ctx, first.ID)
			if err != nil {
				t.Fatal(err)
			}
			initial := rollbackSession(t, db, owner, second.ID)
			condition := `NEW.state->'storage'->>'upload_id' <> ''`
			if phase == "cleanup_commit" {
				condition = `NEW.state->'storage'->>'upload_id' = ''`
			}
			function := `CREATE FUNCTION research_duplicate_commit_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.id::text='` + second.ID + `' AND (` + condition + `) THEN
 IF NEW.state->>'result_id' <> '` + published.ID + `' OR NOT EXISTS(SELECT 1 FROM stored_objects WHERE owner_id=NEW.owner_id AND storage_key=NEW.state->'storage'->>'key' AND size_bytes=512) THEN
 RAISE EXCEPTION 'fault not after duplicate completion' USING ERRCODE='P0002'; END IF;
 RAISE EXCEPTION 'research duplicate local commit rollback' USING ERRCODE='P0001'; END IF; RETURN NEW; END $$`
			if _, err = db.Exec(function); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`CREATE CONSTRAINT TRIGGER research_duplicate_commit_fault AFTER UPDATE ON reference_uploads DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION research_duplicate_commit_fault()`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				db.Exec(`DROP TRIGGER IF EXISTS research_duplicate_commit_fault ON reference_uploads`)
				db.Exec(`DROP FUNCTION IF EXISTS research_duplicate_commit_fault()`)
			})
			_, err = library.FinishUpload(ctx, second.ID)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "P0001" {
				t.Fatalf("wrong commit fault: %v", err)
			}
			if remote.completes != 2 {
				t.Fatal("duplicate completion not reached")
			}
			wantTotal, wantLive := 1, 1
			if phase == "cleanup_commit" {
				wantTotal = 2
				wantLive = 2
			}
			if refs, total, live := rollbackCounts(t, db, owner); refs != 1 || total != wantTotal || live != wantLive {
				t.Fatalf("rollback leaked accounting: %d/%d/%d", refs, total, live)
			}
			after := rollbackSession(t, db, owner, second.ID)
			wantResult := ""
			if phase == "cleanup_commit" {
				wantResult = published.ID
			}
			if after.ResultID != wantResult || after.Storage != initial.Storage {
				t.Fatal("session did not roll back")
			}
			db.Exec(`DROP TRIGGER research_duplicate_commit_fault ON reference_uploads`)
			db.Exec(`DROP FUNCTION research_duplicate_commit_fault()`)
			bRecord, err := settings.StorageSettingsRepository.Connection(retryCtx, b)
			if err != nil {
				t.Fatal(err)
			}
			if err = settings.Save(ctx, bRecord); err != nil {
				t.Fatal(err)
			}
			before := len(remote.requests)
			foreign := identity.WithOwnerID(retryCtx, uuid.NewString())
			if _, err = library.FinishUpload(foreign, second.ID); !errors.Is(err, storage.ErrObjectNotFound) || len(remote.requests) != before {
				t.Fatal("foreign owner completed session or contacted S3", err)
			}
			settings.connections = nil
			item, err := library.FinishUpload(retryCtx, second.ID)
			if err != nil || item.ID != published.ID || !item.Duplicate {
				t.Fatalf("retry after duplicate remote completion and local rollback: id=%q duplicate=%v err=%v puts=%d completes=%d NoSuchUpload=%d deleted=%d", item.ID, item.Duplicate, err, remote.puts, remote.completes, remote.recoveries, len(remote.deleted))
			}

			if remote.puts != 2 || remote.completes != 2 || len(remote.deleted) != 1 {
				t.Fatalf("repeated bytes/completion or wrong deletion: %+v", remote)
			}
			winnerURL, err := url.Parse(first.Target.URL)
			if err != nil {
				t.Fatal(err)
			}
			duplicateURL, err := url.Parse(second.Target.URL)
			if err != nil {
				t.Fatal(err)
			}
			winnerKey := winnerURL.Host + winnerURL.Path
			duplicateKey := duplicateURL.Host + duplicateURL.Path
			if winnerKey == duplicateKey || !remote.deleted[duplicateKey] || remote.deleted[winnerKey] {
				t.Fatalf("cleanup deleted the wrong remote key: duplicate=%s winner=%s deleted=%v", duplicateKey, winnerKey, remote.deleted)
			}
			if refs, total, live := rollbackCounts(t, db, owner); refs != 1 || total != 2 || live != 1 {
				t.Fatalf("retry metadata %d/%d/%d", refs, total, live)
			}
			var accountKey string
			var size int64
			if err = db.QueryRow(`SELECT storage_key,size_bytes FROM stored_objects WHERE owner_id=$1 AND deleted_at IS NULL`, owner).Scan(&accountKey, &size); err != nil || accountKey != published.StorageKey || size != 512 {
				t.Fatal("wrong accounting", accountKey, size, err)
			}
			recovered := rollbackSession(t, db, owner, second.ID)
			if recovered.ResultID != published.ID || recovered.Storage.UploadID != "" || recovered.Storage.StorageOwner != identity.SharedOwnerID || recovered.Storage.Key != initial.Storage.Key {
				t.Fatal("recovery lost pinned state")
			}
			before = len(remote.requests)
			if _, err = library.FinishUpload(retryCtx, second.ID); err != nil || len(remote.requests) != before {
				t.Fatal("repeated success contacted S3", err)
			}
			expireUploads(t, db, owner)
			if err = library.CleanupUploads(retryCtx); err != nil {
				t.Fatal(err)
			}
			if _, err = library.Get(retryCtx, published.ID); err != nil {
				t.Fatal("cleanup lost published reference", err)
			}
			if refs, total, live := rollbackCounts(t, db, owner); refs != 1 || total != 2 || live != 1 || len(remote.deleted) != 1 {
				t.Fatalf("cleanup altered publication/accounting: %d/%d/%d", refs, total, live)
			}
			var sessions int
			if err = db.QueryRow(`SELECT count(*) FROM reference_uploads WHERE owner_id=$1`, owner).Scan(&sessions); err != nil || sessions != 0 {
				t.Fatal("sessions remain", err)
			}
			if len(settings.connections) == 0 {
				t.Fatal("no pinned lookups")
			}
			for _, connection := range settings.connections {
				if connection != identity.SharedOwnerID+":"+a {
					t.Fatal("lost original connection/storage owner", connection)
				}
			}
			t.Logf("phase=%s: injected SQL P0001 after remote completion, retry reused winner, puts=%d completes=%d NoSuchUpload=%d deleted=%d; final=1reference/1live-accounting(512bytes)+1deleted-accounting; original A connection/owner retained; foreign=no HTTP; cleanup preserved winner; sessions=0", phase, remote.puts, remote.completes, remote.recoveries, len(remote.deleted))
		})
	}
}
