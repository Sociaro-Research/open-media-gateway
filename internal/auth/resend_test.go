package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResendPayloadAndRetries(t *testing.T) {
	sender, err := NewResend("fixture-key", "Example Service <login@example.test>")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var bodies []string
	sender.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		count++
		if r.URL.String() != "https://api.resend.com/emails" || r.Header.Get("Authorization") != "Bearer fixture-key" || r.Header.Get("Idempotency-Key") != "fixture-id" {
			t.Fatal("incorrect Resend request")
		}
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		var v map[string]any
		if json.Unmarshal(body, &v) != nil {
			t.Fatal("invalid JSON")
		}
		if v["from"] != "Example Service <login@example.test>" || !strings.Contains(v["text"].(string), "#email_verify=") {
			t.Fatal("missing sender or verification link")
		}
		attachments, ok := v["attachments"].([]any)
		if !ok || len(attachments) != 0 || strings.Contains(v["html"].(string), "cid:") {
			t.Fatal("unconfigured email must not attach a logo")
		}
		code := 200
		if count == 1 {
			code = 500
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(`{"id":"fixture"}`))}, nil
	})
	if err = sender.Send(context.Background(), "user@example.test", "verify", "https://example.test/#email_verify=fixture", "fixture-id"); err != nil {
		t.Fatal(err)
	}
	if count != 2 || bodies[0] != bodies[1] {
		t.Fatal("retry was not idempotent")
	}
	if _, err = NewResend("", "bad sender"); err == nil {
		t.Fatal("accepted missing credentials")
	}
}

func TestResendPaymentNotification(t *testing.T) {
	sender, err := NewResend("fixture-key", "Example Service <alerts@example.test>")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	sender.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Idempotency-Key") != "payment-notice-fixture" {
			t.Fatal("missing stable delivery key")
		}
		var payload struct {
			To      []string `json:"to"`
			Subject string   `json:"subject"`
			Text    string   `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.To) != 1 || payload.To[0] != "admin@example.test" || payload.Subject != "Payment credited" || payload.Text != "USD 4.54 credited" {
			t.Fatal(payload)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"fixture"}`))}, nil
	})
	if err = sender.SendNotification(context.Background(), "admin@example.test", "Payment credited", "USD 4.54 credited", "payment-notice-fixture"); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
}
