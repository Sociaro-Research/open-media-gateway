package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func TestVATSettingsAndVerificationAuthorization(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	settings, err := repo.VATSettings(ctx)
	if err != nil || settings.Version != 1 || settings.Ready() == nil {
		t.Fatal(settings, err)
	}
	settings.Seller = payments.SellerCompany{LegalName: "Operator Company Ltd", Address: payments.Address{Country: "GB", Line1: "1 Registered Road", City: "London"}, RegistrationNumber: "123456", VATRegistered: true, VATNumber: "GB123456789"}
	settings.Rules = []payments.VATRule{{Country: "domestic", Status: "any", Treatment: "standard", Rate: 2000}, {Country: "non_eu", Status: "any", Treatment: "outside_scope"}}
	if _, err = repo.SaveVATSettings(ctx, owner, settings); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("ordinary manager configured seller", err)
	}
	saved, err := repo.SaveVATSettings(ctx, admin, settings)
	if err != nil || saved.Version != 2 {
		t.Fatal(saved, err)
	}
	if _, err = repo.SaveVATSettings(ctx, admin, settings); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale settings accepted", err)
	}
	// Invalid numbers never change the stored draft; formatting is normalized
	// only after the ordinary version and authorization checks succeed.
	invalid := saved
	invalid.Seller.VATNumber = "GB!123"
	if _, err = repo.SaveVATSettings(ctx, admin, invalid); !errors.Is(err, payments.ErrSellerVATNumber) {
		t.Fatal("invalid seller VAT accepted", err)
	}
	saved.Seller.VATNumber = " gb 123 456 789 "
	saved, err = repo.SaveVATSettings(ctx, admin, saved)
	if err != nil || saved.Seller.VATNumber != "GB123456789" || saved.Version != 3 {
		t.Fatal("seller VAT normalization", saved, err)
	}
	org, err := repo.CreateOrganization(ctx, admin, "VAT buyer", owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	v := payments.VATVerification{Country: "GB", Number: "GB123456789", BusinessName: "VAT buyer", Verified: true, Reference: "Tax registration certificate checked"}
	if _, err = repo.SaveVATVerification(ctx, owner, org.ID, v); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("buyer verified own status", err)
	}
	checked, err := repo.SaveVATVerification(ctx, admin, org.ID, v)
	if err != nil || checked.Source != "manual" || checked.Version != 1 || !checked.ExpiresAt.After(time.Now()) {
		t.Fatal(checked, err)
	}
	if _, err = repo.SaveVATVerification(ctx, admin, org.ID, v); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale verification accepted", err)
	}
	checked.Verified = false
	revoked, err := repo.SaveVATVerification(ctx, admin, org.ID, checked)
	if err != nil || revoked.Verified || revoked.Version != 2 {
		t.Fatal("revoke", revoked, err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action IN ('payment_vat_settings','vat_verification')`).Scan(&count); err != nil || count < 3 {
		t.Fatal("missing VAT audit", count, err)
	}
}
