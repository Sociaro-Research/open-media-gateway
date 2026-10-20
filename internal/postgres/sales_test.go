package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSalesHierarchyScopeMovesAndRoleChanges(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	r := NewSystemRepository(db)
	people := NewAuthRepository(db)
	users := map[string]identity.User{}
	for _, name := range []string{"admin", "regionA", "regionB", "leadA", "leadB", "managerA", "managerB", "direct", "standalone", "customer"} {
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "https://profiles.fixture.test/"+name+".png")
		if e != nil {
			t.Fatal(e)
		}
		users[name] = u
	}
	admin := users["admin"].ID
	if e := r.SetAdmin(ctx, "", admin, true, true); e != nil {
		t.Fatal(e)
	}
	place := func(name, role, parent string) {
		t.Helper()
		s, e := r.SalesStructure(ctx, admin)
		if e != nil {
			t.Fatal(e)
		}
		pid := ""
		if parent != "" {
			pid = users[parent].ID
		}
		if _, e = r.PlaceSalesMember(ctx, admin, users[name].ID, system.SalesPlacement{Role: role, SupervisorID: pid, Version: s.Version}); e != nil {
			t.Fatalf("place %s: %v", name, e)
		}
	}
	place("regionA", system.RegionalClientManager, "")
	place("regionB", system.RegionalClientManager, "")
	place("leadA", system.LeadClientManager, "regionA")
	place("leadB", system.LeadClientManager, "")
	place("managerA", system.ClientManager, "leadA")
	place("managerB", system.ClientManager, "leadA")
	place("direct", system.ClientManager, "regionA")
	place("standalone", system.ClientManager, "")
	structure, err := r.SalesStructure(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, employee := range structure.Members {
		if employee.Picture != "https://profiles.fixture.test/"+employee.Name+".png" {
			t.Fatalf("missing profile picture: %+v", employee)
		}
	}

	companies := map[string]string{}
	for _, name := range []string{"leadA", "managerA", "managerB", "direct", "standalone"} {
		o, e := r.CreateOrganization(ctx, admin, name, users["customer"].ID, uuid.NewString()+".fixture.test")
		if e != nil {
			t.Fatal(e)
		}
		if e = r.AssignClientManager(ctx, admin, o.ID, users[name].ID); e != nil {
			t.Fatal(e)
		}
		companies[name] = o.ID
	}
	check := func(name string, want int) {
		t.Helper()
		got, e := r.Organizations(ctx, users[name].ID, false)
		if e != nil || len(got) != want {
			t.Fatalf("%s scope: %d want %d, %v", name, len(got), want, e)
		}
	}
	check("regionA", 4)
	check("regionB", 0)
	check("leadA", 3)
	check("managerA", 1)
	check("standalone", 1)
	if _, e := r.OrganizationAccess(ctx, users["leadB"].ID, companies["managerA"]); !errors.Is(e, org.ErrForbidden) {
		t.Fatalf("foreign client: %v", e)
	}
	if e := r.AssignClientManager(ctx, users["leadA"].ID, companies["managerA"], users["managerB"].ID); e != nil {
		t.Fatal(e)
	}
	check("managerA", 0)
	check("managerB", 2)
	for _, tc := range []struct{ actor, company, target string }{{"managerB", "managerA", "managerA"}, {"leadA", "managerA", "direct"}, {"leadB", "managerA", "leadB"}, {"regionA", "standalone", "direct"}, {"leadA", "managerA", ""}} {
		target := ""
		if tc.target != "" {
			target = users[tc.target].ID
		}
		if e := r.AssignClientManager(ctx, users[tc.actor].ID, companies[tc.company], target); !errors.Is(e, system.ErrForbidden) {
			t.Fatalf("out-of-scope transfer %+v: %v", tc, e)
		}
	}
	if e := r.AssignClientManager(ctx, users["regionA"].ID, companies["direct"], users["managerA"].ID); e != nil {
		t.Fatal(e)
	}
	place("leadA", system.LeadClientManager, "regionB")
	check("regionA", 0)
	check("regionB", 4)
	// A role promotion keeps personal customers and the same user identity.
	place("managerB", system.LeadClientManager, "regionB")
	check("managerB", 2)
	cycleState, _ := r.SalesStructure(ctx, admin)
	if _, e := r.PlaceSalesMember(ctx, admin, users["regionB"].ID, system.SalesPlacement{Role: system.ClientManager, SupervisorID: users["leadA"].ID, Version: cycleState.Version}); !errors.Is(e, system.ErrSalesPlacement) {
		t.Fatalf("demotion into descendant: %v", e)
	}
	// Demoting a lead moves incompatible direct reports to the selected regional parent.
	place("regionB", system.LeadClientManager, "")
	s, e := r.SalesStructure(ctx, admin)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range s.Members {
		if (m.ID == users["leadA"].ID || m.ID == users["managerB"].ID) && m.SupervisorID != "" {
			t.Fatal("lead report not lifted to administrator")
		}
	}
	for _, in := range []system.SalesPlacement{{Role: system.RegionalClientManager, SupervisorID: users["managerA"].ID, Version: s.Version}, {Role: system.LeadClientManager, SupervisorID: users["leadA"].ID, Version: s.Version}, {Role: system.ClientManager, SupervisorID: users["managerA"].ID, Version: s.Version}} {
		if _, e = r.PlaceSalesMember(ctx, admin, users["managerA"].ID, in); !errors.Is(e, system.ErrSalesPlacement) {
			t.Fatalf("invalid placement: %v", e)
		}
	}
	if _, e = r.PlaceSalesMember(ctx, users["leadA"].ID, users["managerA"].ID, system.SalesPlacement{Role: system.ClientManager, Version: s.Version}); !errors.Is(e, system.ErrForbidden) {
		t.Fatalf("nonadmin tree mutation: %v", e)
	}
	if _, e = r.SalesStructure(ctx, users["regionA"].ID); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("tree exposed")
	}
	team, e := r.SalesTeam(ctx, users["leadA"].ID)
	if e != nil || len(team) != 2 {
		t.Fatalf("scoped roster %v %v", team, e)
	}
	if _, e = r.SalesTeam(ctx, users["managerA"].ID); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("ordinary manager roster")
	}
	// Concurrent stale saves cannot both modify the structure.
	s, _ = r.SalesStructure(ctx, admin)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, parent := range []string{users["regionA"].ID, users["regionB"].ID} {
		wg.Add(1)
		go func(parent string) {
			defer wg.Done()
			_, e := r.PlaceSalesMember(ctx, admin, users["standalone"].ID, system.SalesPlacement{Role: system.ClientManager, SupervisorID: parent, Version: s.Version})
			results <- e
		}(parent)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, system.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("concurrent saves: %d/%d", ok, conflict)
	}
	// Removing a sales role repairs reports, removes only that employee's client assignments.
	place("managerA", system.ClientManager, "leadA")
	if e = r.SetUserRole(ctx, admin, users["leadA"].ID, "user"); e != nil {
		t.Fatal(e)
	}
	s, _ = r.SalesStructure(ctx, admin)
	for _, m := range s.Members {
		if m.SupervisorID == users["leadA"].ID {
			t.Fatal("removed supervisor retained")
		}
	}
	if _, e = db.Exec(`UPDATE users SET sales_supervisor_id=id WHERE id=$1`, users["managerA"].ID); e == nil {
		t.Fatal("DB accepted self parent")
	}
	var events int
	if e = db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='sales_structure' AND scope='system'`).Scan(&events); e != nil || events < 8 {
		t.Fatalf("audit events %d %v", events, e)
	}
}

func TestSalesRoleAdmissionPreservesKeysAndMembershipBoundaries(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	r := NewSystemRepository(db)
	people := NewAuthRepository(db)
	newUser := func() identity.User {
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "User", "")
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	admin, owner, employee := newUser(), newUser(), newUser()
	if e := r.SetAdmin(ctx, "", admin.ID, true, true); e != nil {
		t.Fatal(e)
	}
	o, e := r.CreateOrganization(ctx, admin.ID, "Company", owner.ID, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	s, _ := r.SalesStructure(ctx, admin.ID)
	if _, e = r.PlaceSalesMember(ctx, admin.ID, owner.ID, system.SalesPlacement{Role: system.RegionalClientManager, Version: s.Version}); !errors.Is(e, system.ErrRoleConflict) {
		t.Fatalf("owner became sales: %v", e)
	}
	keys := NewAPIKeyRepository(db)
	key := apikey.Key{ID: uuid.NewString(), Name: "Before sales", Prefix: "gw_fixture", Scopes: []string{apikey.Generation}, CreatedAt: time.Now()}
	hash := apikey.Hash("fixture-secret")
	if e = keys.Create(ctx, employee.ID, key, hash); e != nil {
		t.Fatal(e)
	}
	if _, e = r.PlaceSalesMember(ctx, admin.ID, employee.ID, system.SalesPlacement{Role: system.RegionalClientManager, Version: s.Version}); e != nil {
		t.Fatal(e)
	}
	if _, _, e = keys.Authenticate(ctx, hash); !errors.Is(e, apikey.ErrNotFound) {
		t.Fatalf("personal key active: %v", e)
	}
	if e = r.AssignClientManager(ctx, admin.ID, o.ID, employee.ID); e != nil {
		t.Fatal(e)
	}
	if e = r.SetUserRole(ctx, admin.ID, employee.ID, system.LeadClientManager); e != nil {
		t.Fatal(e)
	}
	var manager string
	if e = db.QueryRow(`SELECT client_manager_id FROM organizations WHERE id=$1`, o.ID).Scan(&manager); e != nil || manager != employee.ID {
		t.Fatal("sales promotion/demotion lost own customers", e)
	}
	if e = r.SetClientManager(ctx, admin.ID, employee.ID, true); e != nil {
		t.Fatal(e)
	}
	if role, e := r.SalesRole(ctx, employee.ID); e != nil || role != system.LeadClientManager {
		t.Fatalf("legacy flag reset lead: %s %v", role, e)
	}
	if e = r.SetClientManager(ctx, admin.ID, employee.ID, false); e != nil {
		t.Fatal(e)
	}
	if role, e := r.SalesRole(ctx, employee.ID); e != nil || role != "" {
		t.Fatal("sales removal", role, e)
	}
	if _, _, e = keys.Authenticate(ctx, hash); !errors.Is(e, apikey.ErrNotFound) {
		t.Fatal("revoked key revived")
	}
}

func TestSalesHierarchyMigrationPreservesExistingManagers(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	goose.SetDialect("postgres")
	if e := goose.UpTo(db, "migrations", 54); e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	if _, e := db.Exec(`INSERT INTO users(id,google_subject,email,display_name,is_client_manager) VALUES($1,$2,'legacy@fixture.test','Existing manager',TRUE)`, id, id); e != nil {
		t.Fatal(e)
	}
	if e := Migrate(db); e != nil {
		t.Fatal(e)
	}
	var active bool
	var role string
	var parent *string
	if e := db.QueryRow(`SELECT is_client_manager,sales_role,sales_supervisor_id FROM users WHERE id=$1`, id).Scan(&active, &role, &parent); e != nil || !active || role != system.ClientManager || parent != nil {
		t.Fatalf("migration lost manager: %t %s %v %v", active, role, parent, e)
	}
}

func TestSalesVacancyAdoptsExistingTeamAndIndependentPlacementDoesNot(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	r, auth := NewSystemRepository(db), NewAuthRepository(db)
	users := []identity.User{}
	for _, name := range []string{"owner", "future lead", "manager two", "manager three", "future region", "other region", "foreign manager", "client"} {
		u, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "https://profiles.fixture.test/avatar.png")
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	if err := r.SetAdmin(ctx, "", users[0].ID, true, true); err != nil {
		t.Fatal(err)
	}
	place := func(i int, role, parent string, fill bool) system.SalesStructure {
		t.Helper()
		s, err := r.SalesStructure(ctx, users[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		s, err = r.PlaceSalesMember(ctx, users[0].ID, users[i].ID, system.SalesPlacement{Role: role, SupervisorID: parent, Version: s.Version, FillVacancy: fill})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for i := 1; i <= 4; i++ {
		place(i, system.ClientManager, "", false)
	}
	place(5, system.RegionalClientManager, "", false)
	place(6, system.ClientManager, users[5].ID, false)
	companies := []string{}
	for i := 1; i <= 3; i++ {
		o, err := r.CreateOrganization(ctx, users[0].ID, uuid.NewString(), users[7].ID, uuid.NewString()+".fixture.test")
		if err != nil {
			t.Fatal(err)
		}
		if err = r.AssignClientManager(ctx, users[0].ID, o.ID, users[i].ID); err != nil {
			t.Fatal(err)
		}
		companies = append(companies, o.ID)
	}
	checkParent := func(s system.SalesStructure, i int, parent string) {
		t.Helper()
		for _, m := range s.Members {
			if m.ID == users[i].ID {
				if m.SupervisorID != parent {
					t.Fatalf("%s parent %q, want %q", m.Name, m.SupervisorID, parent)
				}
				return
			}
		}
		t.Fatal("missing member")
	}
	independent := place(1, system.LeadClientManager, "", false)
	checkParent(independent, 2, "")
	checkParent(independent, 3, "")
	// Filling the vacancy must work even when role and supervisor are unchanged.
	filled := place(1, system.LeadClientManager, "", true)
	checkParent(filled, 2, users[1].ID)
	checkParent(filled, 3, users[1].ID)
	checkParent(filled, 4, users[1].ID)
	checkParent(filled, 6, users[5].ID)
	for i, company := range companies {
		var assigned string
		if err := db.QueryRow(`SELECT client_manager_id FROM organizations WHERE id=$1`, company).Scan(&assigned); err != nil || assigned != users[i+1].ID {
			t.Fatalf("client ownership changed: %s %v", assigned, err)
		}
	}
	// A regional vacancy adopts the lower-rank branch and retains its descendants.
	region := place(4, system.RegionalClientManager, "", true)
	checkParent(region, 1, users[4].ID)
	checkParent(region, 2, users[1].ID)
	checkParent(region, 3, users[1].ID)
	checkParent(region, 5, "")
	checkParent(region, 6, users[5].ID)
	if _, err := r.PlaceSalesMember(ctx, users[0].ID, users[1].ID, system.SalesPlacement{Role: system.LeadClientManager, Version: independent.Version, FillVacancy: true}); !errors.Is(err, system.ErrConflict) {
		t.Fatalf("stale vacancy write: %v", err)
	}
	after, err := r.SalesStructure(ctx, users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	checkParent(after, 1, users[4].ID)
	// Pictures are returned in the existing authorized directories, including
	// financial-redacted membership responses; no unscoped lookup is necessary.
	directory, err := r.Users(ctx, "", false, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range directory {
		if u.Picture == "" {
			t.Fatal("missing directory picture")
		}
	}
	bonuses, err := r.SalesBonuses(ctx, users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range bonuses.People {
		if u.Picture == "" {
			t.Fatal("missing bonus picture")
		}
	}
}

// Every source rank can become every destination rank, with and without skipped
// supervisors. Empty-branch placement additionally leaves the former team behind.
func TestSalesPlacementRoleAndTeamMatrix(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	r, auth := NewSystemRepository(db), NewAuthRepository(db)
	newUser := func(t *testing.T, name string) string {
		t.Helper()
		u, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	admin, customer := newUser(t, "admin"), newUser(t, "customer")
	if err := r.SetAdmin(ctx, "", admin, true, true); err != nil {
		t.Fatal(err)
	}
	place := func(t *testing.T, employee, role, parent string, fresh bool) system.SalesStructure {
		t.Helper()
		s, err := r.SalesStructure(ctx, admin)
		if err != nil {
			t.Fatal(err)
		}
		s, err = r.PlaceSalesMember(ctx, admin, employee, system.SalesPlacement{Role: role, SupervisorID: parent, Version: s.Version, NewTeam: fresh})
		if err != nil {
			t.Fatalf("place %s under %s: %v", role, parent, err)
		}
		return s
	}
	roles := []string{system.ClientManager, system.LeadClientManager, system.RegionalClientManager}
	for _, source := range roles {
		for _, destination := range roles {
			for _, parentRole := range append([]string{""}, roles...) {
				if parentRole != "" && system.SalesRank(parentRole) <= system.SalesRank(destination) {
					continue
				}
				for _, fresh := range []bool{false, true} {
					if fresh && destination == system.ClientManager {
						continue
					}
					t.Run(source+"/to_"+destination+"/under_"+parentRole+"/new_"+strconv.FormatBool(fresh), func(t *testing.T) {
						oldParent, target := newUser(t, "old region"), newUser(t, "moving employee")
						place(t, oldParent, system.RegionalClientManager, "", false)
						oldSupervisor := oldParent
						if source == system.RegionalClientManager {
							oldSupervisor = ""
						}
						place(t, target, source, oldSupervisor, false)
						parent, destinationRegion := "", ""
						if parentRole != "" {
							parent = newUser(t, "destination supervisor")
							if parentRole == system.LeadClientManager {
								destinationRegion = newUser(t, "destination region")
								place(t, destinationRegion, system.RegionalClientManager, "", false)
							}
							place(t, parent, parentRole, destinationRegion, false)
						}
						reports := map[string]string{}
						if source != system.ClientManager {
							child := newUser(t, "direct manager")
							place(t, child, system.ClientManager, target, false)
							reports[child] = system.ClientManager
						}
						grandchild, childLead := "", ""
						if source == system.RegionalClientManager {
							childLead = newUser(t, "direct lead")
							place(t, childLead, system.LeadClientManager, target, false)
							reports[childLead] = system.LeadClientManager
							grandchild = newUser(t, "nested manager")
							place(t, grandchild, system.ClientManager, childLead, false)
						}
						company, err := r.CreateOrganization(ctx, admin, uuid.NewString(), customer, uuid.NewString()+".fixture.test")
						if err != nil {
							t.Fatal(err)
						}
						if err = r.AssignClientManager(ctx, admin, company.ID, target); err != nil {
							t.Fatal(err)
						}
						before, err := r.SalesStructure(ctx, admin)
						if err != nil {
							t.Fatal(err)
						}
						after := place(t, target, destination, parent, fresh)
						indexed := map[string]system.SalesMember{}
						for _, m := range after.Members {
							indexed[m.ID] = m
						}
						if got := indexed[target]; got.Role != destination || got.SupervisorID != parent || got.OwnClients != 1 {
							t.Fatalf("wrong placement: %+v", got)
						}
						for employee, role := range reports {
							want := target
							if fresh {
								want = oldSupervisor
							} else if system.SalesRank(role) >= system.SalesRank(destination) {
								want = parent
								if parentRole != "" && system.SalesRank(parentRole) <= system.SalesRank(role) {
									want = destinationRegion
								}
							}
							if got := indexed[employee]; got.SupervisorID != want || got.Role != role {
								t.Fatalf("wrong report repair: %+v, parent want %s", got, want)
							}
						}
						if grandchild != "" && indexed[grandchild].SupervisorID != childLead {
							t.Fatal("nested team was flattened")
						}
						for _, old := range before.Members {
							if old.ID == target {
								continue
							}
							if _, report := reports[old.ID]; report {
								continue
							}
							if indexed[old.ID] != old {
								t.Fatalf("unrelated employee changed: %+v -> %+v", old, indexed[old.ID])
							}
						}
						var assigned string
						if err = db.QueryRow(`SELECT client_manager_id FROM organizations WHERE id=$1`, company.ID).Scan(&assigned); err != nil || assigned != target {
							t.Fatal("own client assignment lost", err)
						}
					})
				}
			}
		}
	}
}

func TestSalesNewTeamAtomicityAndScope(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	r, auth := NewSystemRepository(db), NewAuthRepository(db)
	ids := []string{}
	for _, name := range []string{"admin", "old region", "new region", "lead", "manager"} {
		u, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	if err := r.SetAdmin(ctx, "", ids[0], true, true); err != nil {
		t.Fatal(err)
	}
	place := func(i int, role, parent string, fresh bool) system.SalesStructure {
		t.Helper()
		s, err := r.SalesStructure(ctx, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		s, err = r.PlaceSalesMember(ctx, ids[0], ids[i], system.SalesPlacement{Role: role, SupervisorID: parent, Version: s.Version, NewTeam: fresh})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	place(1, system.RegionalClientManager, "", false)
	place(2, system.RegionalClientManager, "", false)
	place(3, system.LeadClientManager, ids[1], false)
	place(4, system.ClientManager, ids[3], false)
	before, err := r.SalesStructure(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	attempts := []struct {
		actor string
		in    system.SalesPlacement
		want  error
	}{
		{ids[0], system.SalesPlacement{Role: system.LeadClientManager, SupervisorID: ids[2], Version: before.Version - 1, NewTeam: true}, system.ErrConflict},
		{ids[3], system.SalesPlacement{Role: system.LeadClientManager, SupervisorID: ids[2], Version: before.Version, NewTeam: true}, system.ErrForbidden},
		{ids[0], system.SalesPlacement{Role: system.LeadClientManager, SupervisorID: ids[4], Version: before.Version, NewTeam: true}, system.ErrSalesPlacement},
		{ids[0], system.SalesPlacement{Role: system.LeadClientManager, SupervisorID: ids[3], Version: before.Version, NewTeam: true}, system.ErrSalesPlacement},
		{ids[0], system.SalesPlacement{Role: system.LeadClientManager, SupervisorID: ids[2], Version: before.Version, NewTeam: true, FillVacancy: true}, system.ErrSalesPlacement},
		{ids[0], system.SalesPlacement{Role: system.ClientManager, SupervisorID: ids[2], Version: before.Version, NewTeam: true}, system.ErrSalesPlacement},
	}
	for _, attempt := range attempts {
		if _, err := r.PlaceSalesMember(ctx, attempt.actor, ids[3], attempt.in); !errors.Is(err, attempt.want) {
			t.Fatalf("wrong rejection: %v want %v", err, attempt.want)
		}
		after, err := r.SalesStructure(ctx, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("rejected new-team move changed hierarchy/version")
		}
	}
	// Same role and supervisor still performs a meaningful move into an empty team.
	same := place(3, system.LeadClientManager, ids[1], true)
	if same.Version <= before.Version {
		t.Fatal("same-parent empty-team move did not advance version")
	}
	for _, m := range same.Members {
		if m.ID == ids[4] && m.SupervisorID != ids[1] {
			t.Fatal("former manager did not stay under old region")
		}
	}
	place(4, system.ClientManager, ids[3], false)
	moved := place(3, system.LeadClientManager, ids[2], true)
	for _, m := range moved.Members {
		if m.ID == ids[4] && m.SupervisorID != ids[1] {
			t.Fatal("former team moved to destination region")
		}
	}
}
