package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/api"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// A completed response body may already be buffered when its worker loses its
// lease. Cancellation cannot roll back a filesystem rename or a remote upload.
type archiveBarrierBody struct {
	reader  *bytes.Reader
	entered chan struct{}
	release chan struct{}
}

func (b *archiveBarrierBody) Read(p []byte) (int, error) {
	if b.reader.Len() > 0 {
		return b.reader.Read(p)
	}
	close(b.entered)
	<-b.release
	return 0, io.EOF
}
func (*archiveBarrierBody) Close() error { return nil }

type archiveRaceTransport struct {
	body     *archiveBarrierBody
	gatePath string
}

func (f archiveRaceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "archive.example.test" {
		return nil, fmt.Errorf("unexpected test egress: %s", r.URL.Host)
	}
	var body io.ReadCloser = io.NopCloser(strings.NewReader(archiveRaceBytes(r.URL.Path)))
	if r.URL.Path == f.gatePath {
		body = f.body
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, Body: body, ContentLength: -1, Request: r}, nil
}

func archiveRaceBytes(path string) string {
	version, artifact := "replacement", "result"
	if strings.HasPrefix(path, "/old") {
		version = "stale"
	}
	if strings.HasSuffix(path, "frame") {
		artifact = "frame"
	}
	return version + " " + artifact + " bytes"
}

type archiveRaceProvider struct {
	polls atomic.Int32
	frame bool
}

func (*archiveRaceProvider) Name() string { return "mock" }
func (*archiveRaceProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	panic("a durable provider ID must never be resubmitted")
}
func (p *archiveRaceProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	path := "/new-"
	if p.polls.Add(1) == 1 {
		path = "/old-"
	}
	result := core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: "https://archive.example.test" + path + "result",
		Usage: &core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "video_duration", Unit: "second", Quantity: 1}}}}
	if p.frame {
		result.LastFrameURL = "https://archive.example.test" + path + "frame"
	}
	return result, nil
}

func TestCancelledArchiveCannotOverwriteReplacementResult(t *testing.T) {
	for _, frame := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			t.Run(fmt.Sprintf("frame=%t/cancelled=%t", frame, cancelled), func(t *testing.T) {
				testCancelledArchive(t, frame, cancelled, false)
			})
		}
	}
}

func TestStorageManagerCancelledArchiveAccounting(t *testing.T) {
	for _, frame := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			t.Run(fmt.Sprintf("frame=%t/cancelled=%t", frame, cancelled), func(t *testing.T) {
				testCancelledArchive(t, frame, cancelled, true)
			})
		}
	}
}

