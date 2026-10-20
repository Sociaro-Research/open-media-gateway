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

func TestSenderSignsDelivery(t *testing.T) {
	var received *http.Request
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		received = request
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	sender := NewSender("test-secret", client)
	sender.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	delivery := core.WebhookDelivery{
		ID:        "delivery-1",
		EventType: core.EventTaskSucceeded,
		URL:       "https://client.example/events",
		Payload:   []byte(`{"id":"delivery-1"}`),
	}
	if err := sender.Send(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if received.Header.Get("X-Media-Gateway-Delivery") != delivery.ID {
		t.Fatalf("delivery header missing")
	}
	if received.Header.Get("X-Media-Gateway-Timestamp") != "1700000000" {
		t.Fatalf("unexpected timestamp header")
	}
	if received.Header.Get("X-Media-Gateway-Event") != "generation.completed" {
		t.Fatal("wrong event contract")
	}
	want := sender.signature("1700000000", delivery.Payload)
	if received.Header.Get("X-Media-Gateway-Signature") != want {
		t.Fatalf("signature mismatch")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
