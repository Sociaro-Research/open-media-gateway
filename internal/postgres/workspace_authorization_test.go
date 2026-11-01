package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

func TestWorkspaceAuthorizationLifecycleAndIsolation(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewSystemRepository(db)
	people := NewAuthRepository(db)
	user := func(name string) identity.User {
		t.Helper()
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	admin, a, b := user("Admin"), user("Customer A"), user("Customer B")
	if e := repo.SetAdmin(ctx, "", admin.ID, true, true); e != nil {
		t.Fatal(e)
	}
	settings, _ := repo.Settings(ctx)
	settings.CustomerWorkspaces = true
	settings.SharedResources = true
	if _, e := repo.SaveSettings(ctx, admin.ID, settings); e != nil {
		t.Fatal(e)
	}
	company, e := repo.CreateOrganization(ctx, admin.ID, "Owned by B", b.ID, "owned-b.fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	manager := wa.New(repo, cipher)
	deviceProof := map[string]string{}
	keyProof := map[string]string{}
	start := func(replacement string) (string, wa.Challenge) {
		t.Helper()
		device, e := wa.DeviceCode()
		if e != nil {
			t.Fatal(e)
		}
		proof, _ := wa.DeviceCode()
		if replacement != "" {
			proof = keyProof[replacement]
		}
		deviceProof[device] = proof
		v, e := manager.Start(ctx, device, wa.Challenge{InstallationProof: proof, Label: "Synthetic installation", Origin: "http://private.lan:8088", ReplaceKey: replacement})
		if e != nil {
			t.Fatal(e)
		}
		return device, v
	}
	poll := func(device string) (wa.Challenge, string, error) {
		db.Exec(`UPDATE workspace_authorizations SET next_poll_at=NOW()-INTERVAL '1 second' WHERE device_hash=$1`, wa.Digest(device))
		v, token, err := manager.Poll(ctx, device)
		if err == nil {
			keyProof[v.KeyID] = deviceProof[device]
		}
		return v, token, err
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	device, v := start("")
	_, companies, create, e := repo.Describe(ctx, a.ID, v.Code)
	if e != nil || len(companies) != 0 || !create {
		t.Fatal("foreign company leaked", companies, e)
	}
	if _, e = repo.Approve(ctx, a.ID, v.Code, wa.Approval{Company: company.ID, Consent: true}); !errors.Is(e, wa.ErrForbidden) {
		t.Fatal("foreign company approved", e)
	}
	if _, _, e = manager.Poll(ctx, device); !errors.Is(e, wa.ErrPending) {
		t.Fatal(e)
	}
	if _, _, e = manager.Poll(ctx, device); !errors.Is(e, wa.ErrSlow) {
		t.Fatal("poll interval not enforced", e)
	}
	if count("user_api_keys") != 0 {
		t.Fatal("pending request issued key")
	}
	if _, e = repo.Approve(ctx, a.ID, v.Code, wa.Approval{}); !errors.Is(e, wa.ErrForbidden) {
		t.Fatal("missing consent", e)
	}
	in := wa.Approval{Consent: true}
	approved, e := repo.Approve(ctx, a.ID, v.Code, in)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e = repo.Approve(ctx, a.ID, v.Code, in); e != nil {
			t.Fatal("consent retry", e)
		}
	}
	if count("workspace_installations") != 1 || count("organization_credits") != 0 {
		t.Fatal("company duplicated or funded")
	}
	if _, _, _, e = repo.Describe(ctx, b.ID, v.Code); !errors.Is(e, wa.ErrForbidden) {
		t.Fatal("other browser consumed consent", e)
	}
	issued, token, e := poll(device)
	if e != nil {
		t.Fatal(e)
	}
	repeated, same, e := poll(device)
	if e != nil || issued.KeyID != repeated.KeyID || token != same || count("user_api_keys") != 1 {
		t.Fatal("lost reply issued another key", e)
	}
	raw, _ := json.Marshal(issued)
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), "cipher") {
		t.Fatal("secret in consent metadata")
	}
	keyRepo := NewAPIKeyRepository(db)
	_, key, e := keyRepo.Authenticate(ctx, apikey.Hash(token))
	if e != nil || key.OrganizationID != approved.Company || len(key.Scopes) != 1 || key.Scopes[0] != apikey.Generation {
		t.Fatal("wrong company/scope", e)
	}
	if e = repo.Revoke(ctx, b.ID, issued.KeyID); !errors.Is(e, wa.ErrUnavailable) {
		t.Fatal("foreign revoke", e)
	}
	// Renewal explicitly rotates only an installation key belonging to the same customer.
	renewal, request := start(issued.KeyID)
	if _, e = repo.Approve(ctx, b.ID, request.Code, wa.Approval{Company: company.ID, Consent: true, Replace: true}); !errors.Is(e, wa.ErrForbidden) {
		t.Fatal("foreign replacement accepted", e)
	}
	if _, e = repo.Approve(ctx, a.ID, request.Code, wa.Approval{Consent: true, Replace: true}); e != nil {
		t.Fatal(e)
	}
	replacement, replacementToken, e := poll(renewal)
	if e != nil || replacement.KeyID == issued.KeyID {
		t.Fatal("rotation failed", e)
	}
	if _, _, e = keyRepo.Authenticate(ctx, apikey.Hash(token)); !errors.Is(e, apikey.ErrNotFound) {
		t.Fatal("old key remains valid", e)
	}
	if e = repo.Cancel(ctx, wa.Digest(renewal)); e != nil {
		t.Fatal(e)
	}
	if e = repo.Cancel(ctx, wa.Digest(renewal)); e != nil {
		t.Fatal("cancel retry", e)
	}
	if _, _, e = keyRepo.Authenticate(ctx, apikey.Hash(replacementToken)); !errors.Is(e, apikey.ErrNotFound) {
		t.Fatal("cancel did not revoke issued grant", e)
	}
	if _, _, e = poll(renewal); !errors.Is(e, wa.ErrUnavailable) {
		t.Fatal("cancelled request issued another key", e)
	}
	// Approval cannot survive a lost membership before device proof.
	revoked, request := start("")
	bApproved, e := repo.Approve(ctx, b.ID, request.Code, wa.Approval{Consent: true})
	if e != nil {
		t.Fatal(e)
	}
	db.Exec(`UPDATE organization_members SET removed_at=NOW() WHERE organization_id=$1 AND user_id=$2`, bApproved.Company, b.ID)
	if _, _, e = poll(revoked); !errors.Is(e, wa.ErrForbidden) {
		t.Fatal("removed owner issued key", e)
	}
	expired, request := start("")
	db.Exec(`UPDATE workspace_authorizations SET expires_at=NOW()-INTERVAL '1 second' WHERE device_hash=$1`, wa.Digest(expired))
	if _, _, e = poll(expired); !errors.Is(e, wa.ErrUnavailable) {
		t.Fatal("expired code redeemed", e)
	}
	if _, e = repo.Approve(ctx, a.ID, request.Code, in); !errors.Is(e, wa.ErrUnavailable) {
		t.Fatal("expired user code approved", e)
	}
	// Access management remains available after disabling new public onboarding.
	settings, _ = repo.Settings(ctx)
	settings.CustomerWorkspaces = false
	if _, e = repo.SaveSettings(ctx, admin.ID, settings); e != nil {
		t.Fatal(e)
	}
	if e = repo.Revoke(ctx, a.ID, issued.KeyID); e != nil {
		t.Fatal(e)
	}
	grants, e := repo.Grants(ctx, a.ID)
	if e != nil || len(grants) != 2 {
		t.Fatal("own lifecycle history unavailable", grants, e)
	}
	if _, e = manager.Start(ctx, uuid.NewString(), wa.Challenge{}); e == nil {
		t.Fatal("invalid device proof accepted")
	}
}
func TestWorkspaceConsentCancellationAndSameInstallationRace(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewSystemRepository(db)
	people := NewAuthRepository(db)
	owner, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	db.Exec(`UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`)
	cipher, _ := providerconfig.NewCipher(strings.Repeat("r", 32))
	manager := wa.New(repo, cipher)
	proof, _ := wa.DeviceCode()
	codes := make([]string, 2)
	requests := make([]wa.Challenge, 2)
	for i := range codes {
		codes[i], _ = wa.DeviceCode()
		requests[i], e = manager.Start(ctx, codes[i], wa.Challenge{InstallationProof: proof, Label: "Installation", Origin: "http://localhost:8088"})
		if e != nil {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := range codes {
		wg.Go(func() {
			<-start
			_, e := repo.Approve(ctx, owner.ID, requests[i].Code, wa.Approval{Consent: true})
			results <- e
		})
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if !errors.Is(e, wa.ErrConflict) {
			t.Fatal(e)
		}
	}
	if successes != 2 {
		t.Fatal("same installation approvals failed", successes)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM organizations`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate company after race")
	}
	// Cancel and redemption lock the same request: whichever wins, a cancelled
	// final state must never leave a usable credential.
	for i := range codes {
		wg.Add(2)
		go func(i int) { defer wg.Done(); _ = repo.Cancel(ctx, wa.Digest(codes[i])) }(i)
		go func(i int) { defer wg.Done(); _, _, _ = manager.Poll(ctx, codes[i]) }(i)
		wg.Wait()
	}
	db.QueryRow(`SELECT count(*) FROM user_api_keys WHERE revoked_at IS NULL AND expires_at>NOW()`).Scan(&n)
	if n != 0 {
		t.Fatal("cancel/issue race left a live grant")
	}
}
