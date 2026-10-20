package reliability_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/queue"
	"github.com/Sociaro-Research/media-gateway-dev/internal/webhook"
	"github.com/redis/go-redis/v9"
)

// Fail only Ack, after the worker has committed its PostgreSQL transition.
// The real Redis claim remains available for lease recovery on restart.
type webhookAckQueue struct {
	*queue.RedisQueue
	cancel context.CancelFunc
	mode   string
	acks   int
	err    error
}

func (q *webhookAckQueue) Ack(ctx context.Context, id string) error {
	q.acks++
	switch q.mode {
	case "lost":
		q.err = errors.New("injected lost Redis Ack")
	case "cancel":
		q.cancel()
		q.err = q.RedisQueue.Ack(ctx, id)
	default:
		q.err = q.RedisQueue.Ack(ctx, id)
	}
	q.cancel()
	return q.err
}

func (q *webhookAckQueue) run(t *testing.T, s *testStack, sender webhook.DeliverySender, max int, mode string) {
	t.Helper()
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	q.cancel, q.mode, q.acks, q.err = cancel, mode, 0, nil
	if err := webhook.NewWorker(s.repo, q, sender, max, 3*time.Second).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if q.acks != 1 || (mode == "ack" && q.err != nil) || (mode != "ack" && q.err == nil) {
		t.Fatalf("Ack boundary not exercised: mode=%s acks=%d err=%v", mode, q.acks, q.err)
	}
}

func newWebhookAckQueue(t *testing.T, s *testStack) (*webhookAckQueue, string) {
	t.Helper()
	name := s.queueName + "-webhook-backoff"
	q, err := queue.Open(s.ctx, s.redisURL, name)
	if err != nil {
		t.Fatal(err)
	}
	base := "media-gateway:{" + name + "}:v2:"
	t.Cleanup(func() {
		if err := s.redis.Del(context.Background(), base+"pending", base+"known", base+"owners", base+"leases").Err(); err != nil {
			t.Error(err)
		}
		q.Close()
	})
	return &webhookAckQueue{RedisQueue: q}, base
}