func testCancelledArchive(t *testing.T, frame, cancelled, withManager bool) {
	ctx, db, repo, q, client, prefix := operationTestServices(t)
	if withManager {
		ctx = identity.WithResourceOwners(ctx, identity.SystemOwnerID, identity.SharedOwnerID)
	}
	now := time.Now().UTC()
	id, webhook := uuid.NewString(), "https://receiver.example.test/events"
	task := core.Task{ID: id, Status: core.TaskQueued, Provider: "mock", Model: "test-model", MediaType: "video", Prompt: "synthetic cancellation race",
		CreatedAt: now, UpdatedAt: now, WebhookURL: &webhook, StorageOwnerID: identity.StorageOwnerID(ctx),
		Cost: &core.TaskCost{ExpectedUSD: money.USD(370000), Status: "estimated", Pricing: core.PricingSnapshot{Currency: "USD", Unit: "request", Price: money.USD(370000), Per: 1}}}
	if _, _, err := repo.CreateOrGet(ctx, task, "", "cancellation-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetProviderTaskID(ctx, id, "durable-upstream-id"); err != nil {
		t.Fatal(err)
	}
	gatePath := "/old-result"
	if frame {
		gatePath = "/old-frame"
	}
	body := &archiveBarrierBody{reader: bytes.NewReader([]byte(archiveRaceBytes(gatePath))), entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-body.release:
		default:
			close(body.release)
		}
	}()
	root := t.TempDir()
	transport := &http.Client{Transport: archiveRaceTransport{body, gatePath}}
	var store storage.Store
	var manager *storage.Manager
	var err error
	if withManager {
		cipher, cipherErr := providerconfig.NewCipher("synthetic-manager-accounting")
		if cipherErr != nil {
			t.Fatal(cipherErr)
		}
		limit := int64(len("replacement result bytes"))
		if frame {
			limit += int64(len("replacement frame bytes"))
		}
		if _, limitErr := db.ExecContext(ctx, `UPDATE system_settings SET shared_storage_limit_bytes=$1`, limit); limitErr != nil {
			t.Fatal(limitErr)
		}
		manager, err = storage.NewManager(postgres.NewStorageSettingsRepository(db), cipher, root, transport, 1024)
		store = manager
	} else {
		store, err = storage.NewLocal(root, transport, 1024)
	}
	if err != nil {
		t.Fatal(err)
	}
	p := &archiveRaceProvider{frame: frame}
	w := New(repo, q, p, store, time.Second, time.Minute, 2, time.Second)
	oldCtx, cancelOld := context.WithCancel(core.WithClaimToken(ctx, uuid.NewString()))
	defer cancelOld()
	if err := q.Enqueue(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, err := q.Dequeue(oldCtx); err != nil || got != id {
		t.Fatalf("first delivery: %s %v", got, err)
	}
	oldDone := make(chan error, 1)
	go func() { oldDone <- w.runClaim(oldCtx, id, repo, q) }()
	joined := false
	defer func() {
		cancelOld()
		if !joined {
			select {
			case <-body.release:
			default:
				close(body.release)
			}
			<-oldDone
		}
	}()
	select {
	case <-body.entered:
	case <-ctx.Done():
		t.Fatal("old transfer did not reach EOF barrier", ctx.Err())
	}
	// Expire only this test's delivery and DB leases; no wall-clock race or
	// production lease-duration changes. This models takeover after lease loss.
	if _, err := db.ExecContext(ctx, `UPDATE tasks SET worker_lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(ctx, prefix+"leases", redis.Z{Score: 0, Member: id}).Err(); err != nil {
		t.Fatal(err)
	}
	if cancelled {
		cancelOld()
	}
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	newCtx := core.WithClaimToken(ctx, uuid.NewString())
	if got, err := q.Dequeue(newCtx); err != nil || got != id {
		t.Fatalf("replacement delivery: %s %v", got, err)
	}
	if err := w.runClaim(newCtx, id, repo, q); err != nil {
		t.Fatal(err)
	}
	if withManager {
		inspectManagerAccounting(t, ctx, db, manager, root, id, "winner-published-before-late-effect")
	}
	close(body.release)
	select {
	case err := <-oldDone:
		joined = true
		if err == nil {
			t.Fatal("expired old claim unexpectedly succeeded")
		}
	case <-ctx.Done():
		t.Fatal("old worker did not finish", ctx.Err())
	}
	saved, err := repo.Get(ctx, id)
	if err != nil || saved.Status != core.TaskSucceeded || saved.StorageKey == nil || saved.ResultSHA256 == nil || saved.ResultSize == nil {
		t.Fatalf("terminal state: %+v %v", saved, err)
	}
	object, err := store.Open(ctx, *saved.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(object.Reader)
	object.Reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if string(data) != "replacement result bytes" || hex.EncodeToString(hash[:]) != *saved.ResultSHA256 || int64(len(data)) != *saved.ResultSize {
		t.Fatalf("stale cancelled archive overwrote committed result: bytes=%q hash=%x saved_hash=%s size=%d saved_size=%d", data, hash, *saved.ResultSHA256, len(data), *saved.ResultSize)
	}
	// Read through the normal authenticated content routes as well as storage.
	service := core.NewTaskService(repo, q, "mock", "test-model")
	handler := api.NewWithStorage(service, nil, "synthetic-key", store, "", time.Hour)
	for _, artifact := range []string{"content", "last-frame"} {
		if artifact == "last-frame" && !frame {
			continue
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/generations/"+id+"/"+artifact, nil)
		req.Header.Set("Authorization", "Bearer synthetic-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		want := "replacement result bytes"
		if artifact == "last-frame" {
			want = "replacement frame bytes"
		}
		if response.Code != http.StatusOK || response.Body.String() != want {
			t.Fatalf("download %s: status=%d bytes=%q want=%q", artifact, response.Code, response.Body.String(), want)
		}
	}
	if withManager {
		inspectManagerAccounting(t, ctx, db, manager, root, id, "after-late-effect")
	}
	var payload []byte
	if err := db.QueryRowContext(ctx, `SELECT payload FROM webhook_deliveries WHERE task_id=$1 AND event_type='task.succeeded'`, id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			Status string         `json:"status"`
			SHA256 string         `json:"result_sha256"`
			Size   int64          `json:"result_size_bytes"`
			Cost   *core.TaskCost `json:"cost"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &event); err != nil || event.Type != "generation.completed" || event.Data.Status != "completed" || event.Data.SHA256 != *saved.ResultSHA256 || event.Data.Size != int64(len(data)) {
		t.Fatalf("terminal event disagrees with result: %s %v", payload, err)
	}
	var terminal int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM webhook_deliveries WHERE task_id=$1 AND event_type IN ('task.succeeded','task.failed')`, id).Scan(&terminal); err != nil || terminal != 1 {
		t.Fatalf("terminal event count=%d err=%v", terminal, err)
	}
	if saved.Cost == nil || saved.Cost.MeteredUSD == nil || *saved.Cost.MeteredUSD != money.USD(370000) || saved.Cost.ActualUSD != nil || saved.Cost.ActualSource != "" {
		t.Fatalf("cancellation changed estimated usage into a refund: %+v", saved.Cost)
	}
	if event.Data.Cost == nil || event.Data.Cost.MeteredUSD == nil || *event.Data.Cost.MeteredUSD != *saved.Cost.MeteredUSD || event.Data.Cost.ActualUSD != nil || event.Data.Cost.ActualSource != "" {
		t.Fatalf("terminal cost snapshot disagrees: %+v", event.Data.Cost)
	}
	if p.polls.Load() != 2 {
		t.Fatalf("poll count=%d", p.polls.Load())
	}
	// A further at-least-once delivery must be a terminal no-op.
	if err := q.Enqueue(ctx, id); err != nil {
		t.Fatal(err)
	}
	duplicateCtx := core.WithClaimToken(ctx, uuid.NewString())
	if got, err := q.Dequeue(duplicateCtx); err != nil || got != id {
		t.Fatalf("duplicate delivery: %s %v", got, err)
	}
	if err := w.runClaim(duplicateCtx, id, repo, q); err != nil || p.polls.Load() != 2 {
		t.Fatalf("terminal duplicate did work: polls=%d err=%v", p.polls.Load(), err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM webhook_deliveries WHERE task_id=$1 AND event_type IN ('task.succeeded','task.failed')`, id).Scan(&terminal); err != nil || terminal != 1 {
		t.Fatalf("duplicate changed terminal event count=%d err=%v", terminal, err)
	}
	if withManager {
		if err := service.DeleteResult(ctx, id, manager.DeleteResult); err != nil {
			t.Fatal(err)
		}
		inspectManagerAccounting(t, ctx, db, manager, root, id, "after-normal-result-deletion")
	}
}

