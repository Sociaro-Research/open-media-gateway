// Command webhook-receiver is a development-only endpoint for exercising
// signed webhook delivery and retries through Docker Compose.
package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type receivedEvent struct {
	DeliveryID string          `json:"delivery_id"`
	Event      string          `json:"event"`
	Timestamp  string          `json:"timestamp"`
	Signature  string          `json:"signature"`
	Body       json.RawMessage `json:"body"`
	ReceivedAt time.Time       `json:"received_at"`
}

func main() {
	port := env("RECEIVER_PORT", "8090")
	failFirst, _ := strconv.Atoi(env("FAIL_FIRST", "0"))
	var attempts atomic.Int64
	var mu sync.Mutex
	events := make([]receivedEvent, 0, 16)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /events", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil || !json.Valid(body) {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		event := receivedEvent{
			DeliveryID: r.Header.Get("X-Media-Gateway-Delivery"),
			Event:      r.Header.Get("X-Media-Gateway-Event"),
			Timestamp:  r.Header.Get("X-Media-Gateway-Timestamp"),
			Signature:  r.Header.Get("X-Media-Gateway-Signature"),
			Body:       append(json.RawMessage(nil), body...),
			ReceivedAt: time.Now().UTC(),
		}
		mu.Lock()
		events = append(events, event)
		if len(events) > 100 {
			events = events[len(events)-100:]
		}
		mu.Unlock()
		currentAttempt := attempts.Add(1)
		slog.Info("webhook received", "delivery_id", event.DeliveryID, "event", event.Event, "attempt", currentAttempt)
		if currentAttempt <= int64(failFirst) {
			http.Error(w, "intentional test failure", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		snapshot := append([]receivedEvent(nil), events...)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	slog.Info("test webhook receiver listening", "address", ":"+port, "fail_first", failFirst)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("webhook receiver stopped", "error", err)
		os.Exit(1)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
