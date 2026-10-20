package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestCompanyIdentityAcrossCreationPaths(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	r := NewSystemRepository(db)
	people := NewAuthRepository(db)
	user := func(name string) identity.User {
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	admin, manager, owner := user("Admin"), user("Sales"), user("Owner")
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(r.SetAdmin(ctx, "", admin.ID, true, true))
	must(r.SetClientManager(ctx, admin.ID, manager.ID, true))
	onboarding, e := org.NewOnboarding(r, nil, "https://console.fixture.test", strings.Repeat("s", 32))
	must(e)
	input := func(name, domain string) org.ClientInput {
		return org.ClientInput{OperationID: uuid.NewString(), Name: name, Domain: domain, Country: "CY", ContactName: "Owner", Email: owner.Email}
	}
	first, e := r.CreateOrganization(ctx, admin.ID, "  Acme   Team ", owner.ID, "https://WWW.Acme.test/about")
	must(e)
	if first.Name != "Acme Team" || first.Domain != "acme.test" {
		t.Fatal(first)
	}
	duplicate := func(e error, criterion string, visible bool) {
		t.Helper()
		var d *org.Duplicate
		if !errors.As(e, &d) || d.Criterion != criterion || (d.OrganizationID != "") != visible {
			t.Fatalf("duplicate %s: %+v %v", criterion, d, e)
		}
	}
	_, e = onboarding.Create(ctx, manager.ID, input(" aCME  TEAM ", "another.test"))
	duplicate(e, "name", false)
	_, e = onboarding.Create(ctx, manager.ID, input("Different company", "http://acme.test:443/contact"))
	duplicate(e, "domain", false)
	_, e = r.CreateOrganization(ctx, admin.ID, "Duplicate website", owner.ID, "acme.test")
	duplicate(e, "domain", true)
	if _, e = r.CreateOrganization(ctx, admin.ID, "Missing website", owner.ID, ""); !errors.Is(e, org.ErrInvalid) {
		t.Fatal("missing admin website", e)
	}
	if _, e = onboarding.Create(ctx, manager.ID, input("Missing sales website", "")); !errors.Is(e, org.ErrInvalid) {
		t.Fatal("missing sales website", e)
	}
	// A contact can own several genuinely distinct companies.
	second, e := onboarding.Create(ctx, manager.ID, input("Second company", "second.test"))
	must(e)
	third, e := onboarding.Create(ctx, manager.ID, input("Third company", "third.test"))
	must(e)
	if second.OrganizationID == third.OrganizationID {
		t.Fatal("same contact conflated companies")
	}
	listed, e := r.Organizations(ctx, admin.ID, true)
	must(e)
	found := false
	for _, company := range listed {
		if company.ID == second.OrganizationID {
			found = company.Domain == "second.test"
		}
	}
	if !found {
		t.Fatal("website missing from organization")
	}
	// Concurrent sales/admin requests with independent retry IDs must create exactly one row.
	for _, criterion := range []string{"name", "domain"} {
		var wg sync.WaitGroup
		results := make(chan error, 12)
		for n := 0; n < 12; n++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				name, domain := fmt.Sprintf("Race company %s %d", criterion, n), fmt.Sprintf("race-%s-%d.test", criterion, n)
				if criterion == "name" {
					name = "Shared race name"
					if n%2 == 0 {
						name = " SHARED   RACE NAME "
					}
				} else {
					domain = "https://WWW.Race-domain.test/path"
				}
				var err error
				if n%2 == 0 {
					_, err = r.CreateOrganization(ctx, admin.ID, name, owner.ID, domain)
				} else {
					_, err = onboarding.Create(ctx, manager.ID, input(name, domain))
				}
				results <- err
			}(n)
		}
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			} else {
				var d *org.Duplicate
				if !errors.As(err, &d) {
					t.Fatal("unexpected create error", err)
				}
			}
		}
		if success != 1 {
			t.Fatalf("%s race created %d companies", criterion, success)
		}
	}
	// Renaming cannot bypass uniqueness; unchanged names and other settings still save.
	must(r.OrganizationSettings(ctx, admin.ID, first.ID, first.Name, 0, nil, false))
	e = r.OrganizationSettings(ctx, admin.ID, first.ID, " Second   COMPANY ", 0, nil, false)
	duplicate(e, "name", true)
	must(r.OrganizationSettings(ctx, admin.ID, first.ID, "Renamed company", 0, nil, false))
	_, e = r.CreateOrganization(ctx, admin.ID, "Acme Team", owner.ID, "new-acme.test")
	must(e)
	_, e = r.CreateOrganization(ctx, admin.ID, "Another name", owner.ID, "www.acme.test")
	duplicate(e, "domain", true)
	var organizations, invites, deliveries int
	must(db.QueryRow(`SELECT count(*) FROM organizations WHERE name='Missing website' OR name='Missing sales website' OR name='Duplicate website'`).Scan(&organizations))
	if organizations != 0 {
		t.Fatal("blocked creation left a row")
	}
	must(db.QueryRow(`SELECT (SELECT count(*) FROM client_onboardings),(SELECT count(*) FROM organization_invites WHERE first_owner),(SELECT count(*) FROM owner_invitation_deliveries)`).Scan(&organizations, &invites, &deliveries))
	if organizations != invites || invites != deliveries {
		t.Fatal("orphan invitation side effect", organizations, invites, deliveries)
	}
}

