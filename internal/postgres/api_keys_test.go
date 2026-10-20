package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/google/uuid"
)

func TestKeyUsagePeriodUsesLocalMonthAcrossDST(t *testing.T) {
	for _, test := range []struct{ zone, month, from, until string }{
		{"Asia/Nicosia", "2026-03", "2026-02-28T22:00:00Z", "2026-03-31T21:00:00Z"},
		{"America/New_York", "2026-11", "2026-11-01T04:00:00Z", "2026-12-01T05:00:00Z"},
		{"UTC", "2026-09", "2026-09-01T00:00:00Z", "2026-10-01T00:00:00Z"},
	} {
		t.Run(test.zone+"/"+test.month, func(t *testing.T) {
			location, err := time.LoadLocation(test.zone)
			if err != nil {
				t.Fatal(err)
			}
			month, err := time.ParseInLocation("2006-01", test.month, location)
			if err != nil {
				t.Fatal(err)
			}
			from, until := keyUsagePeriod(month)
			if from.UTC().Format(time.RFC3339) != test.from || until.UTC().Format(time.RFC3339) != test.until {
				t.Fatalf("wrong calendar boundaries: %v to %v", from, until)
			}
		})
	}
}

// Run only against a dedicated, disposable database. Applies all migrations.
func TestPersonalAPIKeyPersistence(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to an isolated test database")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	authRepo := NewAuthRepository(db)
	alice, err := authRepo.UpsertGoogleUser(ctx, "test-alice-"+uuid.NewString(), "alice@example.test", "Alice", "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := authRepo.UpsertGoogleUser(ctx, "test-bob-"+uuid.NewString(), "bob@example.test", "Bob", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tasks WHERE owner_id IN ($1,$2)`, alice.ID, bob.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE id IN ($1,$2)`, alice.ID, bob.ID)
	})
	repo := NewAPIKeyRepository(db)
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Test integration"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, alice.ID, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	user, credential, err := repo.Authenticate(ctx, apikey.Hash(secret))
	if err != nil || user.ID != alice.ID || credential.ID != key.ID {
		t.Fatalf("authentication: %v", err)
	}
	keys, err := repo.List(ctx, bob.ID)
	if err != nil || len(keys) != 0 {
		t.Fatal("cross-account key list")
	}
	keys, err = repo.List(ctx, alice.ID)
	if err != nil || len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatal("key metadata or usage missing")
	}
	if err := repo.Revoke(ctx, bob.ID, key.ID); !errors.Is(err, apikey.ErrNotFound) {
		t.Fatal("cross-account revoke")
	}
	if _, _, err := repo.Authenticate(ctx, apikey.Hash("wrong")); !errors.Is(err, apikey.ErrNotFound) {
		t.Fatal("invalid key accepted")
	}
	// Persist a task and verify attribution, shared owner visibility and idempotent replay.
	tasks := NewTaskRepository(db)
	aliceCtx := identity.WithAPIKeyID(identity.WithUser(ctx, alice), key.ID)
	task := core.Task{ID: uuid.NewString(), OwnerID: alice.ID, APIKeyID: identity.APIKeyID(aliceCtx), Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Integration test", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	stored, created, err := tasks.CreateOrGet(aliceCtx, task, "same-request", "same-hash")
	if err != nil || !created {
		t.Fatalf("task create: %v", err)
	}
	replay, created, err := tasks.CreateOrGet(identity.WithUser(ctx, alice), task, "same-request", "same-hash")
	if err != nil || created || replay.ID != stored.ID || replay.APIKeyID == nil || *replay.APIKeyID != key.ID {
		t.Fatalf("idempotent attribution lost: %v", err)
	}
	fetched, err := tasks.Get(identity.WithUser(ctx, alice), task.ID)
	if err != nil || fetched.APIKeyID == nil || *fetched.APIKeyID != key.ID {
		t.Fatalf("session cannot read attributed task: %v", err)
	}
	if _, err := tasks.Get(identity.WithUser(ctx, bob), task.ID); !errors.Is(err, core.ErrTaskNotFound) {
		t.Fatal("cross-account task read")
	}
	otherTasks, err := tasks.ListRecent(identity.WithUser(ctx, bob), 50)
	if err != nil || len(otherTasks) != 0 {
		t.Fatal("cross-account task list")
	}
	// Provider connections also use the same owner, irrespective of credential.
	settings := NewProviderSettingsRepository(db)
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_settings(owner_id,provider_id,enabled,credential_source,created_at,updated_at) VALUES ($1,'mock',true,'none',NOW(),NOW())`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Get(aliceCtx, "mock"); err != nil {
		t.Fatal("key cannot read own provider settings")
	}
	if _, err := settings.Get(identity.WithUser(ctx, bob), "mock"); err == nil {
		t.Fatal("key reads another owner’s provider settings")
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM provider_settings WHERE owner_id=$1`, alice.ID)
	})
	if _, err := db.ExecContext(ctx, `UPDATE user_api_keys SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Authenticate(ctx, apikey.Hash(secret)); !errors.Is(err, apikey.ErrNotFound) {
		t.Fatal("expired key accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user_api_keys SET expires_at=NULL WHERE id=$1`, key.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := repo.Revoke(ctx, alice.ID, key.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := repo.Authenticate(ctx, apikey.Hash(secret)); !errors.Is(err, apikey.ErrNotFound) {
		t.Fatal("revoked key accepted")
	}
	if _, err := tasks.Get(identity.WithUser(ctx, alice), task.ID); err != nil {
		t.Fatal("revoking key removed task")
	}
	var hash []byte
	var revoked sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT token_hash,revoked_at FROM user_api_keys WHERE id=$1`, key.ID).Scan(&hash, &revoked); err != nil || len(hash) != 32 || !revoked.Valid {
		t.Fatal("invalid stored credential")
	}
}

func TestAPIKeyMonthlyUsage(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to an isolated test database")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	owner, err := NewAuthRepository(db).UpsertGoogleUser(ctx, "usage-"+uuid.NewString(), "usage@example.test", "Usage", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.ExecContext(ctx, `DELETE FROM tasks WHERE owner_id=$1`, owner.ID)
		db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, owner.ID)
	}()
	repo := NewAPIKeyRepository(db)
	key, secret, _ := apikey.Generate(apikey.CreateInput{Name: "Monthly usage"}, time.Now())
	if err := repo.Create(ctx, owner.ID, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		at                      time.Time
		provider, status, usage string
		attributed              bool
	}{
		{start.Add(-time.Second), "bytedance", "succeeded", `{"estimated_cost":99,"currency":"USD"}`, true},
		{start, "bytedance", "succeeded", `{"estimated_cost":0.12,"currency":"USD"}`, true},
		{start.Add(time.Hour), "bytedance", "succeeded", `{"estimated_cost":0.34,"currency":"USD"}`, true},
		{start.Add(time.Hour), "mock", "succeeded", `{}`, true},
		{start.Add(time.Hour), "alibaba", "succeeded", `{}`, true},
		{start.Add(time.Hour), "alibaba", "failed", `{"estimated_cost":5,"currency":"CNY"}`, true},
		{start.Add(time.Hour), "alibaba", "running", `{}`, true},
		{start.Add(time.Hour), "bytedance", "succeeded", `{"estimated_cost":88,"currency":"USD"}`, false},
		{start.AddDate(0, 1, 0), "bytedance", "succeeded", `{"estimated_cost":77,"currency":"USD"}`, true},
	} {
		var keyID *string
		if item.attributed {
			keyID = &key.ID
		}
		var cost any
		if item.provider == "mock" {
			cost = `{"expected_usd":0,"actual_usd":0,"actual_source":"free_provider","status":"confirmed"}`
		}
		_, err := db.ExecContext(ctx, `INSERT INTO tasks (provider_owner_id,storage_owner_id,id,owner_id,api_key_id,status,provider,model,media_type,prompt,request_options,provider_usage,created_at,updated_at,cost)
		VALUES ($2,$2,$1,$2,$3,$4,$5,'test','video','Usage test','{}',$6,$7,$7,$8)`, uuid.NewString(), owner.ID, keyID, item.status, item.provider, item.usage, item.at, cost)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Revoke(ctx, owner.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	usage, err := repo.Usage(ctx, owner.ID, start)
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage: %v %v", usage, err)
	}
	got := usage[0]
	if got.KeyID != key.ID || got.Month != "2026-09" || got.EstimatedCostUSD != 0.46 || got.Tasks != 6 || got.PendingTasks != 1 || got.UnknownCostTasks != 2 {
		t.Fatalf("wrong monthly attribution: %+v", got)
	}
	other, err := repo.Usage(ctx, uuid.NewString(), start)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-account usage: %v %v", other, err)
	}
}
