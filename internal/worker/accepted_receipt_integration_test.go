package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

type receiptImmediateProvider struct{ submissions *atomic.Int32 }

func (receiptImmediateProvider) Name() string { return "mock" }
func (p receiptImmediateProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	p.submissions.Add(1)
	return core.ProviderTask{Artifacts: []core.ProviderArtifact{
		{ID: "private-native-first", MediaType: "image", Format: "png", URL: "https://receipt.fixture/first.png?signature=private"},
		{ID: "private-native-second", MediaType: "image", Format: "png", URL: "https://receipt.fixture/second.png?signature=private"},
	}, Usage: &core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "image_count", Unit: "image", Quantity: 2}}}}, nil
}
func (receiptImmediateProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{}, errors.New("synchronous images have no query endpoint")
}

type receiptTransport struct{ fetches *atomic.Int32 }

func (tr receiptTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "receipt.fixture" || r.Header.Get("Authorization") != "" {
		return nil, errors.New("unexpected receipt test transport")
	}
	tr.fetches.Add(1)
	data := []byte("\x89PNG\r\n\x1a\naccepted-" + r.URL.Path)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
}

type receiptInterruptStore struct {
	*storage.Local
	calls  int
	before int
	cancel context.CancelFunc
}

func (s *receiptInterruptStore) Archive(ctx context.Context, id, source string) (core.StoredMedia, error) {
	s.calls++
	if s.calls == s.before {
		s.cancel()
		return core.StoredMedia{}, ctx.Err()
	}
	return s.Local.Archive(ctx, id, source)
}

