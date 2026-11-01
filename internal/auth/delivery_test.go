package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

type deliveryFixture struct {
	record  DeliveryRecord
	failure error
}

func (r *deliveryFixture) EmailDelivery(context.Context) (DeliveryRecord, error) {
	return r.record, r.failure
}
func (r *deliveryFixture) SaveEmailDelivery(_ context.Context, _ string, next DeliveryRecord) (DeliveryRecord, error) {
	next.Version++
	r.record = next
	return next, nil
}
func (r *deliveryFixture) BootstrapEmailDelivery(_ context.Context, next DeliveryRecord) error {
	r.record = next
	return nil
}

func TestDynamicDeliveryAndSecretHandling(t *testing.T) {
	ctx := context.Background()
	cipher, err := providerconfig.NewCipher(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := &deliveryFixture{record: DeliveryRecord{Version: 1}}
	sender := NewDeliveryManager(repo, cipher)
	var requests int
	var authorization string
	sender.newSender = func(key, from string) (EmailSender, error) {
		s, err := NewResend(key, from)
		if err != nil {
			return nil, err
		}
		s.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			authorization = r.Header.Get("Authorization")
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"fixture"}`))}, nil
		})
		return s, nil
	}
	manager := &Manager{email: &emailAuth{sender: sender}}
	methods := func() map[string]bool {
		t.Helper()
		w := httptest.NewRecorder()
		manager.Methods(w, httptest.NewRequest("GET", "/auth/methods", nil))
		var v map[string]bool
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if methods()["email"] || methods()["email_delivery"] || !methods()["email_confirmation"] {
		t.Fatal("unconfigured delivery must hide email login while retaining existing links")
	}
	key := "fixture-private-resend-key"
	settings, err := sender.Update(ctx, "admin", DeliveryUpdate{Provider: "resend", From: "Sociaro <login@example.test>", APIKey: key, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("saving sent an email")
	}
	if !methods()["email"] || !methods()["email_delivery"] {
		t.Fatal("configuration did not activate email dynamically")
	}
	public, _ := json.Marshal(settings)
	if strings.Contains(string(public), key) || strings.Contains(string(repo.record.KeyCiphertext), key) {
		t.Fatal("key leaked")
	}
	if err = sender.Send(ctx, "user@example.test", "verify", "https://example.test/#email_verify=fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || authorization != "Bearer "+key {
		t.Fatal("wrong configured key")
	}
	// Editing a non-secret field without a key must preserve the saved credential.
	settings, err = sender.Update(ctx, "admin", DeliveryUpdate{Provider: "resend", From: "Support <support@example.test>", Version: settings.Version})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(repo.record.KeyCiphertext)
	if err != nil || string(plain) != key {
		t.Fatal("key lost on sender edit")
	}
	settings, err = sender.Update(ctx, "admin", DeliveryUpdate{Provider: "resend", From: settings.From, APIKey: "replacement-key", Version: settings.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err = sender.Send(ctx, "user@example.test", "reset", "https://example.test/#email_reset=fixture", "fixture-2"); err != nil || authorization != "Bearer replacement-key" {
		t.Fatal("rotation not applied", err)
	}
	for _, in := range []DeliveryUpdate{
		{Provider: "unsupported", Version: settings.Version},
		{Provider: "resend", From: "bad address", APIKey: "key", Version: settings.Version},
		{Provider: "resend", From: settings.From, APIKey: "key\r\ninjected", Version: settings.Version},
	} {
		if _, err = sender.Update(ctx, "admin", in); !errors.Is(err, ErrDeliverySettings) {
			t.Fatal("invalid configuration accepted", err)
		}
	}
	_, err = sender.Update(ctx, "admin", DeliveryUpdate{Provider: "", Version: settings.Version})
	if err != nil {
		t.Fatal(err)
	}
	if methods()["email"] || methods()["email_delivery"] || len(repo.record.KeyCiphertext) > 0 {
		t.Fatal("disconnect retained availability or key")
	}
	if err = sender.Send(ctx, "user@example.test", "verify", "unused", "unused"); !errors.Is(err, ErrDeliveryUnavailable) {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal("unexpected mail sent")
	}
	repo.failure = errors.New("database unavailable")
	if methods()["email_delivery"] {
		t.Fatal("failed lookup exposed registration")
	}
}
