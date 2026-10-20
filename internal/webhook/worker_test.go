package webhook

import (
	"context"
	"errors"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type deliveryRepositoryStub struct {
	delivery    core.WebhookDelivery
	delivered   bool
	attempts    int
	nextAttempt time.Time
	terminal    bool
}

func (r *deliveryRepositoryStub) ListDueWebhookDeliveries(context.Context, int) ([]string, error) {
	return []string{r.delivery.ID}, nil
}
func (r *deliveryRepositoryStub) GetWebhookDelivery(context.Context, string) (core.WebhookDelivery, error) {
	return r.delivery, nil
}
func (r *deliveryRepositoryStub) MarkWebhookDelivered(context.Context, string) error {
	r.delivered = true
	return nil
}
func (r *deliveryRepositoryStub) MarkWebhookRetry(_ context.Context, _ string, attempts int, next time.Time, _ string, terminal bool) error {
	r.attempts, r.nextAttempt, r.terminal = attempts, next, terminal
	return nil
}

type senderStub struct{ err error }

func (s senderStub) Send(context.Context, core.WebhookDelivery) error { return s.err }

type countingSender struct{ calls int }

func (s *countingSender) Send(context.Context, core.WebhookDelivery) error {
	s.calls++
	return nil
}

func TestWebhookWorkerRespectsDeliveryDeadline(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		name string
		due  time.Time
		want int
	}{
		{"unset", time.Time{}, 1},
		{"past", now.Add(-time.Nanosecond), 1},
		{"due now", now, 1},
		{"future", now.Add(time.Nanosecond), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &deliveryRepositoryStub{delivery: core.WebhookDelivery{ID: "delivery-1", Status: core.WebhookPending, NextAttemptAt: tc.due}}
			sender := &countingSender{}
			worker := NewWorker(repo, nil, sender, 3, time.Second)
			worker.now = func() time.Time { return now }
			if err := worker.process(context.Background(), repo.delivery.ID); err != nil {
				t.Fatal(err)
			}
			if sender.calls != tc.want || repo.delivered != (tc.want == 1) || repo.attempts != 0 {
				t.Fatalf("deadline=%s calls=%d delivered=%t retries=%d", tc.due, sender.calls, repo.delivered, repo.attempts)
			}
		})
	}
}

func TestWebhookWorkerMarksSuccessfulDelivery(t *testing.T) {
	repo := &deliveryRepositoryStub{delivery: core.WebhookDelivery{ID: "delivery-1", Status: core.WebhookPending}}
	worker := NewWorker(repo, nil, senderStub{}, 3, time.Second)
	if err := worker.process(context.Background(), repo.delivery.ID); err != nil {
		t.Fatal(err)
	}
	if !repo.delivered {
		t.Fatal("delivery was not marked delivered")
	}
}

func TestWebhookWorkerSchedulesRetryAndStopsAtLimit(t *testing.T) {
	repo := &deliveryRepositoryStub{delivery: core.WebhookDelivery{ID: "delivery-1", Status: core.WebhookPending, Attempts: 1}}
	worker := NewWorker(repo, nil, senderStub{err: errors.New("temporary failure")}, 2, time.Second)
	worker.now = func() time.Time { return time.Unix(100, 0) }
	if err := worker.process(context.Background(), repo.delivery.ID); err != nil {
		t.Fatal(err)
	}
	if repo.attempts != 2 || !repo.terminal {
		t.Fatalf("unexpected retry state: attempts=%d terminal=%v", repo.attempts, repo.terminal)
	}
}

func TestWebhookWorkerBackoffBoundaries(t *testing.T) {
	const capDelay = 5 * time.Minute
	bases := []time.Duration{time.Nanosecond, time.Second, time.Hour, math.MaxInt64, 1 << 56, (1 << 56) + 1}
	for factor := time.Duration(1); factor <= 256; factor *= 2 {
		for _, boundary := range []time.Duration{capDelay / factor, math.MaxInt64 / factor} {
			for _, offset := range []time.Duration{-1, 0, 1} {
				if boundary+offset > 0 {
					bases = append(bases, boundary+offset)
				}
			}
		}
	}
	attempts := []int{math.MinInt, -1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 63, 64, math.MaxInt32, math.MaxInt}
	failures := 0
	for _, base := range bases {
		worker := NewWorker(nil, nil, nil, math.MaxInt, base)
		for _, attempt := range attempts {
			// Arbitrary-precision arithmetic is independent of duration overflow.
			want := big.NewInt(int64(base))
			for i := 1; i < attempt && i < 9; i++ {
				want.Mul(want, big.NewInt(2))
			}
			if want.Cmp(big.NewInt(int64(capDelay))) > 0 {
				want.SetInt64(int64(capDelay))
			}
			got := worker.backoff(attempt)
			if got != time.Duration(want.Int64()) || got <= 0 || got > capDelay {
				if failures < 8 {
					t.Errorf("base=%dns attempt=%d delay=%s want=%s", base, attempt, got, time.Duration(want.Int64()))
				}
				failures++
			}
		}
	}
	t.Logf("%d boundary combinations, %d mismatches", len(bases)*len(attempts), failures)
}

func TestWebhookWorkerRetryDeadlineArithmetic(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		base     time.Duration
		attempts int
		limit    int
		want     time.Duration
	}{
		{"negative wrap", math.MaxInt64, 1, math.MaxInt, 5 * time.Minute},
		{"zero wrap", 1 << 56, 8, math.MaxInt, 5 * time.Minute},
		{"positive wrap", (1 << 56) + 1, 8, math.MaxInt, 5 * time.Minute},
		{"large attempt", math.MaxInt64, math.MaxInt32 - 1, math.MaxInt32, 5 * time.Minute},
		{"default plateau", time.Second, 63, 65, 256 * time.Second},
		{"tiny plateau", time.Nanosecond, 63, 65, 256 * time.Nanosecond},
		{"terminal", time.Second, 1, 2, 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &deliveryRepositoryStub{delivery: core.WebhookDelivery{ID: "delivery-1", Status: core.WebhookPending, Payload: []byte(`{}`), Attempts: tc.attempts}}
			worker := NewWorker(repo, nil, senderStub{err: errors.New("fixture 503")}, tc.limit, tc.base)
			worker.now = func() time.Time { return now }
			if err := worker.process(context.Background(), repo.delivery.ID); err != nil {
				t.Fatal(err)
			}
			if repo.attempts != tc.attempts+1 || repo.terminal != (tc.attempts+1 >= tc.limit) || !repo.nextAttempt.Equal(now.Add(tc.want)) {
				t.Fatalf("attempts=%d terminal=%t delay=%s; want attempts=%d terminal=%t delay=%s", repo.attempts, repo.terminal, repo.nextAttempt.Sub(now), tc.attempts+1, tc.attempts+1 >= tc.limit, tc.want)
			}
		})
	}
}
