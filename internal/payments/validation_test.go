package payments

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/google/uuid"
)

func paymentDiagnostic(t *testing.T, err error, field string) {
	t.Helper()
	var diagnostic *ValidationError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &diagnostic) {
		t.Fatalf("expected typed invalid payment: %v", err)
	}
	got, hint := diagnostic.Diagnostic()
	if got != field || !strings.Contains(hint, field) {
		t.Fatalf("field=%q hint=%q want=%q", got, hint, field)
	}
}

func TestPaymentAmountAndSettlementDiagnostics(t *testing.T) {
	for _, amount := range []money.USD{0, money.USD(money.Scale - 1), money.USD(MaxCents*10000 + 10000), money.USD(money.Scale + 1)} {
		_, err := Cents(amount)
		paymentDiagnostic(t, err, "amount_usd")
	}
	for _, amount := range []money.USD{money.USD(money.Scale), money.USD(money.Scale + 10000), money.USD(MaxCents * 10000)} {
		cents, err := Cents(amount)
		if err != nil || cents != int64(amount)/10000 {
			t.Fatalf("accepted boundary %d: %d %v", amount, cents, err)
		}
	}
	valid := ExternalInput{OrganizationID: uuid.NewString(), ExternalID: "fixture", AmountCents: 100, Currency: "usd", Revision: 1}
	for _, tc := range []struct {
		field  string
		change func(*ExternalInput)
	}{
		{"organization_id", func(s *ExternalInput) { s.OrganizationID = "private-invalid-value" }},
		{"external_id", func(s *ExternalInput) { s.ExternalID = " " }},
		{"external_id", func(s *ExternalInput) { s.ExternalID = strings.Repeat("é", 65) }},
		{"currency", func(s *ExternalInput) { s.Currency = "USD" }},
		{"amount_cents", func(s *ExternalInput) { s.AmountCents = 99 }},
		{"amount_cents", func(s *ExternalInput) { s.AmountCents = MaxCents + 1 }},
		{"revision", func(s *ExternalInput) { s.Revision = 0 }},
		{"fee_cents", func(s *ExternalInput) { s.FeeCents = -1 }},
		{"fee_cents", func(s *ExternalInput) { s.FeeCents = 101 }},
		{"refunded_cents", func(s *ExternalInput) { s.RefundedCents = -1 }},
		{"refunded_cents", func(s *ExternalInput) { s.RefundedCents = 101 }},
		{"held_cents", func(s *ExternalInput) { s.HeldCents = -1 }},
		{"held_cents", func(s *ExternalInput) { s.HeldCents = 101 }},
	} {
		t.Run(tc.field, func(t *testing.T) { in := valid; tc.change(&in); paymentDiagnostic(t, in.Validate(), tc.field) })
	}
	// Each offset is independently bounded; their sum is deliberately not newly constrained.
	for _, amount := range []int64{MinCents, MaxCents} {
		in := valid
		in.AmountCents = amount
		in.FeeCents = amount
		in.RefundedCents = amount
		in.HeldCents = amount
		in.ExternalID = strings.Repeat("é", 64)
		if err := in.Validate(); err != nil {
			t.Fatal("accepted settlement changed", err)
		}
	}
}

type validationAdapter struct {
	Adapter
	calls int
}

var validationReachedAdapter = errors.New("synthetic adapter reached")

func (a *validationAdapter) Check(context.Context, Credentials) (Account, error) {
	a.calls++
	return Account{}, validationReachedAdapter
}

func TestPaymentProviderValidationDiagnostics(t *testing.T) {
	ctx := context.Background()
	adapter := &validationAdapter{}
	m := New(nil, nil, "https://gateway.test", map[string]Adapter{"stripe": adapter})
	valid := ProviderUpdate{Kind: "stripe", FeeMode: "service", Name: "Fixture", Enabled: true, SecretKey: "rk_" + "fixture", WebhookSecret: "whsec_" + "fixture"}
	for _, tc := range []struct {
		field  string
		change func(*ProviderUpdate)
	}{
		{"kind", func(in *ProviderUpdate) { in.Kind = "unknown" }},
		{"fee_mode", func(in *ProviderUpdate) { in.FeeMode = "unknown" }},
		{"name", func(in *ProviderUpdate) { in.Name = " " }},
		{"name", func(in *ProviderUpdate) { in.Name = strings.Repeat("é", 51) }},
		{"secret_key", func(in *ProviderUpdate) { in.SecretKey = "private-key-sentinel" }},
		{"secret_key", func(in *ProviderUpdate) { in.SecretKey = "rk_" + strings.Repeat("x", 510) }},
		{"secret_key", func(in *ProviderUpdate) { in.SecretKey = "rk_private\nvalue" }},
		{"webhook_secret", func(in *ProviderUpdate) { in.WebhookSecret = "" }},
		{"webhook_secret", func(in *ProviderUpdate) { in.WebhookSecret = "private-signing-sentinel" }},
		{"webhook_secret", func(in *ProviderUpdate) { in.WebhookSecret = "whsec_" + strings.Repeat("x", 507) }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			in := valid
			tc.change(&in)
			_, err := m.Save(ctx, "actor", "", in)
			paymentDiagnostic(t, err, tc.field)
		})
	}
	if adapter.calls != 0 {
		t.Fatal("invalid settings reached adapter")
	}
	for _, key := range []string{"rk_" + strings.Repeat("x", 509), "sk_" + "fixture", "rk_fixture\u00a0suffix"} {
		in := valid
		in.Name = strings.Repeat("é", 50)
		in.SecretKey = key
		in.WebhookSecret = "whsec_" + strings.Repeat("x", 506)
		if _, err := m.Save(ctx, "actor", "", in); !errors.Is(err, validationReachedAdapter) {
			t.Fatal("accepted settings changed", err)
		}
	}
	in := valid
	in.Enabled = false
	in.WebhookSecret = ""
	if _, err := m.Save(ctx, "actor", "", in); !errors.Is(err, validationReachedAdapter) {
		t.Fatal("disabled webhook changed", err)
	}
	m.PublicURL = "https://operator:private@gateway.test/path?private=value"
	if _, err := m.Save(ctx, "actor", "", valid); !errors.Is(err, ErrCallbackOrigin) {
		t.Fatal(err)
	}
	if _, _, err := m.NewIntegration(ctx, "actor", " ", false); err == nil {
		t.Fatal("blank integration name accepted")
	} else {
		paymentDiagnostic(t, err, "name")
	}
	for _, tc := range []struct {
		amount           money.USD
		key, mode, field string
	}{{0, "fixture", "service", "amount_usd"}, {money.USD(money.Scale), " ", "service", "Idempotency-Key"}, {money.USD(money.Scale), "fixture", "other", "fee_mode"}} {
		_, err := m.Checkout(ctx, "actor", "org", "provider", tc.amount, tc.key, tc.mode)
		paymentDiagnostic(t, err, tc.field)
	}
}
