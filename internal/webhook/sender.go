package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/webhookurl"
)

type Sender struct {
	secret []byte
	client *http.Client
	now    func() time.Time
}

func NewSender(secret string, client *http.Client) *Sender {
	return &Sender{secret: []byte(secret), client: client, now: time.Now}
}

func (s *Sender) Send(ctx context.Context, delivery core.WebhookDelivery) error {
	if err := webhookurl.Validate(delivery.URL); err != nil {
		return err
	}
	timestamp := strconv.FormatInt(s.now().Unix(), 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.URL, bytes.NewReader(delivery.Payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "media-gateway-webhooks/0.2")
	request.Header.Set("X-Media-Gateway-Delivery", delivery.ID)
	request.Header.Set("X-Media-Gateway-Event", core.GenerationEventType(delivery.EventType))
	request.Header.Set("X-Media-Gateway-Timestamp", timestamp)
	request.Header.Set("X-Media-Gateway-Signature", s.signature(timestamp, delivery.Payload))

	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("webhook returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	return nil
}

func (s *Sender) signature(timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(payload)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}
