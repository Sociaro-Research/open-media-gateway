package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestTablePreferencesMigrationConcurrentPersistenceAndProjection(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 96); err != nil {
		t.Fatal(err)
	}
	id, other := uuid.NewString(), uuid.NewString()
	ctx := identity.WithOwnerID(context.Background(), id)
	for _, user := range []string{id, other} {
		if _, err := db.Exec(`INSERT INTO users(id,google_subject,email,display_name,custom_display_name,theme,report_columns,contacts) VALUES($1,$2,$3,'Legacy custom',true,'dark','["date","user","estimated_cost"]','[{"label":"Support","url":"mailto:support@example.test"}]')`, user, user, user+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewAuthRepository(db)
	yes := true
	var wg sync.WaitGroup
	for table, columns := range auth.TableColumnIDs {
		if table == "requests" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.UpdateProfile(ctx, id, auth.ProfileUpdate{TableColumns: &map[string][]string{table: columns[:1]}}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := repo.UpdateProfile(ctx, id, auth.ProfileUpdate{TableWide: &map[string]*bool{"organizations": &yes, "requests": &yes}}); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		theme := "light"
		contacts := []identity.Contact{{Label: "New", URL: "https://example.test"}}
		if _, err := repo.UpdateProfile(ctx, id, auth.ProfileUpdate{Theme: &theme, Contacts: &contacts}); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	token := sha256.Sum256([]byte(uuid.NewString()))
	if err := repo.CreateSession(ctx, token[:], id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	user, err := NewAuthRepository(db).UserBySession(ctx, token[:], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(user.TableColumns) != len(auth.TableColumnIDs)-1 || !user.TableWide["organizations"] || !user.TableWide["requests"] || user.Theme != "light" || len(user.Contacts) != 1 || user.Contacts[0].Label != "New" || !reflect.DeepEqual(user.ReportColumns, []string{"date", "user", "estimated_cost"}) {
		t.Fatalf("lost concurrent/profile/legacy update: %+v", user)
	}
	login, err := repo.UpsertGoogleUser(ctx, id, id+"@example.test", "Google name", "")
	if err != nil || !reflect.DeepEqual(login.TableColumns, user.TableColumns) || login.Name != "Legacy custom" {
		t.Fatal("login lost preferences", login, err)
	}
	remote, err := NewCompanyInterfaceAuthority(db).User(ctx, id)
	if err != nil || !reflect.DeepEqual(remote.TableColumns, user.TableColumns) || !reflect.DeepEqual(remote.TableWide, user.TableWide) {
		t.Fatal("BrowserActor projection lost preferences", remote, err)
	}
	foreign, err := NewCompanyInterfaceAuthority(db).User(ctx, other)
	if err != nil || len(foreign.TableColumns) != 0 || len(foreign.TableWide) != 0 {
		t.Fatal("cross-user preference disclosure", foreign, err)
	}
	for _, project := range []func(any) ([]byte, error){core.CustomerJSON, core.SalesJSON} {
		raw, err := project(map[string]any{"user": user, "estimated_cost": "HIDDEN"})
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			User      identity.User `json:"user"`
			Estimated any           `json:"estimated_cost"`
		}
		if err = json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(got.User.ReportColumns, user.ReportColumns) || !reflect.DeepEqual(got.User.TableColumns, user.TableColumns) || !reflect.DeepEqual(got.User.TableWide, user.TableWide) || got.Estimated != nil {
			t.Fatalf("preference redaction/parity: %s %v", raw, err)
		}
	}
	if _, err = repo.UpdateProfile(ctx, id, auth.ProfileUpdate{TableColumns: &map[string][]string{"organizations": nil}}); err != nil {
		t.Fatal(err)
	}
	user, err = repo.UserBySession(ctx, token[:], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := user.TableColumns["organizations"]; exists || len(user.TableColumns) != len(auth.TableColumnIDs)-2 || !user.TableWide["organizations"] {
		t.Fatal("reset overwrote other preferences", user)
	}
	if err = goose.DownTo(db, "migrations", 96); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	after, err := repo.UserBySession(ctx, token[:], time.Now())
	if err != nil || !reflect.DeepEqual(after.TableColumns, user.TableColumns) {
		t.Fatal("re-upgrade lost preferences", after, err)
	}
	if _, err = db.Exec(`CREATE FUNCTION reject_table_profile_audit() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='profile' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_table_profile_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_table_profile_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.UpdateProfile(ctx, id, auth.ProfileUpdate{TableColumns: &map[string][]string{"organizations": {"balance"}}}); err == nil {
		t.Fatal("preference update survived audit failure")
	}
	after, err = repo.UserBySession(ctx, token[:], time.Now())
	if err != nil || !reflect.DeepEqual(after.TableColumns, user.TableColumns) {
		t.Fatal("audit rollback lost", after, err)
	}
}
