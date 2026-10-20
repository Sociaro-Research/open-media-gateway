package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"os"
	"sync"
	"testing"
	"time"
)

func TestSharedBudgetAcrossKeysAndOwnerIsolation(t *testing.T) {
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
	owner, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), "shared-budget@example.test", "Shared", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM tasks WHERE owner_id=$1`, owner.ID)
		db.Exec(`DELETE FROM users WHERE id=$1`, owner.ID)
	}()
	defer budgetOrganization(t, db, owner.ID, 0)()
	ctx = identity.WithOrganization(identity.WithUser(ctx, owner), owner.ID, true)
	repo, keys := NewTaskRepository(db), NewAPIKeyRepository(db)
	now := time.Now().UTC()
	createKey := func() apikey.Key {
		t.Helper()
		key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Shared key", OrganizationID: owner.ID, ResourceMode: "shared", StorageMode: "shared"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if err = keys.Create(ctx, owner.ID, key, apikey.Hash(secret)); err != nil {
			t.Fatal(err)
		}
		_, loaded, err := keys.Authenticate(ctx, apikey.Hash(secret))
		if err != nil || loaded.ResourceMode != "shared" || loaded.StorageMode != "shared" {
			t.Fatalf("key mode persistence: %+v %v", loaded, err)
		}
		return key
	}
	a, b := createKey(), createKey()
	request := func(key apikey.Key) (core.Task, error) {
		c := identity.WithAPIKeyID(identity.WithUser(ctx, owner), key.ID)
		c = identity.WithResourceOwners(c, identity.SharedOwnerID, identity.SharedOwnerID)
		task := core.Task{ID: uuid.NewString(), OwnerID: owner.ID, ProviderOwnerID: identity.SharedOwnerID, StorageOwnerID: identity.SharedOwnerID, APIKeyID: &key.ID, Provider: "bytedance", ProviderIdentity: "shared-fingerprint", Model: "seedream-4-5-251128", MediaType: "image", Prompt: "Shared budget", Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now, Cost: &core.TaskCost{ExpectedUSD: money.USD(40000)}}
		saved, _, err := repo.CreateOrGet(c, task, task.ID, task.ID)
		return saved, err
	}
	if _, err = request(a); !errors.Is(err, core.ErrBudgetExceeded) {
		t.Fatalf("default zero budget: %v", err)
	}
	if _, err = db.Exec(`UPDATE organizations SET credit_limit_micros=50000 WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	accepted := make(chan core.Task, 2)
	failed := make(chan error, 2)
	for _, key := range []apikey.Key{a, b} {
		wg.Add(1)
		go func(k apikey.Key) {
			defer wg.Done()
			task, err := request(k)
			if err == nil {
				accepted <- task
			} else if !errors.Is(err, core.ErrBudgetExceeded) {
				failed <- err
			}
		}(key)
	}
	wg.Wait()
	close(accepted)
	close(failed)
	for err := range failed {
		t.Fatal(err)
	}
	if len(accepted) != 2 {
		t.Fatalf("cross-key admissions: %d", len(accepted))
	}
	task := <-accepted
	got, err := repo.Get(identity.WithUser(ctx, owner), task.ID)
	if err != nil || got.ProviderOwnerID != identity.SharedOwnerID || got.StorageOwnerID != identity.SharedOwnerID {
		t.Fatalf("task routing persisted: %+v %v", got, err)
	}
	if _, err = repo.Get(identity.WithOrganization(ctx, uuid.NewString(), false), task.ID); !errors.Is(err, core.ErrTaskNotFound) {
		t.Fatalf("foreign task access: %v", err)
	}
	if err = keys.Revoke(ctx, owner.ID, *task.APIKeyID); err != nil {
		t.Fatal(err)
	}
	c := createKey()
	if _, err = request(c); err != nil {
		t.Fatalf("pending tasks blocked new key: %v", err)
	}
	if err = repo.updateCost(ctx, task.ID, func(t *core.Task) {
		amount := money.USD(55000)
		zero := money.USD(0)
		t.Cost.TariffUSD = &amount
		t.Cost.ActualUSD = &zero
	}); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkFailed(ctx, task.ID, "completed provider work; artifact failed"); err != nil {
		t.Fatal(err)
	}
	_, remaining, err := repo.Budget(identity.WithUser(ctx, owner), c.ID, now)
	if err != nil || remaining == nil || *remaining != -5000 {
		t.Fatalf("overrun / package budget: %v %v", remaining, err)
	}
	if err = repo.SetProviderUsage(ctx, task.ID, *core.NormalizeProviderUsage(map[string]any{"total_tokens": 38800})); err != nil {
		t.Fatal(err)
	}
	report, err := repo.CostReport(identity.WithUser(ctx, owner), core.ReportFilter{AllOrganizations: true, From: now.Add(-time.Minute), Until: now.Add(time.Minute), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range report {
		if row.ID == task.ID {
			found = row.UserID == owner.ID && row.UserEmail == owner.Email && row.Usage != nil && len(row.Usage.Metrics) == 1 && row.Usage.Metrics[0].Quantity == 38800
		}
	}
	if !found {
		t.Fatal("shared report lost consumer identity")
	}
	// System reports include personal-provider tasks too, while an organization
	// report still cannot read another organization's history.
	if _, err = db.Exec(`UPDATE tasks SET provider_owner_id=$2 WHERE id=$1`, task.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	all, err := repo.CostReport(identity.WithUser(ctx, owner), core.ReportFilter{AllOrganizations: true, From: now.Add(-time.Minute), Until: now.Add(time.Minute), KeyID: *task.APIKeyID, Limit: 100})
	if err != nil || len(all) != 1 || all[0].OrganizationID != owner.ID {
		t.Fatalf("personal-provider task missing from system report: %+v %v", all, err)
	}
	foreign, err := repo.CostReport(identity.WithOwnerID(context.Background(), uuid.NewString()), core.ReportFilter{From: now.Add(-time.Minute), Until: now.Add(time.Minute), Limit: 100})
	if err != nil || len(foreign) != 0 {
		t.Fatalf("organization isolation: %+v %v", foreign, err)
	}
}

func TestSharedAdministrationCopiesConnectionsWithoutOverwriting(t *testing.T) {
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
	admin, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), "shared-admin@example.test", "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM provider_settings WHERE owner_id IN ($1,$2)`, admin.ID, identity.SharedOwnerID)
		db.Exec(`DELETE FROM storage_settings WHERE owner_id IN ($1,$2)`, admin.ID, identity.SharedOwnerID)
		db.Exec(`DELETE FROM storage_connections WHERE owner_id IN ($1,$2)`, admin.ID, identity.SharedOwnerID)
		db.Exec(`DELETE FROM users WHERE id=$1`, admin.ID)
	}()
	repo := NewSystemRepository(db)
	if err = repo.ImportShared(ctx, admin.ID); !errors.Is(err, system.ErrForbidden) {
		t.Fatalf("non-admin import: %v", err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	personal := identity.WithUser(ctx, admin)
	shared := identity.WithResourceOwners(personal, identity.SharedOwnerID, identity.SharedOwnerID)
	providers, stores := NewProviderSettingsRepository(db), NewStorageSettingsRepository(db)
	if err = providers.Upsert(personal, providerconfig.Record{ProviderID: "bytedance", Enabled: true, CredentialsCiphertext: []byte("test-opaque-provider-ciphertext"), CredentialSource: "console"}); err != nil {
		t.Fatal(err)
	}
	if err = stores.Save(personal, storage.SettingsRecord{Driver: "s3", ConnectionID: uuid.NewString(), Ciphertext: []byte("test-opaque-storage-ciphertext")}); err != nil {
		t.Fatal(err)
	}
	if err = repo.ImportShared(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	p, err := providers.Get(shared, "bytedance")
	if err != nil || string(p.CredentialsCiphertext) != "test-opaque-provider-ciphertext" {
		t.Fatalf("provider copy: %v", err)
	}
	s, err := stores.Get(shared)
	if err != nil || string(s.Ciphertext) != "test-opaque-storage-ciphertext" {
		t.Fatalf("storage copy: %v", err)
	}
	p.CredentialsCiphertext = []byte("existing-shared-provider")
	if err = providers.Upsert(shared, p); err != nil {
		t.Fatal(err)
	}
	s.Ciphertext = []byte("existing-shared-storage")
	if err = stores.Save(shared, s); err != nil {
		t.Fatal(err)
	}
	if err = repo.ImportShared(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	p, err = providers.Get(shared, "bytedance")
	if err != nil || string(p.CredentialsCiphertext) != "existing-shared-provider" {
		t.Fatal("import overwrote shared provider")
	}
	s, err = stores.Get(shared)
	if err != nil || string(s.Ciphertext) != "existing-shared-storage" {
		t.Fatal("import overwrote shared storage")
	}

}
