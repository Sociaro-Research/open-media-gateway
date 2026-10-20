package webhook

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

var errPacingFixture = errors.New("injected fast infrastructure failure")

// All calls run through the production Run/processClaim loop. Only dependencies
// are fake; synctest controls the real time package, including Run's timers.
// A 1ms successful/failed dequeue models command latency, not empty-queue polling.
type pacingFixture struct {
	deliveryRepositoryStub
	fault                                              string
	faultUntil                                         time.Time
	failed                                             bool
	failureDelivery                                    core.WebhookDelivery
	recoveries, lists, dequeues, requeues, acks, sends int
	starts                                             []time.Time
}

func newPacingFixture(fault string) *pacingFixture {
	return &pacingFixture{fault: fault, deliveryRepositoryStub: deliveryRepositoryStub{delivery: core.WebhookDelivery{
		ID: "pacing-event", Status: core.WebhookPending, Payload: []byte(`{"fixture":true}`),
	}}}
}
func (f *pacingFixture) activeFault() string {
	if !f.faultUntil.IsZero() && !time.Now().Before(f.faultUntil) {
		return ""
	}
	return f.fault
}
func (f *pacingFixture) fail(op string) error {
	if f.activeFault() == op || f.activeFault() == "all" || (f.activeFault() == "get-requeue" && (op == "get" || op == "requeue")) {
		if !f.failed {
			f.failureDelivery = f.delivery
			f.failed = true
		}
		return errPacingFixture
	}
	return nil
}
func (f *pacingFixture) Recover(context.Context) error {
	f.recoveries++
	if f.recoveries == 1 {
		return nil
	} // Preserve the existing fail-fast startup contract.
	return f.fail("recover")
}
func (f *pacingFixture) ListDueWebhookDeliveries(context.Context, int) ([]string, error) {
	f.lists++
	if err := f.fail("list"); err != nil {
		return nil, err
	}
	return []string{f.delivery.ID}, nil
}
func (f *pacingFixture) Enqueue(context.Context, string) error { return f.fail("enqueue") }
func (f *pacingFixture) Dequeue(ctx context.Context) (string, error) {
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	f.dequeues++
	f.starts = append(f.starts, time.Now())
	if err := f.fail("dequeue"); err != nil {
		return "", err
	}
	if f.activeFault() == "empty" {
		return "", core.ErrQueueEmpty
	}
	return f.delivery.ID, nil
}
func (f *pacingFixture) Ack(context.Context, string) error { f.acks++; return f.fail("ack") }
func (f *pacingFixture) Requeue(context.Context, string) error {
	f.requeues++
	return f.fail("requeue")
}
func (f *pacingFixture) Renew(context.Context, string) error { return nil }
func (f *pacingFixture) GetWebhookDelivery(context.Context, string) (core.WebhookDelivery, error) {
	return f.delivery, f.fail("get")
}
func (f *pacingFixture) Send(context.Context, core.WebhookDelivery) error {
	f.sends++
	if f.activeFault() == "retry" || f.activeFault() == "mark-retry" {
		return errors.New("fixture HTTP 503")
	}
	return nil
}
func (f *pacingFixture) MarkWebhookDelivered(ctx context.Context, id string) error {
	if err := f.fail("mark-delivered"); err != nil {
		return err
	}
	if err := f.deliveryRepositoryStub.MarkWebhookDelivered(ctx, id); err != nil {
		return err
	}
	f.delivery.Status = core.WebhookDelivered
	f.delivery.Attempts++
	return nil
}
func (f *pacingFixture) MarkWebhookRetry(ctx context.Context, id string, attempts int, next time.Time, message string, terminal bool) error {
	if err := f.fail("mark-retry"); err != nil {
		return err
	}
	if err := f.deliveryRepositoryStub.MarkWebhookRetry(ctx, id, attempts, next, message, terminal); err != nil {
		return err
	}
	f.delivery.Attempts, f.delivery.NextAttemptAt = attempts, next
	if terminal {
		f.delivery.Status = core.WebhookFailed
	}
	return nil
}

