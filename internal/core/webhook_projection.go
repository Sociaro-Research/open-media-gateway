package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrStoredWebhookPayload = errors.New("Stored webhook payload is invalid or unsupported. Ask an operator to inspect this delivery; do not resubmit its generation to repair webhook delivery.")

// PublicWebhookPayload projects the documented historical envelope before HMAC.
// It never changes the saved payload. User prompt/options/snapshots are untouched.
func PublicWebhookPayload(raw []byte) ([]byte, error) {
	var event map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&event) != nil || event == nil || d.Decode(&struct{}{}) != io.EOF {
		return nil, ErrStoredWebhookPayload
	}
	if event["object"] != "event" {
		return nil, ErrStoredWebhookPayload
	}
	typ, _ := event["type"].(string)
	switch typ {
	case "generation.queued", "generation.in_progress", "generation.completed", "generation.failed":
	default:
		return nil, ErrStoredWebhookPayload
	}
	data, ok := event["data"].(map[string]any)
	if !ok {
		return nil, ErrStoredWebhookPayload
	}
	if data["object"] != "generation" && data["object"] != "video" {
		return nil, ErrStoredWebhookPayload
	}
	ProjectGenerationRecord(data)
	return CustomerJSON(event)
}
