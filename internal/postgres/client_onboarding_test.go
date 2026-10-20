package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/api"
	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/google/uuid"
)

type ownerMailbox struct {
	fail         bool
	bodies, keys []string
}

func (m *ownerMailbox) SendNotification(_ context.Context, _, _, body, key string) error {
	m.bodies = append(m.bodies, body)
	m.keys = append(m.keys, key)
	if m.fail {
		return errors.New("fixture unavailable")
	}
	return nil
}
func (m *ownerMailbox) token() string {
	return strings.SplitN(strings.SplitN(m.bodies[len(m.bodies)-1], "#invite=", 2)[1], "\n", 2)[0]
}

func TestClientOnboardingLifecycle(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewSystemRepository(db)
	people := NewAuthRepository(db)
	user := func(email string) identity.User {
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), email, email, "")
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	admin := user("admin@fixture.test")
	manager := user("sales@fixture.test")
	other := user("other@fixture.test")
	owner := user("owner+team@fixture.test")
	wrong := user("wrong@fixture.test")
	if e := repo.SetAdmin(ctx, "", admin.ID, true, true); e != nil {
		t.Fatal(e)
	}
	for _, u := range []identity.User{manager, other} {
		if e := repo.SetClientManager(ctx, admin.ID, u.ID, true); e != nil {
			t.Fatal(e)
		}
	}
	mailbox := &ownerMailbox{fail: true}
	m, e := org.NewOnboarding(repo, mailbox, "https://console.fixture.test", strings.Repeat("s", 32))
	if e != nil {
		t.Fatal(e)
	}
	in := org.ClientInput{OperationID: uuid.NewString(), Name: "  Acme   Team ", Country: "cy", Domain: "https://WWW.Acme.test/start", ContactName: "Owner", Email: " OWNER+team@fixture.test "}
	if _, e = m.Create(ctx, wrong.ID, in); !errors.Is(e, org.ErrForbidden) {
		t.Fatalf("ordinary account created company: %v", e)
	}
	if _, e = m.Create(identity.WithAPIKeyID(ctx, uuid.NewString()), manager.ID, in); !errors.Is(e, org.ErrForbidden) {
		t.Fatalf("old key created company: %v", e)
	}
	c, e := m.Create(ctx, manager.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	if c.Name != "Acme Team" || c.Domain != "acme.test" || c.Email != "owner+team@fixture.test" || c.Status != "sending" || time.Until(c.ExpiresAt) < 14*24*time.Hour-time.Minute {
		t.Fatalf("initial card: %+v", c)
	}
	var actual *string
	var members int
	if e = db.QueryRow(`SELECT client_manager_id,(SELECT count(*) FROM organization_members WHERE organization_id=o.id) FROM organizations o WHERE id=$1`, c.OrganizationID).Scan(&actual, &members); e != nil || actual != nil || members != 0 {
		t.Fatalf("premature activation: %v %v %v", actual, members, e)
	}
	if _, e = repo.OrganizationAccess(ctx, manager.ID, c.OrganizationID); !errors.Is(e, org.ErrForbidden) {
		t.Fatal("provisional manager acquired organization permissions")
	}
	if c2, e := m.Create(ctx, manager.ID, in); e != nil || c2.OrganizationID != c.OrganizationID {
		t.Fatalf("create replay: %+v %v", c2, e)
	}
	var creates sync.WaitGroup
	createResults := make(chan error, 8)
	for n := 0; n < 8; n++ {
		creates.Add(1)
		go func() {
			defer creates.Done()
			next, e := m.Create(ctx, manager.ID, in)
			if e == nil && next.OrganizationID != c.OrganizationID {
				e = errors.New("created second company")
			}
			createResults <- e
		}()
	}
	creates.Wait()
	close(createResults)
	for e := range createResults {
		if e != nil {
			t.Fatalf("concurrent create replay: %v", e)
		}
	}
	var inviteCount, deliveryCount int
	if e = db.QueryRow(`SELECT (SELECT count(*) FROM organization_invites WHERE organization_id=$1),(SELECT count(*) FROM owner_invitation_deliveries WHERE organization_id=$1)`, c.OrganizationID).Scan(&inviteCount, &deliveryCount); e != nil || inviteCount != 1 || deliveryCount != 1 {
		t.Fatalf("duplicate invite/outbox: %d %d %v", inviteCount, deliveryCount, e)
	}
	changed := in
	changed.Name = "Different"
	if _, e = m.Create(ctx, manager.ID, changed); !errors.Is(e, org.ErrConflict) {
		t.Fatalf("operation reused: %v", e)
	}
	for _, criterion := range []string{"name", "domain"} {
		dup := in
		dup.OperationID = uuid.NewString()
		dup.Name = "Another"
		dup.Domain = "other.test"
		dup.Email = "different@fixture.test"
		switch criterion {
		case "name":
			dup.Name = " acME  TEAM "
		case "domain":
			dup.Domain = "http://acme.test/path"
		case "email":
			dup.Email = "owner+team@fixture.test"
		}
		_, e = m.Create(ctx, other.ID, dup)
		var d *org.Duplicate
		if !errors.As(e, &d) || d.Criterion != criterion || d.OrganizationID != "" || !strings.Contains(d.Manager, "sales") {
			t.Fatalf("foreign duplicate information: %+v %v", d, e)
		}
	}
	m.Deliver(ctx)
	oldToken := mailbox.token()
	cards, e := repo.ClientOnboardings(ctx, manager.ID)
	if e != nil || len(cards) != 1 || cards[0].Status != "send_failed" {
		t.Fatalf("failed mail lost card: %+v %v", cards, e)
	}
	mailbox.fail = false
	db.Exec(`UPDATE owner_invitation_deliveries SET next_attempt_at=NOW()`)
	m.Deliver(ctx)
	if mailbox.keys[0] != mailbox.keys[1] || mailbox.bodies[0] != mailbox.bodies[1] {
		t.Fatal("delivery retry changed provider idempotency payload")
	}
	var stored []byte
	db.QueryRow(`SELECT token_hash FROM organization_invites WHERE organization_id=$1`, c.OrganizationID).Scan(&stored)
	hash := sha256.Sum256([]byte(oldToken))
	if string(stored) != string(hash[:]) {
		t.Fatal("invitation hash mismatch")
	}
	if _, e = repo.OrganizationInvitation(ctx, wrong.ID, oldToken, false); !errors.Is(e, org.ErrRecipient) {
		t.Fatalf("wrong recipient preview: %v", e)
	}
	if _, e = repo.OrganizationInvitation(ctx, wrong.ID, oldToken, true); !errors.Is(e, org.ErrRecipient) {
		t.Fatalf("wrong recipient accepted: %v", e)
	}
	op := uuid.NewString()
	c, e = m.Change(ctx, manager.ID, c.OrganizationID, op, c.Version, false)
	if e != nil {
		t.Fatal(e)
	}
	if c2, e := m.Change(ctx, manager.ID, c.OrganizationID, op, c.Version-1, false); e != nil || c2.Version != c.Version {
		t.Fatalf("resend replay: %+v %v", c2, e)
	}
	if _, e = repo.OrganizationInvitation(ctx, owner.ID, oldToken, false); e != nil {
		t.Fatalf("old link lost before successful resend: %v", e)
	}
	m.Deliver(ctx)
	if _, e = repo.OrganizationInvitation(ctx, owner.ID, oldToken, true); !errors.Is(e, org.ErrNotFound) {
		t.Fatalf("old link remained after successful resend: %v", e)
	}
	token := mailbox.token()
	preview, e := repo.OrganizationInvitation(ctx, owner.ID, token, false)
	if e != nil || preview.Role != org.Supermanager {
		t.Fatalf("owner preview: %+v %v", preview, e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := repo.OrganizationInvitation(ctx, owner.ID, token, true); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("concurrent acceptance: %v", e)
		}
	}
	if role, e := repo.OrganizationAccess(ctx, owner.ID, c.OrganizationID); e != nil || role != org.Supermanager {
		t.Fatalf("first owner role: %s %v", role, e)
	}
	if role, e := repo.OrganizationAccess(ctx, manager.ID, c.OrganizationID); e != nil || role != "client_manager" {
		t.Fatalf("manager assignment: %s %v", role, e)
	}
	var activations int
	var brokenObjects int
	if e = db.QueryRow(`SELECT count(*) FROM audit_events WHERE action='client_onboarding' AND organization_id<>object_id`).Scan(&brokenObjects); e != nil || brokenObjects != 0 {
		t.Fatalf("onboarding audit object: %d %v", brokenObjects, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action='client_onboarding' AND details->'after' ? 'activated_at' AND details->'after'->'activated_at'<>'null'::jsonb`, c.OrganizationID).Scan(&activations); e != nil || activations != 1 {
		t.Fatalf("activation count: %d %v", activations, e)
	}
	keys := NewAPIKeyRepository(db)
	key := apikey.Key{ID: uuid.NewString(), Name: "Owner key", Prefix: "gw_fixture", Scopes: []string{apikey.Generation}, CreatedAt: time.Now(), OrganizationID: c.OrganizationID}
	if e = keys.Create(ctx, owner.ID, key, apikey.Hash("owner-secret")); e != nil {
		t.Fatalf("owner cannot issue existing key: %v", e)
	}
	key.ID = uuid.NewString()
	if e = keys.Create(ctx, manager.ID, key, apikey.Hash("sales-secret")); !errors.Is(e, apikey.ErrForbidden) {
		t.Fatalf("sales manager created key: %v", e)
	}
	if _, e = m.Change(ctx, manager.ID, c.OrganizationID, uuid.NewString(), c.Version+1, false); !errors.Is(e, org.ErrInvitationUnavailable) {
		t.Fatalf("resend after activation: %v", e)
	}
	if _, e = repo.CreateOrganization(ctx, manager.ID, "Unauthorized", owner.ID, uuid.NewString()+".fixture.test"); e == nil {
		t.Fatal("manager used administrator creation")
	}
	// Normal invitations retain the existing 7-day member behavior.
	i, e := repo.CreateOrganizationInvite(ctx, owner.ID, c.OrganizationID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.OrganizationInvitation(ctx, wrong.ID, i.Token, true); e != nil {
		t.Fatal(e)
	}
	if role, e := repo.OrganizationAccess(ctx, wrong.ID, c.OrganizationID); e != nil || role != org.MemberRole {
		t.Fatalf("ordinary invitation changed: %s %v", role, e)
	}
}

func TestClientOnboardingRecoveryAndQuota(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewSystemRepository(db)
	people := NewAuthRepository(db)
	user := func(email string) identity.User {
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), email, email, "")
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	admin := user("admin@fixture.test")
	manager := user("sales@fixture.test")
	owner := user("new@fixture.test")
	if e := repo.SetAdmin(ctx, "", admin.ID, true, true); e != nil {
		t.Fatal(e)
	}
	if e := repo.SetClientManager(ctx, admin.ID, manager.ID, true); e != nil {
		t.Fatal(e)
	}
	mailbox := &ownerMailbox{}
	m, _ := org.NewOnboarding(repo, mailbox, "https://console.fixture.test", strings.Repeat("s", 32))
	in := org.ClientInput{OperationID: uuid.NewString(), Name: "First", Domain: "first.fixture.test", Country: "CY", ContactName: "New owner", Email: owner.Email}
	c, e := m.Create(ctx, manager.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	m.Deliver(ctx)
	first := mailbox.token()
	c, e = m.Change(ctx, manager.ID, c.OrganizationID, uuid.NewString(), c.Version, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.OrganizationInvitation(ctx, owner.ID, first, true); !errors.Is(e, org.ErrInvitationUnavailable) {
		t.Fatalf("revoked invite accepted: %v", e)
	}
	c, e = m.Change(ctx, manager.ID, c.OrganizationID, uuid.NewString(), c.Version, false)
	if e != nil {
		t.Fatal(e)
	}
	m.Deliver(ctx)
	first = mailbox.token()
	db.Exec(`UPDATE organization_invites SET expires_at=NOW()-INTERVAL '1 second' WHERE organization_id=$1`, c.OrganizationID)
	if _, e = repo.OrganizationInvitation(ctx, owner.ID, first, true); !errors.Is(e, org.ErrInvitationUnavailable) {
		t.Fatalf("expired invite accepted: %v", e)
	}
	c, e = m.Change(ctx, manager.ID, c.OrganizationID, uuid.NewString(), c.Version, false)
	if e != nil {
		t.Fatal(e)
	}
	m.Deliver(ctx)
	first = mailbox.token()
	if e = repo.SetClientManager(ctx, admin.ID, manager.ID, false); e != nil {
		t.Fatal(e)
	}
	if e = repo.SetClientManager(ctx, admin.ID, manager.ID, true); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Change(ctx, manager.ID, c.OrganizationID, uuid.NewString(), c.Version, false); !errors.Is(e, org.ErrForbidden) {
		t.Fatalf("role reactivation restored provisional access: %v", e)
	}
	if _, e = repo.OrganizationInvitation(ctx, owner.ID, first, true); e != nil {
		t.Fatalf("customer lost invite after sales deactivation: %v", e)
	}
	var assigned *string
	db.QueryRow(`SELECT client_manager_id FROM organizations WHERE id=$1`, c.OrganizationID).Scan(&assigned)
	if assigned != nil {
		t.Fatal("stale invite restored sales assignment")
	}
	// First card counts as one distinct client; repeated resend/revoke did not
	// exhaust the rolling quota. Exactly 19 additional clients are allowed.
	for n := 0; n < 20; n++ {
		in.OperationID = uuid.NewString()
		in.Name = fmt.Sprintf("Company %d", n)
		in.Domain = fmt.Sprintf("company%d.fixture.test", n)
		in.Email = fmt.Sprintf("owner%d@fixture.test", n)
		_, e = m.Create(ctx, manager.ID, in)
		if n < 19 && e != nil {
			t.Fatalf("quota rejected client %d: %v", n, e)
		}
		if n == 19 && !errors.Is(e, org.ErrOnboardingLimit) {
			t.Fatalf("twenty-first client: %v", e)
		}
	}
	db.Exec(`UPDATE client_onboarding_operations SET created_at=NOW()-INTERVAL '61 minutes'`)
	if _, e = m.Create(ctx, manager.ID, in); e != nil {
		t.Fatalf("rolling quota did not expire: %v", e)
	}
}

func TestNewCustomerRegistersThenAcceptsOwnerInvite(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewSystemRepository(db)
	people := NewAuthRepository(db)
	admin, e := people.UpsertGoogleUser(ctx, uuid.NewString(), "admin@fixture.test", "Admin", "")
	if e != nil {
		t.Fatal(e)
	}
	manager, e := people.UpsertGoogleUser(ctx, uuid.NewString(), "sales@fixture.test", "Sales", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.SetAdmin(ctx, "", admin.ID, true, true); e != nil {
		t.Fatal(e)
	}
	if e = repo.SetClientManager(ctx, admin.ID, manager.ID, true); e != nil {
		t.Fatal(e)
	}
	settings, e := repo.Settings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	settings.SharedResources = true
	if _, e = repo.SaveSettings(ctx, admin.ID, settings); e != nil {
		t.Fatal(e)
	}
	mailbox := &ownerMailbox{}
	onboarding, _ := org.NewOnboarding(repo, mailbox, "https://console.fixture.test", strings.Repeat("s", 32))
	c, e := onboarding.Create(ctx, manager.ID, org.ClientInput{OperationID: uuid.NewString(), Name: "Registered client", Domain: "registered.fixture.test", Country: "CY", ContactName: "Customer", Email: "customer@fixture.test"})
	if e != nil {
		t.Fatal(e)
	}
	onboarding.Deliver(ctx)
	inviteToken := mailbox.token()
	registration := &authMailbox{}
	login, e := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://console.fixture.test", EmailSender: registration})
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/email/{action}", login.Email)
	call := func(action, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://console.fixture.test/auth/email/"+action, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	w := call("register", `{"email":"customer@fixture.test"}`)
	if w.Code != 202 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	w = call("verify", `{"token":"`+registration.token()+`"}`)
	if w.Code != 200 {
		t.Fatalf("verify: %d %s", w.Code, w.Body.String())
	}
	w = call("complete", `{"password":"A long customer password 123!"}`, w.Result().Cookies()...)
	if w.Code != 200 {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "https://console.fixture.test/auth/session", nil)
	for _, cookie := range w.Result().Cookies() {
		r.AddCookie(cookie)
	}
	customer, e := login.Authenticate(r)
	if e != nil {
		t.Fatal(e)
	}
	h := api.NewGateway(api.Dependencies{Auth: login, System: repo, Keys: NewAPIKeyRepository(db), Onboarding: onboarding})
	apiCall := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://console.fixture.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, cookie := range w.Result().Cookies() {
			r.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		return response
	}
	response := apiCall("/v1/invitations/accept", `{"token":"`+inviteToken+`"}`)
	if response.Code != 200 {
		t.Fatalf("HTTP owner acceptance: %d %s", response.Code, response.Body.String())
	}
	response = apiCall("/v1/api-keys", `{"name":"Company application","organization_id":"`+c.OrganizationID+`","scopes":["generation"],"resource_mode":"shared"}`)
	if response.Code != 201 {
		t.Fatalf("HTTP owner key issuance: %d %s", response.Code, response.Body.String())
	}
	if role, e := repo.OrganizationAccess(ctx, customer.ID, c.OrganizationID); e != nil || role != org.Supermanager {
		t.Fatalf("new customer owner: %s %v", role, e)
	}
	var issued apikey.Key
	if e = json.Unmarshal(response.Body.Bytes(), &issued); e != nil || issued.ID == "" {
		t.Fatalf("new owner key response: %v", e)
	}
	ownerContext := identity.WithOrganization(identity.WithAPIKeyID(identity.WithUser(ctx, customer), issued.ID), c.OrganizationID, true)
	ownerContext = identity.WithResourceOwners(ownerContext, identity.SharedOwnerID, identity.SharedOwnerID)
	tasks := NewTaskRepository(db)
	paidTask := func() core.Task {
		now := time.Now().UTC()
		return core.Task{
			ID: uuid.NewString(), OwnerID: c.OrganizationID, OrganizationID: c.OrganizationID, ActorID: customer.ID,
			APIKeyID: &issued.ID, ProviderOwnerID: identity.SharedOwnerID, StorageOwnerID: identity.SharedOwnerID,
			Status: core.TaskQueued, Provider: "bytedance", Model: "local-billing-fixture", MediaType: "image", Prompt: "Fixture",
			CreatedAt: now, UpdatedAt: now, RequireCost: true, Cost: &core.TaskCost{ExpectedUSD: money.USD(money.Scale)},
		}
	}
	if _, _, e = tasks.CreateOrGet(ownerContext, paidTask(), uuid.NewString(), "unfunded-owner-first-generation"); !errors.Is(e, core.ErrBudgetExceeded) {
		t.Fatalf("first owner bypassed empty company balance: %v", e)
	}
	if _, e = repo.AddOrganizationCredit(ctx, admin.ID, c.OrganizationID, org.Credit{
		Amount: money.USD(10 * money.Scale), Kind: "payment", Reason: "Local onboarding fixture", ExternalID: uuid.NewString(),
	}); e != nil {
		t.Fatal(e)
	}
	weekly, remaining, e := tasks.Budget(ownerContext, issued.ID, time.Now().UTC())
	if e != nil || weekly != nil || remaining == nil || *remaining != money.USD(10*money.Scale) {
		budget, _ := json.Marshal(map[string]any{"weekly_limit_usd": weekly, "remaining_usd": remaining})
		t.Fatalf("funded first owner cannot start paid generation: %s error=%v", budget, e)
	}
	_, _, e = tasks.CreateOrGet(ownerContext, paidTask(), uuid.NewString(), "funded-owner-first-generation")
	if e != nil {
		t.Fatalf("funded first owner paid admission: %v", e)
	}
	var memberDefault *money.USD
	if e = db.QueryRow(`SELECT default_weekly_limit_micros FROM organizations WHERE id=$1`, c.OrganizationID).Scan(&memberDefault); e != nil || memberDefault == nil || *memberDefault != 0 {
		t.Fatalf("ordinary member default changed: %v %v", memberDefault, e)
	}
	member, e := people.UpsertGoogleUser(ctx, uuid.NewString(), "member@fixture.test", "Member", "")
	if e != nil {
		t.Fatal(e)
	}
	invitation, e := repo.CreateOrganizationInvite(ctx, customer.ID, c.OrganizationID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.OrganizationInvitation(ctx, member.ID, invitation.Token, true); e != nil {
		t.Fatal(e)
	}
	var memberLimit *money.USD
	if e = db.QueryRow(`SELECT weekly_limit_micros FROM organization_members WHERE organization_id=$1 AND user_id=$2`, c.OrganizationID, member.ID).Scan(&memberLimit); e != nil || memberLimit == nil || *memberLimit != 0 {
		t.Fatalf("ordinary invitation lost default allowance: %v %v", memberLimit, e)
	}
}