func TestAcceptedReceiptWorkerRestartBeforeAndBetweenArtifacts(t *testing.T) {
	for _, before := range []int{1, 2} {
		t.Run(fmt.Sprintf("before=%d", before), func(t *testing.T) {
			base, db, repo, _, _, _ := operationTestServices(t)
			ctx := identity.WithOwnerID(base, identity.SystemOwnerID)
			now := time.Now().UTC()
			id := uuid.NewString()
			webhook := "https://receipt-events.fixture/receive"
			task := core.Task{WebhookURL: &webhook, ID: id, Status: core.TaskQueued, Provider: "mock", Model: "mock-image-v1", MediaType: "image", ProviderIdentity: "accepted-revision", CreatedAt: now, UpdatedAt: now, Cost: &core.TaskCost{EstimatedUnits: 2, ExpectedUSD: 7000, Pricing: core.PricingSnapshot{Currency: "USD", Unit: "image", Price: 3500, Per: 1}}}
			if _, _, err := repo.CreateOrGet(ctx, task, "", "receipt-fixture"); err != nil {
				t.Fatal(err)
			}
			task, err := repo.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			ctx = identity.WithResourceOwners(ctx, task.ProviderOwnerID, task.StorageOwnerID)
			firstCtx, cancel := context.WithCancel(core.WithClaimToken(ctx, uuid.NewString()))
			if ok, err := repo.AcquireTask(firstCtx, id, time.Minute); err != nil || !ok {
				t.Fatal(ok, err)
			}
			root := t.TempDir()
			var submissions, fetches atomic.Int32
			client := &http.Client{Transport: receiptTransport{&fetches}}
			local, err := storage.NewLocal(root, client, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			firstStore := &receiptInterruptStore{Local: local, before: before, cancel: cancel}
			first := New(repo, nil, receiptImmediateProvider{&submissions}, firstStore, time.Millisecond, time.Minute, 2, time.Second)
			if err = first.process(firstCtx, id); !errors.Is(err, context.Canceled) {
				t.Fatalf("interruption %v", err)
			}
			cancel()
			if err = repo.ReleaseTask(context.WithoutCancel(firstCtx), id); err != nil {
				t.Fatal(err)
			}
			pending, err := repo.Get(ctx, id)
			if err != nil || pending.Status != core.TaskRunning {
				t.Fatal(pending.Status, err)
			}
			public, _ := json.Marshal(core.GenerationView(pending))
			for _, secret := range []string{"receipt.fixture", "private-native", "signature=", "accepted-revision", "SourceReference"} {
				if bytes.Contains(public, []byte(secret)) {
					t.Fatal("pending receipt leaked", secret)
				}
			}
			var receiptCount int
			if err = db.QueryRowContext(ctx, `SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1`, id).Scan(&receiptCount); err != nil || receiptCount != 1 {
				t.Fatal("accepted response was not durable", receiptCount, err)
			}
			var searchPath string
			if err = db.QueryRowContext(ctx, `SHOW search_path`).Scan(&searchPath); err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(os.Getenv("GATEWAY_TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			query.Set("search_path", searchPath)
			parsed.RawQuery = query.Encode()
			reopened, err := postgres.Open(ctx, parsed.String())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			recoveredRepo := postgres.NewTaskRepository(reopened)
			recoveredStore, err := storage.NewLocal(root, &http.Client{Transport: receiptTransport{&fetches}}, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			secondCtx := core.WithClaimToken(ctx, uuid.NewString())
			if ok, err := recoveredRepo.AcquireTask(secondCtx, id, time.Minute); err != nil || !ok {
				t.Fatal(ok, err)
			}
			second := New(recoveredRepo, nil, receiptImmediateProvider{&submissions}, recoveredStore, time.Millisecond, time.Minute, 2, time.Second)
			if err = second.process(secondCtx, id); err != nil {
				t.Fatal("receipt recovery", err)
			}
			done, err := recoveredRepo.Get(ctx, id)
			if err != nil || done.Status != core.TaskSucceeded || len(done.StoredArtifacts) != 2 {
				t.Fatal("incomplete recovered batch", done.Status, len(done.StoredArtifacts), err)
			}
			if submissions.Load() != 1 || fetches.Load() != 2 {
				t.Fatal("accepted output was submitted or fetched twice", submissions.Load(), fetches.Load())
			}
			if done.Cost == nil || done.Cost.MeteredUSD == nil || *done.Cost.MeteredUSD != 7000 {
				t.Fatal("usage was lost or counted twice", done.Cost)
			}
			for i, a := range done.StoredArtifacts {
				if a.ID != fmt.Sprint(i+1) {
					t.Fatal("unstable output identity", a.ID)
				}
				object, err := recoveredStore.Open(ctx, a.StorageKey)
				if err != nil {
					t.Fatal(err)
				}
				object.Reader.Close()
			}
			files := 0
			err = filepath.WalkDir(root, func(_ string, e os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					files++
				}
				return nil
			})
			if err != nil || files != 2 {
				t.Fatal("duplicate archived assets", files, err)
			}
			if err = reopened.QueryRowContext(ctx, `SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1`, id).Scan(&receiptCount); err != nil || receiptCount != 0 {
				t.Fatal("terminal receipt not cleaned", receiptCount, err)
			}
			var terminal int
			if err = reopened.QueryRowContext(ctx, `SELECT count(*) FROM webhook_deliveries WHERE task_id=$1 AND event_type='task.succeeded'`, id).Scan(&terminal); err != nil || terminal != 1 {
				t.Fatal("terminal event repeated", terminal)
			}
		})
	}
}

// A retained table cannot protect a paid response if an older writer marks an
// unsupported synchronous operation terminal and the cleanup trigger erases it.
func TestAcceptedReceiptRollbackRetainsPendingPaidProgressAgainstLegacyWrites(t *testing.T) {
	base, db, repo, _, _, _ := operationTestServices(t)
	if err := goose.DownTo(db, "migrations", 81); err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithOwnerID(base, identity.SystemOwnerID)
	now := time.Now().UTC()
	id := uuid.NewString()
	task := core.Task{ID: id, Status: core.TaskQueued, Provider: "mock", Model: "mock-image-v1", MediaType: "image", ProviderIdentity: "rollback-receipt-revision", CreatedAt: now, UpdatedAt: now, Cost: &core.TaskCost{EstimatedUnits: 2, ExpectedUSD: 7000, Pricing: core.PricingSnapshot{Currency: "USD", Unit: "image", Price: 3500, Per: 1}}}
	if _, _, err := repo.CreateOrGet(ctx, task, "", "rollback-receipt"); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	ctx = identity.WithResourceOwners(ctx, task.ProviderOwnerID, task.StorageOwnerID)
	if err := goose.UpTo(db, "migrations", 86); err != nil {
		t.Fatal(err)
	}
	claim := core.WithClaimToken(ctx, uuid.NewString())
	if ok, err := repo.AcquireTask(claim, id, time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err = repo.MarkRunning(claim, id); err != nil {
		t.Fatal(err)
	}
	var submissions, fetches atomic.Int32
	provider := receiptImmediateProvider{&submissions}
	accepted, err := provider.Generate(claim, core.MediaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveAcceptedReceipt(claim, task, accepted); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	client := &http.Client{Transport: receiptTransport{&fetches}}
	store, err := storage.NewLocal(root, client, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Archive(claim, id+"/attempts/"+core.ClaimToken(claim), accepted.Artifacts[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveAcceptedReceiptProgress(claim, task, []core.StoredArtifact{storedArtifact(accepted.Artifacts[0], first, 0)}, nil); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetProviderUsage(claim, id, *accepted.Usage); err != nil {
		t.Fatal(err)
	}
	var receiptBefore, progressBefore []byte
	if err = db.QueryRowContext(ctx, `SELECT receipt,progress FROM accepted_provider_receipts WHERE task_id=$1`, id).Scan(&receiptBefore, &progressBefore); err != nil {
		t.Fatal(err)
	}
	if err = repo.ReleaseTask(claim, id); err != nil {
		t.Fatal(err)
	}
	if err = goose.DownTo(db, "migrations", 81); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"failed", "succeeded"} {
		if _, err = db.ExecContext(ctx, `UPDATE tasks SET status=$2,error='legacy unsupported synchronous handle' WHERE id=$1`, id, status); err == nil {
			t.Fatal("legacy terminal write destroyed accepted receipt", status)
		}
	}
	if _, err = db.ExecContext(ctx, `UPDATE tasks SET updated_at=NOW() WHERE id=$1`, id); err != nil {
		t.Fatal("ordinary legacy write rejected", err)
	}
	var receiptAfter, progressAfter []byte
	if err = db.QueryRowContext(ctx, `SELECT receipt,progress FROM accepted_provider_receipts WHERE task_id=$1`, id).Scan(&receiptAfter, &progressAfter); err != nil || !bytes.Equal(receiptBefore, receiptAfter) || !bytes.Equal(progressBefore, progressAfter) {
		t.Fatal("rollback lost accepted evidence", err)
	}
	if err = goose.UpTo(db, "migrations", 86); err != nil {
		t.Fatal(err)
	}
	newer := core.WithClaimToken(ctx, uuid.NewString())
	if ok, err := repo.AcquireTask(newer, id, time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	worker := New(repo, nil, provider, store, time.Millisecond, time.Minute, 2, time.Second)
	if err = worker.process(newer, id); err != nil {
		t.Fatal("re-upgraded receipt recovery", err)
	}
	final, err := repo.Get(ctx, id)
	if err != nil || final.Status != core.TaskSucceeded || len(final.StoredArtifacts) != 2 || submissions.Load() != 1 || fetches.Load() != 2 {
		t.Fatal("rollback recovery duplicated or lost outputs", final.Status, len(final.StoredArtifacts), submissions.Load(), fetches.Load(), err)
	}
	if final.Cost == nil || final.Cost.MeteredUSD == nil || *final.Cost.MeteredUSD != 7000 {
		t.Fatal("rollback lost immutable accounting", final.Cost)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("authorized completion retained pending metadata", count, err)
	}
}
