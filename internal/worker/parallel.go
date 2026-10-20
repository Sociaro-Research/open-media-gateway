package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/google/uuid"
)

// Synchronous provider calls can take ten minutes, followed by result transfers.
// This bounds the current operations, not the lifetime of pending provider jobs.
const DefaultShutdownTimeout = 25 * time.Minute

var ErrShutdownTimeout = errors.New("worker shutdown deadline exceeded")

func (w *Worker) Run(ctx context.Context) error {
	repo, ok := w.repo.(core.TaskWorkerRepository)
	if !ok {
		return errors.New("concurrent workers require a task claim repository")
	}
	queue, ok := w.queue.(core.LeasedQueue)
	if !ok {
		return errors.New("concurrent workers require a leased queue")
	}
	if w.dispatchInterval <= 0 {
		return errors.New("dispatch interval must be positive")
	}
	if w.OperationConcurrency < 1 || w.OperationConcurrency > 256 {
		return errors.New("operation concurrency must be between 1 and 256")
	}
	if w.ArchiveConcurrency < 1 || w.ArchiveConcurrency > 256 {
		return errors.New("archive concurrency must be between 1 and 256")
	}
	if w.ShutdownTimeout <= 0 {
		return errors.New("worker shutdown timeout must be positive")
	}
	w.archiveSlots = make(chan struct{}, w.ArchiveConcurrency)
	// Stop dequeue/reconciliation immediately, but keep acquired operations and
	// lease heartbeats alive until their provider responses and state are durable.
	work, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()
	drained, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-drained:
			return
		case <-ctx.Done():
		}
		slog.Info("worker draining active operations", "timeout", w.ShutdownTimeout)
		timer := time.NewTimer(w.ShutdownTimeout)
		defer timer.Stop()
		select {
		case <-drained:
		case <-timer.C:
			cancelWork()
		}
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); w.dispatch(ctx, repo, queue) }()
	// Bound current operations, including claims and cleanup. Pending provider
	// tasks are persisted and release their slots between polls.
	err := w.consume(ctx, work, repo, queue)
	wg.Wait()
	close(drained)
	<-watcherDone
	if work.Err() != nil {
		return errors.Join(ErrShutdownTimeout, err)
	}
	return err
}

