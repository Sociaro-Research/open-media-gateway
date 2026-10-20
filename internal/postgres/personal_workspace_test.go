package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

func TestPersonalWorkspaceIsolationAndBudgetWithoutMembership(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	user, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Personal tester", "")
	if err != nil {
		t.Fatal(err)
	}
	personal := identity.WithPersonalData(identity.WithUser(ctx, user))
	personal = identity.WithResourceOwners(personal, user.ID, user.ID)
	// Legacy installations created an organization using the same UUID as the user.
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Legacy organization')`, user.ID); err != nil {
		t.Fatal(err)
	}
	orgCtx := identity.WithOrganization(identity.WithUser(ctx, user), user.ID, true)
	defer func() {
		for _, owner := range []string{user.ID, identity.OwnerID(personal)} {
			db.Exec(`DELETE FROM reference_images WHERE owner_id=$1`, owner)
			db.Exec(`DELETE FROM tasks WHERE owner_id=$1`, owner)
		}
		db.Exec(`DELETE FROM user_api_keys WHERE user_id=$1`, user.ID)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, user.ID)
		db.Exec(`DELETE FROM users WHERE id=$1`, user.ID)
	}()
	keys := NewAPIKeyRepository(db)
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Personal"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Create(ctx, user.ID, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	if _, loaded, err := keys.Authenticate(ctx, apikey.Hash(secret)); err != nil || loaded.OrganizationID != "" {
		t.Fatalf("personal auth: %+v %v", loaded, err)
	}
	tasks := NewTaskRepository(db)
	_, remaining, err := tasks.Budget(personal, key.ID, time.Now())
	if err != nil || remaining != nil {
		t.Fatalf("personal budget requires organization: %v %v", remaining, err)
	}
	makeTask := func(c context.Context, keyID *string) core.Task {
		now := time.Now().UTC()
		task := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(c), ActorID: user.ID, APIKeyID: keyID, ProviderOwnerID: user.ID, StorageOwnerID: user.ID, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Isolation", Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now, Cost: &core.TaskCost{ExpectedUSD: money.USD(100)}}
		stored, _, e := tasks.CreateOrGet(c, task, "", "test")
		if e != nil {
			t.Fatal(e)
		}
		return stored
	}
	privateTask := makeTask(personal, &key.ID)
	organizationTask := makeTask(orgCtx, nil)
	if _, e := tasks.Get(orgCtx, privateTask.ID); !errors.Is(e, core.ErrTaskNotFound) {
		t.Fatal("organization saw private task", e)
	}
	if _, e := tasks.Get(personal, organizationTask.ID); !errors.Is(e, core.ErrTaskNotFound) {
		t.Fatal("personal key saw organization task", e)
	}
	other := identity.WithPersonalData(identity.WithOwnerID(ctx, uuid.NewString()))
	if _, e := tasks.Get(other, privateTask.ID); !errors.Is(e, core.ErrTaskNotFound) {
		t.Fatal("other user saw private task", e)
	}
	refs := NewReferenceRepository(db)
	ref := references.Image{ID: uuid.NewString(), Name: "Private", ContentType: "image/png", SizeBytes: 10, StorageKey: identity.OwnerID(personal) + "/references/test/image.png", CreatedAt: time.Now()}
	if err = refs.Insert(personal, ref); err != nil {
		t.Fatal(err)
	}
	if _, err = refs.Get(orgCtx, ref.ID); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("organization saw private reference", err)
	}
	if _, err = refs.Get(personal, ref.ID); err != nil {
		t.Fatal(err)
	}
	filter := core.ReportFilter{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), Limit: 50}
	report, err := tasks.CostReport(personal, filter)
	if err != nil || len(report) != 1 || report[0].ID != privateTask.ID {
		t.Fatalf("personal report: %+v %v", report, err)
	}
	filter.AllOrganizations = true
	report, err = tasks.CostReport(orgCtx, filter)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range report {
		if r.ID == privateTask.ID {
			t.Fatal("cross-organization report exposed personal task")
		}
	}
}
