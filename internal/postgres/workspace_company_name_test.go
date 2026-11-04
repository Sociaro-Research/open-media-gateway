package postgres

import (
	"errors"
	"strings"
	"sync"
	"testing"

	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

func TestInstallationCompanyAddressNamesAndIsolation(t *testing.T) {
	db, repo, manager, customer := dedicatedFixture(t)
	ctx := centralWorkspaceTestContext(t)
	other, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Other customer", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetAdmin(ctx, "", customer.ID, true, true); err != nil {
		t.Fatal(err)
	}
	manual, err := repo.CreateOrganization(ctx, customer.ID, "same.fixture.test", customer.ID, "manual.fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	start := func(proof, origin string) wa.Challenge {
		t.Helper()
		device, _ := wa.DeviceCode()
		request, e := manager.Start(ctx, device, wa.Challenge{Access: wa.CompanyAccess, InstallationProof: proof, Label: "Unrelated truncated label", Origin: origin})
		if e != nil {
			t.Fatal(e)
		}
		return request
	}
	approve := func(actor string, request wa.Challenge) wa.Challenge {
		t.Helper()
		result, e := repo.Approve(ctx, actor, request.Code, wa.Approval{Consent: true, Access: wa.CompanyAccess})
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	ids := map[string]bool{manual.ID: true}
	var first wa.Challenge
	var firstProof string
	for i, actor := range []string{customer.ID, customer.ID, other.ID} {
		proof, _ := wa.DeviceCode()
		request := start(proof, "https://same.fixture.test")
		result := approve(actor, request)
		if ids[result.Company] {
			t.Fatal("display address reused another tenant")
		}
		ids[result.Company] = true
		var name, member string
		if err = db.QueryRow(`SELECT o.name,m.user_id FROM organizations o JOIN organization_members m ON m.organization_id=o.id WHERE o.id=$1`, result.Company).Scan(&name, &member); err != nil || name != "same.fixture.test" || member != actor {
			t.Fatal("wrong address name or member", name, member, err)
		}
		if again := approve(actor, request); again.Company != result.Company {
			t.Fatal("lost approval response duplicated company")
		}
		if i == 0 {
			first, firstProof = result, proof
		}
	}
	// Identical display addresses never weaken manual name/domain uniqueness.
	if _, err = repo.CreateOrganization(ctx, customer.ID, "same.fixture.test", "", "new.fixture.test"); err == nil {
		t.Fatal("manual duplicate accepted")
	} else {
		var duplicate *org.Duplicate
		if !errors.As(err, &duplicate) {
			t.Fatal(err)
		}
	}
	if err = repo.OrganizationSettings(ctx, customer.ID, first.Company, "same.fixture.test", 0, nil, false); err != nil {
		t.Fatal("unchanged duplicate name blocked settings", err)
	}
	if err = repo.OrganizationSettings(ctx, customer.ID, first.Company, "Custom company", 0, nil, false); err != nil {
		t.Fatal(err)
	}
	for _, display := range []string{"https://new.fixture.test", "https://same.fixture.test"} {
		if renewed := approve(customer.ID, start(firstProof, display)); renewed.Company != first.Company {
			t.Fatal("reconnect lost binding")
		}
	}
	var name string
	if err = db.QueryRow(`SELECT name FROM organizations WHERE id=$1`, first.Company).Scan(&name); err != nil || name != "Custom company" {
		t.Fatal("reconnect renamed company", name, err)
	}
	if _, err = repo.Approve(ctx, other.ID, start(firstProof, "https://same.fixture.test").Code, wa.Approval{Consent: true, Access: wa.CompanyAccess}); !errors.Is(err, wa.ErrForbidden) {
		t.Fatal("same-address proof substituted account", err)
	}
	// Independent approval intents for the same proof serialize on the existing settings lock.
	proof, _ := wa.DeviceCode()
	requests := []wa.Challenge{start(proof, "https://race.fixture.test"), start(proof, "https://race.fixture.test")}
	type outcome struct {
		value wa.Challenge
		err   error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, request := range requests {
		wg.Add(1)
		go func(request wa.Challenge) {
			defer wg.Done()
			value, e := repo.Approve(ctx, customer.ID, request.Code, wa.Approval{Consent: true, Access: wa.CompanyAccess})
			results <- outcome{value, e}
		}(request)
	}
	wg.Wait()
	close(results)
	var company string
	for result := range results {
		if result.err != nil || company != "" && company != result.value.Company {
			t.Fatal("concurrent approval diverged", result.err)
		}
		company = result.value.Company
	}
}

func TestInstallationLongAddressSettingsPreserveManualLimits(t *testing.T) {
	db, repo, manager, customer := dedicatedFixture(t)
	ctx := centralWorkspaceTestContext(t)
	host := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + ":65535"
	proof, _ := wa.DeviceCode()
	device, _ := wa.DeviceCode()
	request, err := manager.Start(ctx, device, wa.Challenge{Access: wa.CompanyAccess, InstallationProof: proof, Label: host[:80], Origin: "https://" + host})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := repo.Approve(ctx, customer.ID, request.Code, wa.Approval{Consent: true, Access: wa.CompanyAccess})
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = db.QueryRow(`SELECT name FROM organizations WHERE id=$1`, approved.Company).Scan(&name); err != nil || name != host {
		t.Fatal("full address not retained", name, err)
	}
	if err = repo.OrganizationSettings(ctx, customer.ID, approved.Company, name, 0, nil, false); err != nil {
		t.Fatal("unchanged long address cannot save settings", err)
	}
	if err = repo.OrganizationSettings(ctx, customer.ID, approved.Company, strings.Repeat("x", 161), 0, nil, false); !errors.Is(err, org.ErrInvalid) {
		t.Fatal("overlong rename accepted", err)
	}
	if _, err = repo.CreateOrganization(ctx, customer.ID, strings.Repeat("x", 161), "", "manual.fixture.test"); !errors.Is(err, org.ErrInvalid) {
		t.Fatal("overlong manual creation accepted", err)
	}
	if err = repo.OrganizationSettings(ctx, customer.ID, approved.Company, "Short custom name", 0, nil, false); err != nil {
		t.Fatal("normal rename blocked", err)
	}
	if err = repo.OrganizationSettings(ctx, customer.ID, approved.Company, strings.Repeat(" ", 161)+"Short custom name", 0, nil, false); !errors.Is(err, org.ErrInvalid) {
		t.Fatal("manual input byte limit relaxed", err)
	}
}