func (w *Worker) dispatch(ctx context.Context, repo core.TaskWorkerRepository, queue core.LeasedQueue) {
	cursor := ""
	for ctx.Err() == nil {
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		recoverErr := queue.Recover(check)
		if recoverErr != nil && ctx.Err() == nil {
			slog.Warn("recover expired queue leases", "error", recoverErr)
		}
		const batchSize = 1000
		ids, err := repo.ListRecoverableBatch(check, cursor, batchSize)
		if err == nil {
			if batch, ok := queue.(interface {
				EnqueueBatch(context.Context, []string) error
			}); ok {
				err = batch.EnqueueBatch(check, ids)
			} else {
				for _, id := range ids {
					if err = queue.Enqueue(check, id); err != nil {
						break
					}
				}
			}
			if err == nil {
				err = repo.MarkEnqueuedBatch(check, ids)
			}
			if err == nil && len(ids) > 0 {
				cursor = ids[len(ids)-1]
			}
			if len(ids) < batchSize {
				cursor = ""
			}
		}
		cancel()
		if w.Observer != nil && ctx.Err() == nil {
			w.Observer.ObserveWorkerLoop("dispatcher", errors.Join(recoverErr, err))
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("reconcile task queue", "error", err)
		}
		if err == nil && len(ids) == batchSize {
			continue
		} // Drain full batches without a per-batch sleep.
		if !pause(ctx, w.dispatchInterval) {
			return
		}
	}
}
func (w *Worker) consume(ctx, work context.Context, repo core.TaskWorkerRepository, queue core.LeasedQueue) error {
	var active sync.WaitGroup
	var mu sync.Mutex
	var drainErr error
	slots := make(chan struct{}, w.OperationConcurrency)
dequeueLoop:
	for ctx.Err() == nil {
		// Reserve capacity before acquiring a delivery lease or spawning work.
		// Waiting claims must stay in the queue, without goroutines or heartbeats.
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break dequeueLoop
		}
		if ctx.Err() != nil {
			<-slots
			break
		}
		token := uuid.NewString()
		id, err := queue.Dequeue(core.WithClaimToken(ctx, token))
		if w.Observer != nil && ctx.Err() == nil {
			observed := err
			if errors.Is(err, core.ErrQueueEmpty) {
				observed = nil
			}
			w.Observer.ObserveWorkerLoop("consumer", observed)
		}
		if errors.Is(err, core.ErrQueueEmpty) {
			<-slots
			continue
		}
		if err != nil {
			<-slots
			if ctx.Err() != nil {
				break
			}
			slog.Warn("dequeue task", "error", err)
			if !pause(ctx, time.Second) {
				break
			}
			continue
		}
		claimCtx := core.WithClaimToken(work, token)
		if ctx.Err() != nil {
			cleanup, cancel := context.WithTimeout(claimCtx, 5*time.Second)
			err = queue.Requeue(cleanup, id)
			cancel()
			<-slots
			active.Wait()
			return errors.Join(drainErr, err)
		}
		active.Add(1)
		go func() {
			defer active.Done()
			defer func() { <-slots }()
			var observed func(error)
			if w.Observer != nil {
				observed = w.Observer.WorkerClaimStarted()
			}
			err := w.runClaim(claimCtx, id, repo, queue)
			if observed != nil {
				observed(err)
			}
			if err != nil {
				slog.Warn("task claim", "task_id", id, "error", err)
				if ctx.Err() != nil {
					mu.Lock()
					drainErr = errors.Join(drainErr, err)
					mu.Unlock()
				}
			}
		}()
	}
	active.Wait()
	return drainErr
}
func (w *Worker) runClaim(ctx context.Context, id string, repo core.TaskWorkerRepository, queue core.LeasedQueue) error {
	admission, cancelAdmission := context.WithTimeout(ctx, 5*time.Second)
	acquired, err := repo.AcquireTask(admission, id, core.WorkLeaseDuration)
	cancelAdmission()
	if err != nil {
		return err
	} // Queue lease expiry provides recovery even if Redis is down.
	if !acquired {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return queue.Ack(cleanup, id) // A live DB claim or terminal task; PostgreSQL remains authoritative.
	}
	taskCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(core.WorkLeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-taskCtx.Done():
				return
			case <-ticker.C:
				heartbeat, stop := context.WithTimeout(taskCtx, 5*time.Second)
				err := queue.Renew(heartbeat, id)
				if err == nil {
					err = repo.RenewTask(heartbeat, id, core.WorkLeaseDuration)
				}
				stop()
				if err != nil {
					slog.Warn("task lease lost", "task_id", id, "error", err)
					cancel()
					return
				}
			}
		}
	}()
	processErr := w.process(taskCtx, id)
	cancel()
	<-heartbeatDone
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	var pending *pollLater
	if errors.As(processErr, &pending) {
		// Persist the next poll before acknowledgment. Crashes at either side are
		// recovered through the durable due timestamp and fenced claims.
		processErr = repo.ScheduleTask(cleanup, id, w.pollInterval, pending.errors)
	}
	var queueErr error
	if processErr == nil {
		queueErr = queue.Ack(cleanup, id)
	} else {
		queueErr = queue.Requeue(cleanup, id)
	}
	releaseErr := repo.ReleaseTask(cleanup, id)
	if queueErr != nil {
		return fmt.Errorf("finish queue claim: %w", queueErr)
	}
	if releaseErr != nil {
		return releaseErr
	}
	// Briefly back off transient storage/database failures rather than busy-looping.
	if processErr != nil {
		pause(ctx, 250*time.Millisecond)
	}
	return processErr
}
func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
