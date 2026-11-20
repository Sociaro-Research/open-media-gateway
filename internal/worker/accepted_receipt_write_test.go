package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

// The repository executes the real claim, binding, digest and transaction checks.
// Losing the acknowledgment after COMMIT leaves the first envelope in PostgreSQL.
type receiptWriteFault struct {
	*postgres.TaskRepository
	fault  string
	writes int
	next   context.Context
}

func (r *receiptWriteFault) SaveAcceptedReceipt(ctx context.Context, task core.Task, result core.ProviderTask) error {
	r.writes++
	if err := r.TaskRepository.SaveAcceptedReceipt(ctx, task, result); err != nil {
		return err
	}
	if r.fault == "claim-transfer" && r.writes == 1 {
		if err := r.ReleaseTask(ctx, task.ID); err != nil {
			return err
		}
		acquired, err := r.AcquireTask(r.next, task.ID, time.Minute)
		if err != nil || !acquired {
			return fmt.Errorf("successor claim: acquired=%v error=%v", acquired, err)
		}
	}
	if (r.fault == "lost-ack" || r.fault == "claim-transfer") && r.writes == 1 {
		return errors.New("fixture commit acknowledgment lost")
	}
	return nil
}

func TestAcceptedReceiptWriteRecovery(t *testing.T) {
	for _, fault := range []string{"normal", "before-commit", "lost-ack", "claim-transfer", "unavailable"} {
		t.Run(fault, func(t *testing.T) {
			base, db, repo, _, _, _ := operationTestServices(t)
			if fault == "before-commit" || fault == "unavailable" {
				failWrites := 1
				if fault == "unavailable" {
					failWrites = 2
				}
				// nextval survives rollback. A fresh attempt after the simulated
				// outage can commit to the same disposable database.
				_, err := db.ExecContext(base, fmt.Sprintf(`CREATE SEQUENCE fixture_receipt_attempts;
CREATE FUNCTION fixture_receipt_write() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF nextval('fixture_receipt_attempts') <= %d THEN RAISE EXCEPTION 'fixture receipt write unavailable'; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER fixture_receipt_outage BEFORE INSERT ON accepted_provider_receipts FOR EACH ROW EXECUTE FUNCTION fixture_receipt_write()`, failWrites))
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx := identity.WithOwnerID(base, identity.SystemOwnerID)
			now, id := time.Now().UTC(), uuid.NewString()
			task := core.Task{ID: id, Status: core.TaskQueued, Provider: "minimax", Model: "image-01", Prompt: "fixture", Options: core.MediaOptions{OutputCount: 2}, MediaType: "image", CreatedAt: now, UpdatedAt: now, ProviderIdentity: "receipt-fixture", MinimizeRequestLogging: true,
				Cost: &core.TaskCost{ExpectedUSD: 7000, Pricing: core.PricingSnapshot{Currency: "USD", Unit: "image", Price: 3500, Per: 1}}}
			if _, _, err := repo.CreateOrGet(ctx, task, "", "receipt-write"); err != nil {
				t.Fatal(err)
			}
			var submissions, fetches atomic.Int32
			store, err := storage.NewLocal(t.TempDir(), &http.Client{Transport: receiptTransport{&fetches}}, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			claim := core.WithClaimToken(ctx, uuid.NewString())
			if ok, err := repo.AcquireTask(claim, id, time.Minute); err != nil || !ok {
				t.Fatal("acquire", ok, err)
			}
			next := core.WithClaimToken(ctx, uuid.NewString())
			faulty := &receiptWriteFault{TaskRepository: repo, fault: fault, next: next}
			provider, err := minimax.New("fixture", "", &http.Client{Transport: diagnosticTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.Path != "/v1/image_generation" {
					t.Fatal("unexpected legacy route")
				}
				submissions.Add(1)
				body := `{"base_resp":{"status_code":0},"metadata":{"success_count":2,"failed_count":0},"data":{"image_urls":["https://receipt.fixture/first.png","https://receipt.fixture/second.png"]}}`
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			claim = core.WithProviderConnectionScope(claim, core.ProviderConnectionScope{ID: "minimax", OwnerID: identity.SystemOwnerID, Revision: task.ProviderIdentity})
			first := New(faulty, nil, provider, store, time.Second, time.Minute, 1, time.Second)
			err = first.process(claim, id)
			recoverClaim := fault == "unavailable" || fault == "claim-transfer"
			wantWrites := 2
			if fault == "normal" {
				wantWrites = 1
			}
			if (err != nil) != recoverClaim || faulty.writes != wantWrites || (fault == "claim-transfer" && !errors.Is(err, core.ErrLeaseLost)) {
				t.Fatal("receipt attempts", faulty.writes, err)
			}
			if err := repo.ReleaseTask(claim, id); err != nil {
				t.Fatal(err)
			}
			if recoverClaim {
				fresh := postgres.NewTaskRepository(db)
				if fault == "unavailable" {
					if ok, err := fresh.AcquireTask(next, id, time.Minute); err != nil || !ok {
						t.Fatal("reacquire", ok, err)
					}
				}
				recovery := New(fresh, nil, provider, store, time.Second, time.Minute, 1, time.Second)
				if err := recovery.process(next, id); err != nil {
					t.Fatal(err)
				}
			}
			// A stale Redis delivery after completion must be a no-op, including for
			// an interrupted submission with unknown billing.
			replay := New(postgres.NewTaskRepository(db), nil, provider, store, time.Second, time.Minute, 1, time.Second)
			if err := replay.process(next, id); err != nil {
				t.Fatal(err)
			}
			got, err := repo.Get(ctx, id)
			if err != nil || submissions.Load() != 1 || got.Cost == nil || got.Cost.ActualUSD != nil {
				t.Fatal("submission or billing", err, submissions.Load(), got.Cost)
			}
			if fault == "unavailable" {
				if got.Status != core.TaskFailed || got.Error == nil || *got.Error != core.ErrProviderSubmissionUncertain.Error() || got.Cost.MeteredUSD != nil || got.Usage != nil || fetches.Load() != 0 {
					t.Fatal("lost uncertainty", got.Status, got.Error, got.Cost)
				}
			} else if got.Status != core.TaskSucceeded || len(got.StoredArtifacts) != 2 || fetches.Load() != 2 || got.Cost.MeteredUSD == nil || *got.Cost.MeteredUSD != 7000 {
				t.Fatal("lost or duplicated accepted output or cost", got.Status, fetches.Load(), got.Cost)
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1", id).Scan(&count); err != nil || count != 0 {
				t.Fatal("pending receipts after terminal state", count, err)
			}
			t.Logf("submissions=%d receipt attempts=%d archived=%d status=%s", submissions.Load(), faulty.writes, fetches.Load(), got.Status)
		})
	}
}
