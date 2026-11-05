package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

func organizationDiagnostic(t *testing.T, err error, field string) {
	t.Helper()
	var diagnostic org.ValidationError
	if !errors.Is(err, org.ErrInvalid) || !errors.As(err, &diagnostic) {
		t.Fatalf("untyped organization error: %v", err)
	}
	got, message := diagnostic.Diagnostic()
	if got != field || !strings.Contains(message, field) || strings.Contains(message, "PRIVATE_SENTINEL") {
		t.Fatalf("%q %q", got, message)
	}
}
func TestOrganizationFieldDiagnosticsBeforeStorage(t *testing.T) {
	repo := NewSystemRepository(nil)
	ctx := context.Background()
	negative := money.USD(-1)
	high := money.USD(1_000_000*money.Scale + 1)
	badMarkup := money.Percent(-1)
	for _, tc := range []struct {
		name   string
		credit money.USD
		weekly *money.USD
		markup []*money.Percent
		field  string
	}{
		{name: " ", field: "name"}, {name: "Team", credit: -1, field: "credit_limit_usd"}, {name: "Team", credit: high, field: "credit_limit_usd"},
		{name: "Team", weekly: &negative, field: "default_weekly_limit_usd"}, {name: "Team", weekly: &high, field: "default_weekly_limit_usd"},
		{name: "Team", markup: []*money.Percent{&badMarkup}, field: "markup_percent"},
	} {
		organizationDiagnostic(t, repo.OrganizationSettings(ctx, "actor", "org", tc.name, tc.credit, tc.weekly, true, tc.markup...), tc.field)
	}
	for _, name := range []string{" ", strings.Repeat("é", 81)} {
		_, err := repo.CreateOrganization(ctx, "actor", name, "", "")
		organizationDiagnostic(t, err, "name")
	}
	for _, limit := range []*money.USD{&negative, &high} {
		organizationDiagnostic(t, repo.SetOrganizationMember(ctx, "actor", "org", "target", "member", limit, true, false), "weekly_limit_usd")
	}
	valid := org.Credit{Amount: 1, Kind: "payment", ExternalID: "fixture"}
	for _, tc := range []struct {
		field  string
		change func(*org.Credit)
	}{
		{"amount_usd", func(c *org.Credit) { c.Amount = 0 }},
		{"amount_usd", func(c *org.Credit) { c.Amount = -money.USD(1_000_000*money.Scale + 1) }},
		{"kind", func(c *org.Credit) { c.Kind = "PRIVATE_SENTINEL" }},
		{"amount_usd", func(c *org.Credit) { c.Amount = -1 }},
		{"reason", func(c *org.Credit) { c.Reason = strings.Repeat("é", 501) }},
		{"external_id", func(c *org.Credit) { c.ExternalID = " " }},
		{"external_id", func(c *org.Credit) { c.ExternalID = strings.Repeat("é", 101) }},
	} {
		c := valid
		tc.change(&c)
		_, err := repo.AddOrganizationCredit(ctx, "actor", "org", c)
		organizationDiagnostic(t, err, tc.field)
	}
}
func TestOrganizationDiagnosticsRejectedMutationsAndExistingBoundaries(t *testing.T) {
	ctx := context.Background()
	db, repo, admin, owner := paymentDatabase(t)
	company, err := repo.CreateOrganization(ctx, admin, strings.Repeat("é", 80), owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var got string
		if err := db.QueryRow(`SELECT row_to_json(o)::text FROM organizations o WHERE id=$1`, company.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	previous := snapshot()
	for _, name := range []string{" ", strings.Repeat("é", 81), strings.Repeat(" ", 161) + "New"} {
		err = repo.OrganizationSettings(ctx, admin, company.ID, name, 0, nil, true)
		organizationDiagnostic(t, err, "name")
		if snapshot() != previous {
			t.Fatal("rejected settings changed organization")
		}
	}
	for _, value := range []money.USD{0, money.USD(1_000_000 * money.Scale)} {
		if err = repo.OrganizationSettings(ctx, admin, company.ID, "  Team  ", value, &value, true); err != nil {
			t.Fatal("accepted settings boundary changed", err)
		}
	}
	// Existing long installation names can still be saved unchanged.
	legacy := strings.Repeat("é", 100)
	if _, err = db.Exec(`UPDATE organizations SET name=$2 WHERE id=$1`, company.ID, legacy); err != nil {
		t.Fatal(err)
	}
	if err = repo.OrganizationSettings(ctx, admin, company.ID, legacy, 0, nil, true); err != nil {
		t.Fatal("legacy name changed", err)
	}
	for _, amount := range []money.USD{-money.USD(1_000_000 * money.Scale), money.USD(1_000_000*money.Scale + 1)} {
		if _, err = repo.AddOrganizationCredit(ctx, admin, company.ID, org.Credit{Amount: amount, Kind: "adjustment", Reason: strings.Repeat("é", 500), ExternalID: strings.Repeat("é", 80) + uuid.NewString()}); err != nil {
			t.Fatal("accepted credit boundary changed", err)
		}
	}
	var before int
	db.QueryRow(`SELECT count(*) FROM organization_credits WHERE organization_id=$1`, company.ID).Scan(&before)
	_, err = repo.AddOrganizationCredit(ctx, admin, company.ID, org.Credit{Amount: 1, Kind: "payment", ExternalID: " "})
	organizationDiagnostic(t, err, "external_id")
	var after int
	db.QueryRow(`SELECT count(*) FROM organization_credits WHERE organization_id=$1`, company.ID).Scan(&after)
	if after != before {
		t.Fatal("rejected credit posted")
	}
	// Valid-shaped changes still obey authorization and company boundaries.
	other, err := repo.CreateOrganization(ctx, admin, "Other", owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	bound := identity.WithCompanyBoundary(ctx, other.ID)
	previous = snapshot()
	if err = repo.OrganizationSettings(bound, admin, company.ID, "Changed", 0, nil, false); !errors.Is(err, ci.ErrDenied) {
		t.Fatalf("cross-tenant: %v", err)
	}
	if err = repo.OrganizationSettings(ctx, owner, company.ID, "Changed", 1, nil, true); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("credit permission: %v", err)
	}
	if snapshot() != previous {
		t.Fatal("unauthorized mutation changed organization")
	}
}

func TestProfileDiagnosticRejectionsPreserveStoredProfile(t *testing.T) {
	ctx := context.Background()
	db, _, admin, _ := paymentDatabase(t)
	manager, err := auth.New(ctx, NewAuthRepository(db), auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://gateway.test"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var got string
		if err := db.QueryRow(`SELECT row_to_json(u)::text FROM users u WHERE id=$1`, admin).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := snapshot()
	name := "Changed"
	theme := "PRIVATE_SENTINEL"
	preset := "unpublished"
	contacts := []identity.Contact{{Label: "Public", URL: "javascript:PRIVATE_SENTINEL"}}
	for _, in := range []auth.ProfileUpdate{{Name: &name, Theme: &theme}, {Name: &name, ColorPreset: &preset}, {Name: &name, Contacts: &contacts}} {
		if _, err = manager.UpdateProfile(ctx, admin, in); !errors.Is(err, auth.ErrInvalidProfile) {
			t.Fatalf("expected rejected profile: %v", err)
		}
		if snapshot() != before {
			t.Fatal("rejected profile changed stored data")
		}
	}
}
