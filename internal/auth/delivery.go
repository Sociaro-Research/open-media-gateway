package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

var ErrDeliverySettings = errors.New("invalid email delivery settings")
var ErrDeliveryUnavailable = errors.New("email delivery is not configured")

// DeliveryRecord never crosses the HTTP boundary. Only the encrypted key is stored.
type DeliveryRecord struct {
	Provider      string
	From          string
	KeyCiphertext []byte
	Version       int64
}

type DeliveryRepository interface {
	EmailDelivery(context.Context) (DeliveryRecord, error)
	SaveEmailDelivery(context.Context, string, DeliveryRecord) (DeliveryRecord, error)
	BootstrapEmailDelivery(context.Context, DeliveryRecord) error
}

type DeliverySettings struct {
	Provider      string             `json:"provider"`
	From          string             `json:"from"`
	KeyConfigured bool               `json:"key_configured"`
	Enabled       bool               `json:"enabled"`
	Version       int64              `json:"version"`
	Available     []DeliveryProvider `json:"available_providers"`
}
type DeliveryProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type DeliveryUpdate struct {
	Provider string `json:"provider"`
	From     string `json:"from"`
	APIKey   string `json:"api_key,omitempty"`
	Version  int64  `json:"version"`
}

// DeliveryManager reads committed settings on each request so all gateway replicas
// see changes immediately. It never caches plaintext credentials or sends test mail.
type DeliveryManager struct {
	repo      DeliveryRepository
	cipher    *providerconfig.Cipher
	newSender func(string, string) (EmailSender, error)
}

func NewDeliveryManager(repo DeliveryRepository, cipher *providerconfig.Cipher) *DeliveryManager {
	return &DeliveryManager{repo: repo, cipher: cipher, newSender: func(key, from string) (EmailSender, error) { return NewResend(key, from) }}
}
func deliveryView(r DeliveryRecord) DeliverySettings {
	return DeliverySettings{Provider: r.Provider, From: r.From, KeyConfigured: len(r.KeyCiphertext) > 0,
		Enabled: r.Provider == "resend" && r.From != "" && len(r.KeyCiphertext) > 0, Version: r.Version,
		Available: []DeliveryProvider{{ID: "resend", Name: "Resend"}}}
}
func (m *DeliveryManager) Settings(ctx context.Context) (DeliverySettings, error) {
	r, err := m.repo.EmailDelivery(ctx)
	return deliveryView(r), err
}
func (m *DeliveryManager) Update(ctx context.Context, actor string, in DeliveryUpdate) (DeliverySettings, error) {
	if in.Version < 1 || (in.Provider != "" && in.Provider != "resend") {
		return DeliverySettings{}, ErrDeliverySettings
	}
	r, err := m.repo.EmailDelivery(ctx)
	if err != nil {
		return DeliverySettings{}, err
	}
	r.Provider = in.Provider
	r.From = strings.TrimSpace(in.From)
	r.Version = in.Version
	if in.Provider == "" {
		r.From = ""
		r.KeyCiphertext = nil
	} else {
		key := strings.TrimSpace(in.APIKey)
		if key == "" {
			plain, err := m.cipher.Decrypt(r.KeyCiphertext)
			if err != nil {
				return DeliverySettings{}, err
			}
			key = string(plain)
		}
		if len(key) > 512 || strings.ContainsAny(key, "\r\n\t ") || len(r.From) > 320 {
			return DeliverySettings{}, ErrDeliverySettings
		}
		if _, err := NewResend(key, r.From); err != nil {
			return DeliverySettings{}, ErrDeliverySettings
		}
		if in.APIKey != "" {
			r.KeyCiphertext, err = m.cipher.Encrypt([]byte(key))
			if err != nil {
				return DeliverySettings{}, err
			}
		}
	}
	r, err = m.repo.SaveEmailDelivery(ctx, actor, r)
	return deliveryView(r), err
}
func (m *DeliveryManager) Bootstrap(ctx context.Context, key, from string) error {
	if key == "" {
		return nil
	}
	current, err := m.repo.EmailDelivery(ctx)
	if err != nil {
		return err
	}
	if current.Version != 1 || current.Provider != "" {
		return nil
	}
	if _, err := NewResend(key, from); err != nil {
		return err
	}
	encrypted, err := m.cipher.Encrypt([]byte(key))
	if err != nil {
		return err
	}
	return m.repo.BootstrapEmailDelivery(ctx, DeliveryRecord{Provider: "resend", From: from, KeyCiphertext: encrypted})
}
func (m *DeliveryManager) sender(ctx context.Context) (EmailSender, error) {
	r, err := m.repo.EmailDelivery(ctx)
	if err != nil {
		return nil, err
	}
	if !deliveryView(r).Enabled {
		return nil, ErrDeliveryUnavailable
	}
	key, err := m.cipher.Decrypt(r.KeyCiphertext)
	if err != nil {
		return nil, err
	}
	sender, err := m.newSender(string(key), r.From)
	if err != nil {
		return nil, err
	}
	if resend, ok := sender.(*Resend); ok {
		if reader, ok := m.repo.(branding.Reader); ok {
			settings, err := reader.Branding(ctx)
			if err != nil {
				return nil, err
			}
			resend.brand = &settings
		}
	}
	return sender, nil
}
func (m *DeliveryManager) Configured(ctx context.Context) (bool, error) {
	_, err := m.sender(ctx)
	if errors.Is(err, ErrDeliveryUnavailable) {
		return false, nil
	}
	return err == nil, err
}
func (m *DeliveryManager) Send(ctx context.Context, to, purpose, link, idempotencyKey string) error {
	sender, err := m.sender(ctx)
	if err != nil {
		return err
	}
	return sender.Send(ctx, to, purpose, link, idempotencyKey)
}
func (m *DeliveryManager) SendNotification(ctx context.Context, to, subject, text, idempotencyKey string) error {
	sender, err := m.sender(ctx)
	if err != nil {
		return err
	}
	notifications, ok := sender.(interface {
		SendNotification(context.Context, string, string, string, string) error
	})
	if !ok {
		return ErrDeliveryUnavailable
	}
	return notifications.SendNotification(ctx, to, subject, text, idempotencyKey)
}
func (m *DeliveryManager) SendCatalogReport(ctx context.Context, to, subject, text, markup, key string) error {
	sender, err := m.sender(ctx)
	if err != nil {
		return err
	}
	if reports, ok := sender.(interface {
		SendCatalogReport(context.Context, string, string, string, string, string) error
	}); ok {
		return reports.SendCatalogReport(ctx, to, subject, text, markup, key)
	}
	return ErrDeliveryUnavailable
}
func (m *Manager) EmailDelivery() *DeliveryManager {
	if m.email == nil {
		return nil
	}
	sender, _ := m.email.sender.(*DeliveryManager)
	return sender
}
