package core

import (
	"context"
	"errors"
	"time"
)

const WorkLeaseDuration = 60 * time.Second

var ErrQueueEmpty = errors.New("queue is empty")
var ErrLeaseLost = errors.New("work lease expired or belongs to another worker")

type claimTokenKey struct{}

func WithClaimToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, claimTokenKey{}, token)
}
func ClaimToken(ctx context.Context) string {
	token, _ := ctx.Value(claimTokenKey{}).(string)
	return token
}

type LeasedQueue interface {
	TaskQueue
	Renew(context.Context, string) error
}

// Required by the concurrent worker: Redis delivery alone is not authority to
// change a task. Database claims fence stale workers after lease expiry.
type TaskWorkerRepository interface {
	TaskRepository
	AcquireTask(context.Context, string, time.Duration) (bool, error)
	RenewTask(context.Context, string, time.Duration) error
	ReleaseTask(context.Context, string) error
	ScheduleTask(context.Context, string, time.Duration, int) error
	MarkEnqueuedBatch(context.Context, []string) error
	ListRecoverableBatch(context.Context, string, int) ([]string, error)
}
