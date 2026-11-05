package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func profileDiagnostic(t *testing.T, err error, field string) {
	t.Helper()
	var diagnostic *ProfileValidationError
	if !errors.Is(err, ErrInvalidProfile) || !errors.As(err, &diagnostic) {
		t.Fatalf("untyped profile error: %v", err)
	}
	got, message := diagnostic.Diagnostic()
	if got != field || !strings.Contains(message, field) || strings.Contains(message, "PRIVATE_SENTINEL") {
		t.Fatalf("%q %q", got, message)
	}
}
func TestProfileFieldDiagnosticsAndRejectedWrites(t *testing.T) {
	ptr := func(s string) *string { return &s }
	columns := func(s ...string) *[]string { return &s }
	contacts := func(c ...identity.Contact) *[]identity.Contact { return &c }
	good := identity.Contact{Label: "Public", URL: "https://example.test"}
	repo := &profileFixture{}
	m := &Manager{repo: repo}
	for _, tc := range []struct {
		in    ProfileUpdate
		field string
	}{
		{ProfileUpdate{}, "body"},
		{ProfileUpdate{Name: ptr(" ")}, "name"},
		{ProfileUpdate{Name: ptr(strings.Repeat("é", 101))}, "name"},
		{ProfileUpdate{Name: ptr("PRIVATE_SENTINEL\nname")}, "name"},
		{ProfileUpdate{Theme: ptr("PRIVATE_SENTINEL")}, "theme"},
		{ProfileUpdate{ColorPreset: ptr("PRIVATE_SENTINEL/")}, "color_preset"},
		{ProfileUpdate{ReportColumns: columns()}, "report_columns"},
		{ProfileUpdate{ReportColumns: columns("date", "PRIVATE_SENTINEL")}, "report_columns[1]"},
		{ProfileUpdate{ReportColumns: columns("date", "date")}, "report_columns[1]"},
		{ProfileUpdate{Contacts: contacts(make([]identity.Contact, 21)...)}, "contacts"},
		{ProfileUpdate{Contacts: contacts(good, identity.Contact{Label: " ", URL: good.URL})}, "contacts[1].label"},
		{ProfileUpdate{Contacts: contacts(identity.Contact{Label: strings.Repeat("🙂", 81), URL: good.URL})}, "contacts[0].label"},
		{ProfileUpdate{Contacts: contacts(good, identity.Contact{Label: "  Fine  ", URL: "javascript:PRIVATE_SENTINEL"})}, "contacts[1].url"},
		{ProfileUpdate{Contacts: contacts(identity.Contact{Label: "Fine", URL: "https://private:PRIVATE_SENTINEL@example.test"})}, "contacts[0].url"},
		{ProfileUpdate{Contacts: contacts(identity.Contact{Label: "Fine", URL: "https://example.test/" + strings.Repeat("x", 2049-len("https://example.test/"))})}, "contacts[0].url"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			var original []identity.Contact
			if tc.in.Contacts != nil {
				original = append([]identity.Contact{}, (*tc.in.Contacts)...)
			}
			_, err := m.UpdateProfile(context.Background(), "self", tc.in)
			profileDiagnostic(t, err, tc.field)
			if tc.in.Contacts != nil && !reflect.DeepEqual(original, *tc.in.Contacts) {
				t.Fatal("rejected request contacts mutated")
			}
		})
	}
	if repo.calls != 0 {
		t.Fatal("invalid profile reached storage", repo.calls)
	}
}
func TestProfileDiagnosticsPreserveUnicodeAndByteBoundaries(t *testing.T) {
	repo := &profileFixture{}
	m := &Manager{repo: repo}
	ctx := context.Background()
	for _, r := range []string{"a", "é", "🙂"} {
		name := "  " + strings.Repeat(r, 100) + "  "
		url := "https://example.test/" + strings.Repeat("x", 2048-len("https://example.test/"))
		contact := identity.Contact{Label: "  " + strings.Repeat(r, 80) + "  ", URL: "  " + url + "  "}
		contacts := make([]identity.Contact, 20)
		for i := range contacts {
			contacts[i] = contact
		}
		if _, err := m.UpdateProfile(ctx, "self", ProfileUpdate{Name: &name, Contacts: &contacts}); err != nil {
			t.Fatal("accepted Unicode/byte limit changed", err)
		}
	}
	for _, url := range []string{"http://example.test", "https://example.test", "mailto:public@example.test", "tel:+1234567"} {
		contacts := []identity.Contact{{Label: "Public", URL: url}}
		if _, err := m.UpdateProfile(ctx, "self", ProfileUpdate{Contacts: &contacts}); err != nil {
			t.Fatal("accepted contact scheme changed", err)
		}
	}
	empty := []identity.Contact{}
	all := []string{"date", "generation_id", "organization", "user", "api_key", "model", "provider", "status", "tokens", "estimated_cost", "cost", "confirmed_cost", "settlement", "customer_charge", "profit", "profit_status"}
	if _, err := m.UpdateProfile(ctx, "self", ProfileUpdate{Contacts: &empty, ReportColumns: &all}); err != nil {
		t.Fatal(err)
	}
	tooMany := append(all, "date")
	_, err := m.UpdateProfile(ctx, "self", ProfileUpdate{ReportColumns: &tooMany})
	profileDiagnostic(t, err, "report_columns")
}