func recoverWebhookClaim(t *testing.T, s *testStack, q *webhookAckQueue, base, id string) {
	t.Helper()
	if n, err := s.redis.ZCard(s.ctx, base+"leases").Result(); err != nil || n != 1 {
		t.Fatalf("lost Ack did not retain lease: n=%d err=%v", n, err)
	}
	// Shorten only this fixture's lease. Recover still executes the real Lua.
	if err := s.redis.ZAdd(s.ctx, base+"leases", redis.Z{Score: 0, Member: id}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := q.Recover(s.ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := s.redis.LLen(s.ctx, base+"pending").Result(); err != nil || n != 1 {
		t.Fatalf("claim was not recovered: pending=%d err=%v", n, err)
	}
}

type webhookRequests struct {
	t        *testing.T
	id, body string
	times    []time.Time
	status   int
}

func (r *webhookRequests) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Helper()
	if req.URL.String() != "https://receiver.example.test/events" {
		return nil, errors.New("unexpected fixture destination")
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte("backoff-fixture-secret"))
	mac.Write([]byte(req.Header.Get("X-Media-Gateway-Timestamp") + "."))
	mac.Write(body)
	if req.Header.Get("X-Media-Gateway-Signature") != "v1="+hex.EncodeToString(mac.Sum(nil)) {
		r.t.Error("invalid HMAC")
	}
	var envelope struct{ ID string }
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.ID != r.id || req.Header.Get("X-Media-Gateway-Delivery") != r.id || req.Header.Get("X-Media-Gateway-Event") != "generation.queued" {
		r.t.Errorf("changed event identity: envelope=%s err=%v", envelope.ID, err)
	}
	if len(r.times) != 0 && string(body) != r.body {
		r.t.Error("retry changed raw body")
	}
	r.body = string(body)
	r.times = append(r.times, time.Now())
	return &http.Response{StatusCode: r.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture")), Request: req}, nil
}

func webhookDelivery(t *testing.T, s *testStack, id string) core.WebhookDelivery {
	t.Helper()
	d, err := s.repo.GetWebhookDelivery(s.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func submitWebhook(t *testing.T, s *testStack) string {
	t.Helper()
	taskID := s.submit("webhook backoff", "backoff", true)
	var id string
	if err := s.db.QueryRowContext(s.ctx, `SELECT id FROM webhook_deliveries WHERE task_id=$1`, taskID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReliabilityWebhookBackoffAfterLostAck(t *testing.T) {
	for _, earlyAck := range []string{"ack", "cancel"} {
		t.Run(earlyAck, func(t *testing.T) {
			s := newStack(t, 30*time.Second)
			q, base := newWebhookAckQueue(t, s)
			id := submitWebhook(t, s)
			requests := &webhookRequests{t: t, id: id, status: 503}
			sender := webhook.NewSender("backoff-fixture-secret", &http.Client{Transport: requests})
			q.run(t, s, sender, 4, "lost")
			retry := webhookDelivery(t, s, id)
			if retry.Status != core.WebhookPending || retry.Attempts != 1 || !retry.NextAttemptAt.After(time.Now()) || len(requests.times) != 1 {
				t.Fatalf("retry not durable: status=%s attempts=%d next=%s sends=%d", retry.Status, retry.Attempts, retry.NextAttemptAt, len(requests.times))
			}
			if due, err := s.repo.ListDueWebhookDeliveries(s.ctx, 100); err != nil || len(due) != 0 {
				t.Fatalf("future retry visible to scheduler: due=%v err=%v", due, err)
			}
			recoverWebhookClaim(t, s, q, base, id)
			requests.status = 204
			q.run(t, s, sender, 4, earlyAck)
			after := webhookDelivery(t, s, id)
			if len(requests.times) != 1 || after.Status != core.WebhookPending || after.Attempts != 1 || !after.NextAttemptAt.Equal(retry.NextAttemptAt) {
				t.Fatalf("early recovery bypassed durable backoff: sends=%d status=%s attempts=%d due_in=%s", len(requests.times), after.Status, after.Attempts, time.Until(retry.NextAttemptAt))
			}
			if earlyAck == "cancel" {
				recoverWebhookClaim(t, s, q, base, id)
				q.run(t, s, sender, 4, "ack")
				if d := webhookDelivery(t, s, id); len(requests.times) != 1 || d.Status != core.WebhookPending || d.Attempts != 1 || !d.NextAttemptAt.Equal(retry.NextAttemptAt) {
					t.Fatal("shutdown in early Ack window lost or sent pending delivery", d)
				}
			}
			if n, err := s.redis.SCard(s.ctx, base+"known").Result(); err != nil || n != 0 {
				t.Fatalf("early delivery was not acknowledged: known=%d err=%v", n, err)
			}
			// Let real time pass the persisted SQL deadline; do not alter the row.
			timer := time.NewTimer(time.Until(retry.NextAttemptAt) + time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-s.ctx.Done():
				t.Fatal(s.ctx.Err())
			}
			q.run(t, s, sender, 4, "ack")
			final := webhookDelivery(t, s, id)
			if final.Status != core.WebhookDelivered || final.Attempts != 2 || len(requests.times) != 2 || requests.times[1].Before(retry.NextAttemptAt) {
				t.Fatalf("scheduler did not resume retry: status=%s attempts=%d sends=%d", final.Status, final.Attempts, len(requests.times))
			}
			t.Logf("lost Ack -> early recovery: sends=1 attempts=1 pending; due retry sent %+v after deadline; final sends=2 attempts=2 delivered", requests.times[1].Sub(retry.NextAttemptAt))
		})
	}
}

func TestReliabilityWebhookTerminalAfterLostAck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   core.WebhookDeliveryStatus
	}{{"success", 204, core.WebhookDelivered}, {"failed", 503, core.WebhookFailed}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, 20*time.Second)
			q, base := newWebhookAckQueue(t, s)
			id := submitWebhook(t, s)
			requests := &webhookRequests{t: t, id: id, status: tc.status}
			sender := webhook.NewSender("backoff-fixture-secret", &http.Client{Transport: requests})
			q.run(t, s, sender, 1, "lost")
			recoverWebhookClaim(t, s, q, base, id)
			q.run(t, s, sender, 1, "ack")
			d := webhookDelivery(t, s, id)
			if d.Status != tc.want || d.Attempts != 1 || len(requests.times) != 1 {
				t.Fatalf("terminal delivery revived: status=%s attempts=%d sends=%d", d.Status, d.Attempts, len(requests.times))
			}
		})
	}
}

func TestReliabilityWebhookCanceledSendRetainsPending(t *testing.T) {
	s := newStack(t, 20*time.Second)
	q, base := newWebhookAckQueue(t, s)
	id := submitWebhook(t, s)
	before := webhookDelivery(t, s, id)
	requests := &webhookRequests{t: t, id: id, status: 204}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := requests.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
		cancel()
		return nil, req.Context().Err()
	})}
	if err := webhook.NewWorker(s.repo, q, webhook.NewSender("backoff-fixture-secret", client), 4, time.Second).Run(ctx); err != nil {
		t.Fatal(err)
	}
	after := webhookDelivery(t, s, id)
	if q.acks != 0 || len(requests.times) != 1 || after.Status != core.WebhookPending || after.Attempts != 0 || !after.NextAttemptAt.Equal(before.NextAttemptAt) {
		t.Fatal("canceled send lost pending state or consumed an attempt", after)
	}
	recoverWebhookClaim(t, s, q, base, id)
	sender := webhook.NewSender("backoff-fixture-secret", &http.Client{Transport: requests})
	q.run(t, s, sender, 4, "ack")
	final := webhookDelivery(t, s, id)
	if final.Status != core.WebhookDelivered || final.Attempts != 1 || len(requests.times) != 2 {
		t.Fatal("canceled send did not resume", final)
	}
}