func TestCompanyIdentityMigrationKeepsHistoricalCompanies(t *testing.T) {
	db := auditDatabase(t, false)
	ctx := context.Background()
	goose.SetBaseFS(migrations)
	if e := goose.SetDialect("postgres"); e != nil {
		t.Fatal(e)
	}
	if e := goose.UpTo(db, "migrations", 58); e != nil {
		t.Fatal(e)
	}
	// Seed the historical schema directly; current profile readers require migration 064.
	admin := identity.User{ID: uuid.NewString()}
	_, e := db.Exec(`INSERT INTO users(id,email,display_name) VALUES($1,$2,'Admin')`, admin.ID, uuid.NewString()+"@fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	r := NewSystemRepository(db)
	if e = r.SetAdmin(ctx, "", admin.ID, true, true); e != nil {
		t.Fatal(e)
	}
	first, second, invite := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Legacy Company'),($2,'legacy company')`, first, second); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO organization_invites(id,organization_id,token_hash,created_by,expires_at,first_owner,recipient_email) VALUES($1,$2,$3,$4,NOW()+INTERVAL '14 days',TRUE,'owner@fixture.test')`, invite, first, make([]byte, 32), admin.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO client_onboardings(organization_id,country,domain,contact_name,email,invite_id) VALUES($1,'CY','legacy.test','Owner','owner@fixture.test',$2)`, first, invite); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	var domain string
	if e = db.QueryRow(`SELECT domain FROM organizations WHERE id=$1`, first).Scan(&domain); e != nil || domain != "legacy.test" {
		t.Fatal("website backfill", domain, e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM organizations WHERE id IN ($1,$2)`, first, second).Scan(&count); e != nil || count != 2 {
		t.Fatal("historical companies changed", count, e)
	}
	for _, test := range []struct{ name, domain, criterion string }{{"New name", "https://www.legacy.test/about", "domain"}, {" LEGACY  COMPANY ", "new.test", "name"}} {
		_, e = r.CreateOrganization(ctx, admin.ID, test.name, "", test.domain)
		var d *org.Duplicate
		if !errors.As(e, &d) || d.Criterion != test.criterion {
			t.Fatal("legacy duplicate", d, e)
		}
	}
	if e = r.OrganizationSettings(ctx, admin.ID, second, "legacy company", 0, nil, false); e != nil {
		t.Fatal("historical company settings blocked", e)
	}
}
