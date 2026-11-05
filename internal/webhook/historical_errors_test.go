package webhook

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type historicalTransport func(*http.Request) (*http.Response, error)

func (f historicalTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHistoricalWebhookIsProjectedBeforeSigningWithoutRewriting(t *testing.T) {
	const saved = `{"object":"event","type":"generation.failed","data":{"object":"generation","error":{"code":"generation_failed","message":"private-body /private/path","extra":"private-token"},"options":{"parameters":{"object":"generation","error":"user-sentinel"}}}}`
	repo := &deliveryRepositoryStub{delivery: core.WebhookDelivery{ID: "historical-delivery", Status: core.WebhookPending, EventType: core.EventTaskFailed, URL: "https://example.com/hook", Payload: []byte(saved)}}
	calls := 0
	sender := NewSender("synthetic-signing-key", nil)
	sender.now = func() time.Time { return time.Unix(100, 0) }
	sender.client = &http.Client{Transport: historicalTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		data, err := io.ReadAll(r.Body)
		if err != nil || strings.Contains(string(data), "private-body") || strings.Contains(string(data), "private-token") || !strings.Contains(string(data), "user-sentinel") {
			t.Fatal("unsafe projected delivery", string(data), err)
		}
		if r.Header.Get("X-Media-Gateway-Signature") != sender.signature("100", data) || r.Header.Get("X-Media-Gateway-Signature") == sender.signature("100", []byte(saved)) {
			t.Fatal("HMAC did not cover projected bytes")
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	worker := NewWorker(repo, nil, sender, 3, time.Second)
	if err := worker.process(context.Background(), repo.delivery.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !repo.delivered || string(repo.delivery.Payload) != saved {
		t.Fatal("delivery changed persisted payload or delivery behavior")
	}
}

func TestUnsupportedHistoricalWebhookFailsClosedWithBoundedAttempts(t *testing.T) {
	for _, raw := range []string{`{}`, `{"object":"event","type":"other","data":{}}`, string(fixtureWebhookPayload()) + ` {}`} {
		repo := &deliveryRepositoryStub{delivery: core.WebhookDelivery{ID: "invalid-historical", Status: core.WebhookPending, Attempts: 1, Payload: []byte(raw)}}
		sender := &countingSender{}
		worker := NewWorker(repo, nil, sender, 2, time.Second)
		if err := worker.process(context.Background(), repo.delivery.ID); err != nil {
			t.Fatal(err)
		}
		if sender.calls != 0 || repo.attempts != 2 || !repo.terminal || string(repo.delivery.Payload) != raw {
			t.Fatal("invalid historical envelope escaped or retried without bounds")
		}
	}
}
