package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

func acceptedPGFixture(t *testing.T) (*providerResourceFixture, *TaskRepository, context.Context, core.Task, core.ProviderTask) {
	t.Helper()
	f := newProviderResourceFixture(t)
	repo := NewTaskRepository(f.db)
	now := time.Now().UTC()
	task := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(f.ctx), ProviderOwnerID: identity.ProviderOwnerID(f.ctx), StorageOwnerID: identity.StorageOwnerID(f.ctx), Provider: "mock--actual-connection", Model: "immediate-batch", ProviderIdentity: "receipt-private-revision", MediaType: "image", Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now}
	var e error
	task, _, e = repo.CreateOrGet(f.ctx, task, "", "receipt-fixture")
	if e != nil {
		t.Fatal(e)
	}
	claim := core.WithClaimToken(f.ctx, uuid.NewString())
	if ok, e := repo.AcquireTask(claim, task.ID, time.Minute); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if e = repo.MarkRunning(claim, task.ID); e != nil {
		t.Fatal(e)
	}
	task, e = repo.Get(f.ctx, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	p := core.ProviderTask{Artifacts: []core.ProviderArtifact{{ID: "upstream-one", MediaType: "image", Format: "png", URL: "https://private-cdn.fixture/one.png"}, {ID: "upstream-two", MediaType: "image", Format: "png", URL: "https://private-cdn.fixture/two.png"}}, ResultURL: "https://private-cdn.fixture/one.png", Usage: &core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "image_count", Unit: "image", Quantity: 2}}}}
	return f, repo, claim, task, p
}
func acceptedPGStored(task core.Task, ctx context.Context, index int) core.StoredArtifact {
	return core.StoredArtifact{ID: []string{"1", "2"}[index], ProviderID: []string{"upstream-one", "upstream-two"}[index], MediaType: "image", Format: "png", StorageKey: task.ID + "/attempts/" + core.ClaimToken(ctx) + "/artifacts/" + []string{"1", "2"}[index] + "/image.png", ContentType: "image/png", SizeBytes: 512, SHA256: strings.Repeat("a", 64)}
}
func TestAcceptedReceiptPostgresAtomicPersistenceScopeAndRestart(t *testing.T) {
	f, repo, claim, task, p := acceptedPGFixture(t)
	if loaded, e := repo.LoadAcceptedReceipt(claim, task); e != nil || loaded != nil {
		t.Fatal("absent receipt", loaded, e)
	}
	if e := repo.SaveAcceptedReceipt(claim, task, p); e != nil {
		t.Fatal(e)
	}
	if e := repo.SaveAcceptedReceipt(claim, task, p); e != nil {
		t.Fatal("identical save not idempotent", e)
	}
	persisted, e := repo.Get(f.ctx, task.ID)
	if e != nil || persisted.ProviderTaskID == nil || *persisted.ProviderTaskID != "" {
		t.Fatal("sync actual native ID not atomic", persisted.ProviderTaskID, e)
	}
	// A new repository object must load all outputs without a Task scanner field.
	loaded, e := NewTaskRepository(f.db).LoadAcceptedReceipt(claim, persisted)
	if e != nil || !reflect.DeepEqual(*loaded, p) {
		t.Fatal("receipt restart lost outcomes", loaded, e)
	}
	public, e := json.Marshal(core.GenerationView(persisted))
	if e != nil {
		t.Fatal(e)
	}
	listed, e := repo.ListRecent(f.ctx, 10)
	if e != nil {
		t.Fatal(e)
	}
	listJSON, _ := json.Marshal(listed)
	for _, private := range []string{"private-cdn.fixture", "upstream-one", "receipt-private-revision", "accepted_provider_receipts"} {
		if strings.Contains(string(public), private) || strings.Contains(string(listJSON), private) {
			t.Fatal("receipt leaked through scans", private)
		}
	}
	altered := p
	altered.Artifacts = append([]core.ProviderArtifact(nil), p.Artifacts...)
	altered.Artifacts[1].URL = "https://private-cdn.fixture/replacement.png"
	if e := repo.SaveAcceptedReceipt(claim, task, altered); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
		t.Fatal("immutable receipt replaced", e)
	}
	for _, field := range []string{"ID", "OwnerID", "ProviderOwnerID", "StorageOwnerID", "Provider", "Model", "ProviderIdentity", "MediaType", "ActorID"} {
		t.Run(field, func(t *testing.T) {
			forged := task
			reflect.ValueOf(&forged).Elem().FieldByName(field).SetString(uuid.NewString())
			if _, e := repo.LoadAcceptedReceipt(claim, forged); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
				t.Fatal("forged binding read", e)
			}
			if e := repo.SaveAcceptedReceipt(claim, forged, p); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
				t.Fatal("forged binding write", e)
			}
		})
	}
	changedContext := core.WithProviderRevision(claim, "replacement-connection-revision")
	if _, e := repo.LoadAcceptedReceipt(changedContext, task); !errors.Is(e, core.ErrProviderChanged) {
		t.Fatal("changed context revision read", e)
	}
	if e := repo.SaveAcceptedReceipt(changedContext, task, p); !errors.Is(e, core.ErrProviderChanged) {
		t.Fatal("changed context revision write", e)
	}
	foreign := identity.WithOwnerID(claim, uuid.NewString())
	if _, e := repo.LoadAcceptedReceipt(foreign, task); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
		t.Fatal("foreign context read", e)
	}
	if _, e := repo.LoadAcceptedReceipt(f.ctx, task); !errors.Is(e, core.ErrLeaseLost) {
		t.Fatal("unclaimed read", e)
	}
	if e := repo.SaveAcceptedReceipt(f.ctx, task, p); !errors.Is(e, core.ErrLeaseLost) {
		t.Fatal("unclaimed write", e)
	}
	if _, e = f.db.Exec(`UPDATE tasks SET provider_task_id='tampered-native-id' WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = repo.LoadAcceptedReceipt(claim, task); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
		t.Fatal("native-ID mismatch accepted", e)
	}
}
func TestAcceptedReceiptPostgresTamperBoundAndClaimFencing(t *testing.T) {
	f, repo, claim, task, p := acceptedPGFixture(t)
	if e := repo.SaveAcceptedReceipt(claim, task, p); e != nil {
		t.Fatal(e)
	}
	var raw []byte
	var digest string
	if e := f.db.QueryRow(`SELECT receipt,sha256 FROM accepted_provider_receipts WHERE task_id=$1`, task.ID).Scan(&raw, &digest); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []string{"digest", "task binding", "schema", "shape"} {
		t.Run(tc, func(t *testing.T) {
			bad := append([]byte(nil), raw...)
			badDigest := digest
			if tc == "digest" {
				bad[len(bad)-2] ^= 1
			} else {
				var v map[string]any
				_ = json.Unmarshal(bad, &v)
				switch tc {
				case "task binding":
					v["binding"].(map[string]any)["owner_id"] = uuid.NewString()
				case "schema":
					v["version"] = 9
				case "shape":
					v["result"].(map[string]any)["Artifacts"].([]any)[0].(map[string]any)["MediaType"] = "binary"
				}
				bad, _ = json.Marshal(v)
				sum := sha256.Sum256(bad)
				badDigest = hex.EncodeToString(sum[:])
			}
			if _, e := f.db.Exec(`UPDATE accepted_provider_receipts SET receipt=$2,sha256=$3 WHERE task_id=$1`, task.ID, bad, badDigest); e != nil {
				t.Fatal(e)
			}
			if _, e := repo.LoadAcceptedReceipt(claim, task); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
				t.Fatal("tamper loaded", e)
			}
			if _, e := f.db.Exec(`UPDATE accepted_provider_receipts SET receipt=$2,sha256=$3 WHERE task_id=$1`, task.ID, raw, digest); e != nil {
				t.Fatal(e)
			}
		})
	}
	// PostgreSQL itself rejects a corrupted oversized row before readers allocate.
	if _, e := f.db.Exec(`UPDATE accepted_provider_receipts SET receipt=repeat('x',$2)::bytea WHERE task_id=$1`, task.ID, core.MaxAcceptedReceiptBytes+1); e == nil {
		t.Fatal("database accepted oversized receipt")
	}
	if _, e := f.db.Exec(`UPDATE tasks SET worker_lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	next := core.WithClaimToken(f.ctx, uuid.NewString())
	if ok, e := repo.AcquireTask(next, task.ID, time.Minute); e != nil || !ok {
		t.Fatal(ok, e)
	}
	for _, old := range []context.Context{claim, core.WithClaimToken(f.ctx, uuid.NewString())} {
		if _, e := repo.LoadAcceptedReceipt(old, task); !errors.Is(e, core.ErrLeaseLost) {
			t.Fatal("stale claim read", e)
		}
		if e := repo.SaveAcceptedReceipt(old, task, p); !errors.Is(e, core.ErrLeaseLost) {
			t.Fatal("stale claim write", e)
		}
	}
	if got, e := repo.LoadAcceptedReceipt(next, task); e != nil || got == nil {
		t.Fatal("successor cannot recover", got, e)
	}
}
func TestAcceptedReceiptPostgresProgressPrefixSurvivesClaimTransfer(t *testing.T) {
	f, repo, claim, task, p := acceptedPGFixture(t)
	p.LastFrameURL = "https://private-cdn.fixture/last.png"
	if e := repo.SaveAcceptedReceipt(claim, task, p); e != nil {
		t.Fatal(e)
	}
	first := acceptedPGStored(task, claim, 0)
	if e := repo.SaveAcceptedReceiptProgress(claim, task, []core.StoredArtifact{first}, nil); e != nil {
		t.Fatal(e)
	}
	prefix, last, e := repo.LoadAcceptedReceiptProgress(claim, task)
	if e != nil || last != nil || !reflect.DeepEqual(prefix, []core.StoredArtifact{first}) {
		t.Fatal(prefix, last, e)
	}
	for _, edit := range []func(*core.StoredArtifact){func(a *core.StoredArtifact) { a.StorageKey = "../escape" }, func(a *core.StoredArtifact) { a.SHA256 = "invalid" }, func(a *core.StoredArtifact) { a.ProviderID = "different" }, func(a *core.StoredArtifact) { a.ContentType = "video/mp4" }, func(a *core.StoredArtifact) { a.ID = "3" }, func(a *core.StoredArtifact) { a.StorageKey = task.ID + "/attempts/foreign/image.png" }} {
		second := acceptedPGStored(task, claim, 1)
		edit(&second)
		if e := repo.SaveAcceptedReceiptProgress(claim, task, []core.StoredArtifact{first, second}, nil); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
			t.Fatal("invalid appended progress", second, e)
		}
	}
	changed := first
	changed.SHA256 = strings.Repeat("b", 64)
	if e := repo.SaveAcceptedReceiptProgress(claim, task, []core.StoredArtifact{changed}, nil); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
		t.Fatal("saved prefix changed", e)
	}
	if e := repo.SaveAcceptedReceiptProgress(claim, task, nil, nil); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
		t.Fatal("prefix truncated", e)
	}
	if _, e = f.db.Exec(`UPDATE tasks SET worker_lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	next := core.WithClaimToken(f.ctx, uuid.NewString())
	if ok, e := repo.AcquireTask(next, task.ID, time.Minute); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if e := repo.SaveAcceptedReceiptProgress(claim, task, []core.StoredArtifact{first}, nil); !errors.Is(e, core.ErrLeaseLost) {
		t.Fatal("old claim progress write", e)
	}
	second := acceptedPGStored(task, next, 1)
	lastFrame := core.StoredArtifact{ID: "last-frame", MediaType: "image", StorageKey: task.ID + "/attempts/" + core.ClaimToken(next) + "/last-frame/image.png", ContentType: "image/png", SizeBytes: 512, SHA256: strings.Repeat("b", 64)}
	if e := repo.SaveAcceptedReceiptProgress(next, task, []core.StoredArtifact{first, second}, &lastFrame); e != nil {
		t.Fatal("successor append failed", e)
	}
	prefix, last, e = NewTaskRepository(f.db).LoadAcceptedReceiptProgress(next, task)
	if e != nil || !reflect.DeepEqual(prefix, []core.StoredArtifact{first, second}) || !reflect.DeepEqual(last, &lastFrame) {
		t.Fatal("claim transfer lost prefix", prefix, last, e)
	}
	if _, e = f.db.Exec(`UPDATE accepted_provider_receipts SET progress_sha256=$2 WHERE task_id=$1`, task.ID, strings.Repeat("0", 64)); e != nil {
		t.Fatal(e)
	}
	if _, _, e = repo.LoadAcceptedReceiptProgress(next, task); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
		t.Fatal("tampered progress loaded", e)
	}
}
func TestAcceptedReceiptPostgresConcurrentImmutableSaveAndCleanup(t *testing.T) {
	f, repo, claim, task, p := acceptedPGFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- repo.SaveAcceptedReceipt(claim, task, p) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("concurrent identical save", e)
		}
	}
	if e := repo.MarkFailed(claim, task.ID, "safe failure"); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := f.db.QueryRow(`SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1`, task.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("terminal metadata not removed", n, e)
	}
	// Task deletion must also cascade a still-pending accepted response.
	f2, r2, c2, t2, p2 := acceptedPGFixture(t)
	if e := r2.SaveAcceptedReceipt(c2, t2, p2); e != nil {
		t.Fatal(e)
	}
	if _, e := f2.db.Exec(`DELETE FROM tasks WHERE id=$1`, t2.ID); e != nil {
		t.Fatal(e)
	}
	if e := f2.db.QueryRow(`SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1`, t2.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("deleted task retained metadata", n, e)
	}
}

func TestAcceptedReceiptStorageNamespaceValidation(t *testing.T) {
	task := core.Task{ID: uuid.NewString(), OwnerID: uuid.NewString()}
	token := uuid.NewString()
	tail := task.ID + "/attempts/" + token + "/artifacts/1/image.png"
	connection := uuid.NewString()
	for _, key := range []string{tail, task.OwnerID + "/results/" + tail, "s3:" + connection + "/" + task.OwnerID + "/results/" + tail, "s3:" + connection + "/configured/prefix/" + task.OwnerID + "/results/" + tail} {
		if !receiptCurrentClaimPath(task, key, token) {
			t.Fatal("real storage namespace rejected", key)
		}
		if receiptCurrentClaimPath(task, key, uuid.NewString()) {
			t.Fatal("foreign claim path accepted", key)
		}
	}
	for _, key := range []string{"../" + tail, "/" + tail, task.OwnerID + "/results/../" + tail, uuid.NewString() + "/results/" + tail, "s3:not-a-uuid/" + task.OwnerID + "/results/" + tail, "s3:" + connection + "/" + tail, "s3:" + connection + "/prefix/../" + task.OwnerID + "/results/" + tail, tail + "\\escape", tail + "\x00"} {
		if receiptCurrentClaimPath(task, key, token) {
			t.Fatal("unbound namespace accepted", key)
		}
	}
}

func TestAcceptedReceiptPostgresTypedResourceAndLegacyRevision(t *testing.T) {
	f, repo, claim, task, _ := acceptedPGFixture(t)
	task.ProviderIdentity = ""
	task.MediaType = "resource"
	if _, e := f.db.Exec(`UPDATE tasks SET provider_identity='',media_type='resource' WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	p := core.ProviderTask{Resources: []core.ProviderResource{{RemoteID: "private-editing-session", Kind: "editing_session", Subtype: "selection", Status: core.ProviderSucceeded, Session: &core.MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceVideoDuration: 4, SourceSHA256: strings.Repeat("a", 64), FrameCount: 120, FPS: 30, SelectionRevision: 1, HasSelection: true, Selections: []core.FrameSelection{{FrameIndex: 0, Points: []core.SelectionPoint{{X: .7738498789346246, Y: .297142857142857}}}}}}}, Usage: &core.ProviderUsage{CostStatus: "metered"}}
	if e := repo.SaveAcceptedReceipt(claim, task, p); e != nil {
		t.Fatal("legacy empty exact revision rejected", e)
	}
	loaded, e := NewTaskRepository(f.db).LoadAcceptedReceipt(claim, task)
	if e != nil || !reflect.DeepEqual(loaded, &p) {
		t.Fatal("typed resource receipt lost", loaded, e)
	}
	task.ProviderIdentity = "new-revision"
	if _, e = repo.LoadAcceptedReceipt(claim, task); !errors.Is(e, core.ErrAcceptedReceiptInvalid) {
		t.Fatal("legacy receipt upgraded without evidence", e)
	}
}
