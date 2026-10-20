package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type DeliverySender interface {
	Send(context.Context, core.WebhookDelivery) error
}

type Worker struct {
	repo        core.WebhookRepository
	queue       core.TaskQueue
	sender      DeliverySender
	maxAttempts int
	retryBase   time.Duration
	now         func() time.Time
}

func NewWorker(repo core.WebhookRepository, queue core.TaskQueue, sender DeliverySender, maxAttempts int, retryBase time.Duration) *Worker {
	return &Worker{repo: repo, queue: queue, sender: sender, maxAttempts: maxAttempts, retryBase: retryBase, now: time.Now}
}

func (w *Worker) Run(ctx context.Context) error {
	leased, ok := w.queue.(core.LeasedQueue)
	if !ok {
		return errors.New("webhook worker requires a leased queue")
	}
	if err := w.queue.Recover(ctx); err != nil {
		return err
	}
	for retry := false; ctx.Err() == nil; {
		// Pace infrastructure failures independently of the durable HTTP retry
		// schedule. Fast command errors do not receive Dequeue's empty-queue wait.
		if retry {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
		retry = false
		if err := w.queue.Recover(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("recover expired webhook leases", "error", err)
			retry = true
		}
		if err := w.enqueueDue(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("enqueue due webhooks", "error", err)
			retry = true
		}
		claimCtx := core.WithClaimToken(ctx, uuid.NewString())
		id, err := w.queue.Dequeue(claimCtx)
		if errors.Is(err, core.ErrQueueEmpty) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("dequeue webhook", "error", err)
			retry = true
			continue
		}
		if err := w.processClaim(claimCtx, id, leased); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("process webhook", "delivery_id", id, "error", err)
			retry = true
			if retryErr := w.queue.Requeue(context.WithoutCancel(claimCtx), id); retryErr != nil {
				slog.Error("requeue webhook", "delivery_id", id, "error", retryErr)
			}
			continue
		}
		if err := w.queue.Ack(claimCtx, id); err != nil && ctx.Err() == nil {
			slog.Error("ack webhook", "delivery_id", id, "error", err)
			retry = true
		}
	}
	return nil
}

func (w *Worker) enqueueDue(ctx context.Context) error {
	ids, err := w.repo.ListDueWebhookDeliveries(ctx, 100)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := w.queue.Enqueue(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) process(ctx context.Context, id string) error {
	delivery, err := w.repo.GetWebhookDelivery(ctx, id)
	if errors.Is(err, core.ErrWebhookDeliveryNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if delivery.Status != core.WebhookPending {
		return nil
	}
	// Redis may recover a stale claim after a retry was saved but Ack failed.
	// Acknowledge that claim; PostgreSQL will schedule it again when due.
	if delivery.NextAttemptAt.After(w.now()) {
		return nil
	}
	// Sanitize at delivery time, including pending payloads from older releases.
	delivery.Payload, err = core.CustomerJSON(json.RawMessage(delivery.Payload))
	if err != nil {
		return err
	}
	if err := w.sender.Send(ctx, delivery); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempts := delivery.Attempts + 1
		terminal := attempts >= w.maxAttempts
		nextAttempt := w.now().Add(w.backoff(attempts))
		return w.repo.MarkWebhookRetry(ctx, id, attempts, nextAttempt, err.Error(), terminal)
	}
	return w.repo.MarkWebhookDelivered(ctx, id)
}

func (w *Worker) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 {
		attempt = 9
	}
	const maxDelay = 5 * time.Minute
	factor := time.Duration(1 << (attempt - 1))
	// Compare before multiplying so large valid bases cannot overflow Duration.
	if w.retryBase > maxDelay/factor {
		return maxDelay
	}
	return w.retryBase * factor
}

func (w *Worker) processClaim(ctx context.Context, id string, queue core.LeasedQueue) error {
	work, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(core.WorkLeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				check, stop := context.WithTimeout(work, 5*time.Second)
				err := queue.Renew(check, id)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	err := w.process(work, id)
	cancel()
	<-done
	return err
}
