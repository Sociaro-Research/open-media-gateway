package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
)

func TestClientManagerAssignmentsAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := auditDatabase(t, true)
	var err error
	repo := NewSystemRepository(db)
	auth := NewAuthRepository(db)
	users := []identity.User{}
	for _, name := range []string{"System", "Manager", "Other manager", "Org admin"} {
		u, e := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		users = append(users, u)
	}
	admin, manager, other, orgAdmin := users[0].ID, users[1].ID, users[2].ID, users[3].ID
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, name := range []string{"First", "Second", "Unassigned"} {
		o, e := repo.CreateOrganization(ctx, admin, name, orgAdmin, uuid.NewString()+".fixture.test")
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, o.ID)
	}
	if err = repo.SetClientManager(ctx, admin, admin, true); !errors.Is(err, system.ErrRoleConflict) {
		t.Fatalf("system admin conflict: %v", err)
	}
	if err = repo.SetClientManager(ctx, admin, orgAdmin, true); !errors.Is(err, system.ErrRoleConflict) {
		t.Fatalf("org admin conflict: %v", err)
	}
	if err = repo.SetClientManager(ctx, orgAdmin, manager, true); !errors.Is(err, system.ErrForbidden) {
		t.Fatalf("org admin granted global role: %v", err)
	}
	if err = repo.SetOrganizationMember(ctx, admin, ids[2], manager, "developer", nil, true, false); err != nil {
		t.Fatal(err)
	}
	keys := NewAPIKeyRepository(db)
	key := apikey.Key{ID: uuid.NewString(), Name: "Existing", Prefix: "gw_fixture", Scopes: []string{apikey.Generation}, CreatedAt: time.Now(), OrganizationID: ids[2]}
	hash := apikey.Hash("fixture-secret")
	if err = keys.Create(ctx, manager, key, hash); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{manager, other} {
		if err = repo.SetUserRole(ctx, admin, id, "client_manager"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = keys.Authenticate(ctx, hash); !errors.Is(err, apikey.ErrNotFound) {
		t.Fatalf("old key active: %v", err)
	}
	key.ID = uuid.NewString()
	if err = keys.Create(ctx, manager, key, apikey.Hash("new")); !errors.Is(err, apikey.ErrForbidden) {
		t.Fatalf("created manager key: %v", err)
	}
	for _, id := range ids[:2] {
		if err = repo.AssignClientManager(ctx, admin, id, users[1].Email); err != nil {
			t.Fatal(err)
		}
	}
	items, err := repo.Organizations(ctx, manager, false)
	if err != nil || len(items) != 2 {
		t.Fatalf("assigned list %v %v", items, err)
	}
	for _, o := range items {
		if o.Role != "client_manager" || o.ClientManagerID == nil || *o.ClientManagerID != manager {
			t.Fatalf("wrong role %+v", o)
		}
	}
	if _, err = repo.OrganizationAccess(ctx, manager, ids[2]); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("old membership bypassed assignment: %v", err)
	}
	from := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	until := from.Add(24 * time.Hour)
	for _, task := range []struct {
		owner, provider, status, cost string
		at                            time.Time
	}{
		{ids[0], identity.SharedOwnerID, "succeeded", `{"tariff_usd":2,"markup_percent":25}`, from},
		{ids[0], orgAdmin, "succeeded", `{"metered_usd":3}`, from.Add(time.Hour)},
		{ids[0], identity.SharedOwnerID, "succeeded", `{"tariff_usd":4,"actual_usd":0,"package_units":1}`, from.Add(2 * time.Hour)},
		{ids[0], identity.SharedOwnerID, "failed", `{"tariff_usd":10,"actual_source":"provider_failed_no_charge"}`, from.Add(3 * time.Hour)},
		{ids[0], identity.SharedOwnerID, "succeeded", "null", from.Add(4 * time.Hour)},
		{ids[0], identity.SharedOwnerID, "running", `{"tariff_usd":20}`, from.Add(5 * time.Hour)},
		{ids[0], identity.SharedOwnerID, "succeeded", `{"tariff_usd":99}`, until},
		{ids[0], identity.SharedOwnerID, "succeeded", `{"tariff_usd":20,"actual_source":"free_provider"}`, until.Add(time.Hour)},
		{ids[2], identity.SharedOwnerID, "succeeded", `{"tariff_usd":999}`, from},
	} {
		if _, err = db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,prompt,cost,created_at) VALUES($1,$2,$3,$4,$2,$5,'mock','fixture','private content',$6::jsonb,$7)`, uuid.NewString(), task.owner, orgAdmin, task.provider, task.status, task.cost, task.at); err != nil {
			t.Fatal(err)
		}
	}
	items, err = repo.OrganizationsWithUsage(ctx, manager, true, from, until)
	if err != nil || len(items) != 2 {
		t.Fatalf("usage scope: %+v %v", items, err)
	}
	for _, o := range items {
		if o.ID == ids[0] {
			if o.Usage == nil || o.Usage.Spend != money.USD(9500000) || o.Usage.UnknownCosts != 1 || o.Usage.LastActivity == nil || !o.Usage.LastActivity.Equal(until.Add(time.Hour)) {
				t.Fatalf("customer charges, unknown costs and free activity: %+v", o.Usage)
			}
		} else if o.Usage == nil || o.Usage.Spend != 0 || o.Usage.LastActivity != nil {
			t.Fatalf("unused company: %+v", o.Usage)
		}
	}
	rows, err := NewTaskRepository(db).CostReport(identity.WithOwnerID(ctx, ids[0]), core.ReportFilter{From: from, Until: until, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var reportSpend money.USD
	for _, row := range rows {
		if row.ConsumedUSD != nil {
			reportSpend += *row.ConsumedUSD
		}
	}
	if reportSpend != money.USD(9500000) {
		t.Fatalf("list differs from report: %v", reportSpend)
	}
	if _, err = repo.OrganizationMembers(ctx, manager, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.OrganizationCredits(ctx, manager, ids[0], 0); err != nil {
		t.Fatal(err)
	}
	if err = repo.OrganizationSettings(ctx, manager, ids[0], "Forbidden", 0, nil, false); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("manager changed settings: %v", err)
	}
	if _, err = repo.CreateOrganizationInvite(ctx, manager, ids[0]); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("manager created invitation: %v", err)
	}
	if err = repo.AssignClientManager(ctx, orgAdmin, ids[0], other); !errors.Is(err, system.ErrForbidden) {
		t.Fatalf("org admin reassigned manager: %v", err)
	}
	if err = repo.AssignClientManager(ctx, admin, ids[0], other); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.OrganizationAccess(ctx, manager, ids[0]); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("previous manager retained access: %v", err)
	}
	items, err = repo.OrganizationsWithUsage(ctx, manager, false, from, until)
	if err != nil || len(items) != 1 || items[0].ID != ids[1] {
		t.Fatalf("summary retained reassigned company: %+v %v", items, err)
	}
	if err = repo.SetAdmin(ctx, admin, manager, true, false); !errors.Is(err, system.ErrRoleConflict) {
		t.Fatalf("manager promoted without removing role: %v", err)
	}
	if err = repo.SetUserRole(ctx, admin, manager, "system_admin"); err != nil {
		t.Fatal(err)
	}
	items, err = repo.Organizations(ctx, admin, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range items {
		if o.ClientManagerID != nil && *o.ClientManagerID == manager {
			t.Fatal("assignment survived role removal")
		}
	}
	if _, _, err = keys.Authenticate(ctx, hash); !errors.Is(err, apikey.ErrNotFound) {
		t.Fatal("key resurrected after role removal")
	}
	if err = repo.SetUserRole(ctx, admin, manager, "user"); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetUserRole(ctx, admin, admin, "user"); !errors.Is(err, system.ErrLastAdmin) {
		t.Fatalf("last admin direct transition: %v", err)
	}
	if err = repo.AssignClientManager(ctx, admin, ids[0], ""); err != nil {
		t.Fatal(err)
	}
	if items, err = repo.Organizations(ctx, other, false); err != nil || len(items) != 0 {
		t.Fatalf("cleared assignment %v %v", items, err)
	}
}