func startPacingWorker(t *testing.T, f *pacingFixture) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewWorker(f, f, f, 4, 3*time.Second).Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, done
}

func TestWebhookWorkerPacesInfrastructureFailures(t *testing.T) {
	for _, fault := range []string{"dequeue", "get", "get-requeue", "mark-delivered", "mark-retry", "recover", "list", "enqueue", "ack", "all"} {
		t.Run(fault, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newPacingFixture(fault)
				cancel, done := startPacingWorker(t, f)
				// Half-millisecond margin avoids a sample exactly on a dependency wakeup.
				time.Sleep(3500*time.Millisecond + 500*time.Microsecond)
				synctest.Wait()
				t.Logf("3.5005s virtual window: recover=%d list=%d dequeue=%d requeue=%d ack=%d send=%d", f.recoveries, f.lists, f.dequeues, f.requeues, f.acks, f.sends)
				if f.dequeues != 4 || f.recoveries != 5 || f.lists != 4 {
					t.Errorf("unpaced infrastructure loop: recover=%d list=%d dequeue=%d; want 5/4/4", f.recoveries, f.lists, f.dequeues)
				}
				for i := 1; i < len(f.starts); i++ {
					if gap := f.starts[i].Sub(f.starts[i-1]); gap < time.Second {
						t.Errorf("retry gap %s < 1s", gap)
						break
					}
				}
				before := f.dequeues
				stoppedAt := time.Now()
				cancel()
				synctest.Wait()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal("cancellation did not interrupt error pause")
				}
				if time.Now() != stoppedAt || f.dequeues != before {
					t.Fatal("shutdown advanced time or retried dependency")
				}
			})
		})
	}
}

func TestWebhookWorkerResumesAfterInfrastructureRecovery(t *testing.T) {
	for _, fault := range []string{"dequeue", "get", "mark-delivered", "mark-retry", "recover", "list", "enqueue", "ack"} {
		t.Run(fault, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newPacingFixture(fault)
				f.faultUntil = time.Now().Add(100 * time.Millisecond)
				cancel, done := startPacingWorker(t, f)
				time.Sleep(1100 * time.Millisecond)
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				before := f.failureDelivery
				if !f.failed || before.Status != core.WebhookPending || before.Attempts != 0 || !before.NextAttemptAt.IsZero() {
					// Ack fails after a successful durable transition.
					if fault != "ack" || before.Status != core.WebhookDelivered {
						t.Fatal("unexpected state at injected failure", before)
					}
				}
				if !f.delivered || f.delivery.Status != core.WebhookDelivered || f.delivery.Attempts != 1 || !f.delivery.NextAttemptAt.IsZero() {
					t.Fatal("delivery did not resume without consuming a retry", f.delivery)
				}
				wantSends := 1
				if fault == "mark-delivered" || fault == "mark-retry" {
					wantSends = 2
				} // At least once: an unsaved HTTP result may be sent again.
				if f.sends != wantSends {
					t.Fatalf("sends=%d want %d", f.sends, wantSends)
				}
			})
		})
	}
}

func TestWebhookWorkerHealthyAndDurableRetryAreNotPaced(t *testing.T) {
	for _, fault := range []string{"", "empty", "retry"} {
		t.Run("mode="+fault, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newPacingFixture(fault)
				cancel, done := startPacingWorker(t, f)
				time.Sleep(20*time.Millisecond + 500*time.Microsecond)
				synctest.Wait()
				if f.dequeues != 20 {
					t.Errorf("healthy/empty/persisted retry loop was delayed: %d dequeues", f.dequeues)
				}
				if fault == "retry" && (f.delivery.Attempts != 1 || f.sends != 1 || !f.delivery.NextAttemptAt.Equal(f.starts[0].Add(3*time.Second))) {
					t.Fatal("durable retry contract changed", f.delivery, f.sends)
				}
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