func inspectManagerAccounting(t *testing.T, ctx context.Context, db *sql.DB, manager *storage.Manager, root, id, phase string) {
	t.Helper()
	usage, err := manager.SharedUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT storage_key,size_bytes,task_id::text FROM stored_objects WHERE task_id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		t.Fatal(err)
	}
	recorded := map[string]int64{}
	for rows.Next() {
		var key, taskID string
		var size int64
		if err := rows.Scan(&key, &size, &taskID); err != nil {
			t.Fatal(err)
		}
		if taskID != id {
			t.Fatalf("attempt key lost task association: %s", taskID)
		}
		recorded[key] = size
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var trackedBytes, physicalBytes, untrackedBytes int64
	var files, untracked int
	var mismatches []string
	for key, size := range recorded {
		trackedBytes += size
		object, err := manager.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		object.Reader.Close()
		if object.SizeBytes != size {
			mismatches = append(mismatches, fmt.Sprintf("%s db=%d actual=%d", key, size, object.SizeBytes))
		}
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || strings.HasSuffix(entry.Name(), ".part") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files++
		physicalBytes += info.Size()
		if _, ok := recorded[filepath.ToSlash(relative)]; !ok {
			untracked++
			untrackedBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !usage.Applies || usage.UnknownObjects != 0 || usage.UsedBytes != trackedBytes {
		t.Fatalf("quota differs from tracked objects: usage=%+v tracked=%d", usage, trackedBytes)
	}
	snapshot := map[string]any{"phase": phase, "physical_files": files, "physical_bytes": physicalBytes, "quota_bytes": usage.UsedBytes, "limit_bytes": usage.LimitBytes, "exceeded": usage.Exceeded, "physical_equals_quota": physicalBytes == usage.UsedBytes, "tracked_objects": len(recorded), "untracked_objects": untracked, "untracked_bytes": untrackedBytes, "tracked_size_mismatches": mismatches}
	raw, _ := json.Marshal(snapshot)
	t.Logf("MANAGER_ACCOUNTING %s", raw)
	if len(mismatches) != 0 {
		t.Errorf("recorded objects disagree with actual files: %v", mismatches)
	}
	if physicalBytes != usage.UsedBytes {
		t.Errorf("physical/quota invariant failed: physical=%d quota=%d untracked=%d", physicalBytes, usage.UsedBytes, untrackedBytes)
	}
}

// A completed physical archive must be recorded even when its worker context
// is cancelled before bookkeeping. No replacement worker is needed.
func TestManagerRecordsCompletedArchiveAfterCancellation(t *testing.T) {
	ctx, db, repo, _, _, _ := operationTestServices(t)
	ctx = identity.WithResourceOwners(ctx, identity.SystemOwnerID, identity.SharedOwnerID)
	now := time.Now().UTC()
	id := uuid.NewString()
	task := core.Task{ID: id, Status: core.TaskQueued, Provider: "mock", Model: "test-model", MediaType: "video", Prompt: "existing manager gap", CreatedAt: now, UpdatedAt: now, StorageOwnerID: identity.SharedOwnerID}
	if _, _, err := repo.CreateOrGet(ctx, task, "", "existing-manager-gap"); err != nil {
		t.Fatal(err)
	}
	body := &archiveBarrierBody{reader: bytes.NewReader([]byte("stale result bytes")), entered: make(chan struct{}), release: make(chan struct{})}
	root := t.TempDir()
	cipher, err := providerconfig.NewCipher("synthetic-manager-gap")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := storage.NewManager(postgres.NewStorageSettingsRepository(db), cipher, root, &http.Client{Transport: archiveRaceTransport{body: body, gatePath: "/old-result"}}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	selected, err := manager.ForArchive(writeCtx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := selected.Archive(writeCtx, id, "https://archive.example.test/old-result")
		done <- err
	}()
	select {
	case <-body.entered:
	case <-ctx.Done():
		close(body.release)
		<-done
		t.Fatal(ctx.Err())
	}
	cancel()
	close(body.release)
	if err := <-done; err != nil {
		t.Fatalf("completed archive accounting was cancelled: %v", err)
	}
	usage, err := manager.SharedUsage(ctx)
	if err != nil || usage.UsedBytes != 18 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	var physical int64
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, e := entry.Info()
			if e != nil {
				return e
			}
			physical += info.Size()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if physical != 18 {
		t.Fatalf("existing gap physical bytes=%d", physical)
	}
	t.Logf("COMPLETED_ARCHIVE_ACCOUNTING canonical task path, no replacement: physical=%d quota=%d", physical, usage.UsedBytes)
}
