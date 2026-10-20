package reliability_test

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/webhook"
)

func TestReliabilityWebhookRetryArithmetic(t *testing.T) {
	for _, tc := range []struct {
		name     string
		base     time.Duration
		attempts int
		limit    int
	}{
		{"negative wrap", math.MaxInt64, 1, math.MaxInt},
		{"zero wrap", 1 << 56, 8, math.MaxInt},
		{"positive wrap", (1 << 56) + 1, 8, math.MaxInt},
		{"large attempt", math.MaxInt64, math.MaxInt32 - 2, math.MaxInt32},
		{"terminal attempt", math.MaxInt64, math.MaxInt32 - 1, math.MaxInt32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, 20*time.Second)
			q, _ := newWebhookAckQueue(t, s)
			id := submitWebhook(t, s)
			if _, err := s.db.ExecContext(s.ctx, `UPDATE webhook_deliveries SET attempts=$2 WHERE id=$1`, id, tc.attempts); err != nil {
				t.Fatal(err)
			}
			requests := &webhookRequests{t: t, id: id, status: 503}
			sender := webhook.NewSender("backoff-fixture-secret", &http.Client{Transport: requests})
			ctx, cancel := context.WithCancel(s.ctx)
			defer cancel()
			q.cancel, q.mode = cancel, "ack"
			before := time.Now()
			if err := webhook.NewWorker(s.repo, q, sender, tc.limit, tc.base).Run(ctx); err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			delivery := webhookDelivery(t, s, id)
			wantStatus := core.WebhookPending
			if tc.attempts+1 >= tc.limit {
				wantStatus = core.WebhookFailed
			}
			// PostgreSQL timestamps have microsecond precision. Bracket the actual
			// worker clock without waiting five minutes or changing its deadline.
			minNext, maxNext := before.Add(5*time.Minute-time.Microsecond), after.Add(5*time.Minute+time.Microsecond)
			if q.acks != 1 || q.err != nil || len(requests.times) != 1 || delivery.Status != wantStatus || delivery.Attempts != tc.attempts+1 || delivery.NextAttemptAt.Before(minNext) || delivery.NextAttemptAt.After(maxNext) {
				t.Fatalf("acks=%d ackError=%v sends=%d status=%s attempts=%d saved delay=%s; want status=%s attempts=%d delay=5m", q.acks, q.err, len(requests.times), delivery.Status, delivery.Attempts, delivery.NextAttemptAt.Sub(after), wantStatus, tc.attempts+1)
			}
			if due, err := s.repo.ListDueWebhookDeliveries(s.ctx, 100); err != nil || len(due) != 0 {
				t.Fatalf("future retry visible to scheduler: due=%v err=%v", due, err)
			}
			t.Logf("one synthetic 503: %s attempts=%d; persisted delay=%s; due IDs=0", delivery.Status, delivery.Attempts, delivery.NextAttemptAt.Sub(after))
		})
	}
}
