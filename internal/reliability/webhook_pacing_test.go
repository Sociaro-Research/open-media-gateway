package reliability_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/webhook"
)

// Inject one fast repository error; all other reads, durable transitions and
// queue operations use the isolated PostgreSQL/Redis stack.
type unavailableWebhookRepository struct {
	core.WebhookRepository
	operation string
	failed    bool
}

func (r *unavailableWebhookRepository) fail(op string) bool {
	if !r.failed && r.operation == op {
		r.failed = true
		return true
	}
	return false
}
func (r *unavailableWebhookRepository) GetWebhookDelivery(ctx context.Context, id string) (core.WebhookDelivery, error) {
	if r.fail("get") {
		return core.WebhookDelivery{}, errors.New("injected PostgreSQL read failure")
	}
	return r.WebhookRepository.GetWebhookDelivery(ctx, id)
}
func (r *unavailableWebhookRepository) MarkWebhookDelivered(ctx context.Context, id string) error {
	if r.fail("delivered") {
		return errors.New("injected PostgreSQL completion failure")
	}
	return r.WebhookRepository.MarkWebhookDelivered(ctx, id)
}
func (r *unavailableWebhookRepository) MarkWebhookRetry(ctx context.Context, id string, attempts int, next time.Time, message string, terminal bool) error {
	if r.fail("retry") {
		return errors.New("injected PostgreSQL retry failure")
	}
	return r.WebhookRepository.MarkWebhookRetry(ctx, id, attempts, next, message, terminal)
}

type pacingRecoveryQueue struct {
	*webhookAckQueue
	t        *testing.T
	s        *testStack
	before   core.WebhookDelivery
	requeues int
}

func (q *pacingRecoveryQueue) Requeue(ctx context.Context, id string) error {
	q.requeues++
	d := webhookDelivery(q.t, q.s, id)
	if d.Status != q.before.Status || d.Attempts != q.before.Attempts || !d.NextAttemptAt.Equal(q.before.NextAttemptAt) {
		q.t.Error("infrastructure error changed durable attempts/deadline", d)
	}
	if core.ClaimToken(ctx) == "" {
		q.t.Error("requeue lost claim token")
	}
	return q.RedisQueue.Requeue(ctx, id)
}

func TestReliabilityWebhookResumesAfterInfrastructureFailure(t *testing.T) {
	for _, op := range []string{"get", "delivered", "retry"} {
		t.Run(op, func(t *testing.T) {
			s := newStack(t, 15*time.Second)
			aq, base := newWebhookAckQueue(t, s)
			id := submitWebhook(t, s)
			before := webhookDelivery(t, s, id)
			q := &pacingRecoveryQueue{webhookAckQueue: aq, t: t, s: s, before: before}
			repo := &unavailableWebhookRepository{WebhookRepository: s.repo, operation: op}
			requests := &webhookRequests{t: t, id: id, status: 204}
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests.status = 204
				if op == "retry" && len(requests.times) == 0 {
					requests.status = 503
				}
				return requests.RoundTrip(req)
			})
			sender := webhook.NewSender("backoff-fixture-secret", &http.Client{Transport: transport})
			ctx, cancel := context.WithCancel(s.ctx)
			defer cancel()
			aq.cancel, aq.mode = cancel, "ack"
			if err := webhook.NewWorker(repo, q, sender, 4, 3*time.Second).Run(ctx); err != nil {
				t.Fatal(err)
			}
			final := webhookDelivery(t, s, id)
			wantSends := 2
			if op == "get" {
				wantSends = 1
			}
			if !repo.failed || q.requeues != 1 || aq.acks != 1 || aq.err != nil || len(requests.times) != wantSends || final.Status != core.WebhookDelivered || final.Attempts != 1 || !final.NextAttemptAt.Equal(before.NextAttemptAt) {
				t.Fatalf("recovery: injected=%t requeues=%d acks=%d ackErr=%v sends=%d final=%+v", repo.failed, q.requeues, aq.acks, aq.err, len(requests.times), final)
			}
			for _, key := range []string{"pending", "known", "owners", "leases"} {
				if n, err := s.redis.Exists(s.ctx, base+key).Result(); err != nil || n != 0 {
					t.Fatalf("queue retained %s: n=%d err=%v", key, n, err)
				}
			}
			t.Logf("one infrastructure error/requeue -> %d signed HTTP requests, delivered/attempts=1, unchanged deadline, empty Redis coordination", wantSends)
		})
	}
}
