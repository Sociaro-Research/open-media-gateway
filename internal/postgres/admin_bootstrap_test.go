package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func TestHeadlessAdministratorBootstrap(t *testing.T) {
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
	repo := NewSystemRepository(db)
	if _, _, err = repo.InitializeAdministrator(ctx, "invalid", "Test", "Test"); err == nil {
		t.Fatal("invalid email accepted")
	}
	// Failed issuance must also roll back user creation.
	if _, _, err = repo.InitializeAdministrator(ctx, "headless-test@example.test", "Test", ""); err == nil {
		t.Fatal("invalid key name accepted")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM users WHERE email='headless-test@example.test'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial bootstrap", count, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	ids := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			key, secret, e := repo.InitializeAdministrator(ctx, "headless-test@example.test", "Test", "Test")
			if e == nil {
				ids <- key.UserID
				user, credential, a := NewAPIKeyRepository(db).Authenticate(ctx, apikey.Hash(secret))
				if a != nil || user.ID != key.UserID || len(credential.Scopes) != 6 {
					e = errors.New("bootstrap credential rejected")
				}
			}
			results <- e
		})
	}
	wg.Wait()
	close(results)
	success, rejected := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, system.ErrBootstrapped) {
			rejected++
		} else {
			t.Error(e)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("concurrent bootstrap %d/%d", success, rejected)
	}
	close(ids)
	for id := range ids {
		defer db.Exec(`DELETE FROM users WHERE id=$1`, id)
		key, secret, e := repo.IssueAdministratorKey(ctx, id, "Recovery")
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = NewAPIKeyRepository(db).Authenticate(ctx, apikey.Hash(secret)); e != nil {
			t.Fatal(e)
		}
		if e = NewAPIKeyRepository(db).Revoke(ctx, id, key.ID); e != nil {
			t.Fatal(e)
		}
		if _, _, e = NewAPIKeyRepository(db).Authenticate(ctx, apikey.Hash(secret)); !errors.Is(e, apikey.ErrNotFound) {
			t.Fatal("revoked credential accepted", e)
		}
		if _, e = db.Exec(`UPDATE users SET is_system_admin=FALSE WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
		if _, _, e = repo.IssueAdministratorKey(ctx, id, "Forbidden"); !errors.Is(e, system.ErrForbidden) {
			t.Fatal("non-administrator issued key", e)
		}
	}
}
