package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/branding"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

// EmailSender is injected in tests. Production uses Resend only when explicitly configured.
type EmailSender interface {
	Send(context.Context, string, string, string, string) error
}

type Resend struct {
	key, from string
	client    *http.Client
	brand     *branding.Settings
}

func NewResend(key, from string) (*Resend, error) {
	address, err := mail.ParseAddress(from)
	if strings.TrimSpace(key) == "" || err != nil || address.Address == "" || strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("RESEND_API_KEY and EMAIL_FROM must specify a Resend key and valid sender")
	}
	// Resend accepts an unnamed sender as a bare address, not <address>.
	if address.Name == "" {
		from = address.Address
	}
	return &Resend{key: key, from: from, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *Resend) Send(ctx context.Context, to, purpose, link, idempotencyKey string) error {
	settings := branding.Defaults()
	if s.brand != nil {
		settings = *s.brand
	}
	message, err := renderBrandedAccountEmail(to, purpose, link, settings)
	if err != nil {
		return err
	}
	logo, _ := accountBrandLogo(settings)
	attachments := []map[string]string{}
	if message.HasLogo {
		attachments = append(attachments, map[string]string{"filename": "brand-logo", "content_type": logo.MIME, "content_id": accountEmailLogoCID, "content": base64.StdEncoding.EncodeToString(logo.Data)})
	}
	from := s.from
	if name := strings.TrimSpace(settings.Name); s.brand != nil && name != "" {
		address, _ := mail.ParseAddress(from)
		address.Name = name
		from = address.String()
	}
	body, err := json.Marshal(map[string]any{
		"from": from, "to": []string{to}, "subject": message.Subject, "text": message.Text, "html": message.HTML,
		"attachments": attachments,
	})
	if err != nil {
		return err
	}
	return s.sendPayload(ctx, body, idempotencyKey)
}

// SendNotification shares the configured transport without placing payment
// details into authentication templates or including any card/credential data.
func (s *Resend) SendNotification(ctx context.Context, to, subject, text, idempotencyKey string) error {
	body, err := json.Marshal(map[string]any{"from": s.from, "to": []string{to}, "subject": subject, "text": text})
	if err != nil {
		return err
	}
	return s.sendPayload(ctx, body, idempotencyKey)
}
func (s *Resend) SendCatalogReport(ctx context.Context, to, subject, text, markup, key string) error {
	body, err := json.Marshal(map[string]any{"from": s.from, "to": []string{to}, "subject": subject, "text": text, "html": markup})
	if err != nil {
		return err
	}
	return s.sendPayload(ctx, body, key)
}

func (s *Resend) sendPayload(ctx context.Context, body []byte, idempotencyKey string) error {
	// Retry transient failures with the same payload and Resend idempotency key.
	// Neither provider response bodies nor tokens are included in errors or logs.
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+s.key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)
		res, sendErr := s.client.Do(req)
		if sendErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
			res.Body.Close()
			if res.StatusCode >= 200 && res.StatusCode < 300 {
				return nil
			}
			if res.StatusCode != 429 && res.StatusCode < 500 {
				return fmt.Errorf("email delivery rejected (%d)", res.StatusCode)
			}
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.New("email delivery interrupted")
			case <-timer.C:
			}
		}
	}
	return errors.New("email delivery unavailable")
}
