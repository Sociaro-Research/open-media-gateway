package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/limits"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestLimitsPersistenceAndConcurrentKeyCount(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "limits_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec("DROP SCHEMA " + schema + " CASCADE")
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthRepository(db)
	admin, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "admin@example.test", "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	member, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "member@example.test", "Member", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	sys := NewSystemRepository(db)
	org, err := sys.CreateOrganization(ctx, admin.ID, "Quota test", member.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := sys.LimitSettings(ctx, admin.ID, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sys.LimitSettings(ctx, member.ID, org.ID); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("org admin read limits", err)
	}
	scoped := identity.WithAPIKeyID(ctx, uuid.NewString())
	scoped = identity.WithSystemPermission(scoped, false)
	if _, err = sys.SaveLimits(scoped, admin.ID, org.ID, p.Organization); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("unscoped key changed limits", err)
	}
	n := 3
	p.Organization.Values["user_active_keys"] = &n
	if _, err = sys.SaveLimits(ctx, member.ID, org.ID, p.Organization); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("org admin changed limits", err)
	}
	updated, err := sys.SaveLimits(ctx, admin.ID, org.ID, p.Organization)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sys.SaveLimits(ctx, admin.ID, org.ID, p.Organization); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale write allowed", err)
	}
	if updated.Effective["user_active_keys"] != 3 || updated.Effective["key_reads_minute"] != 1200 {
		t.Fatal(updated)
	}
	repo := NewAPIKeyRepository(db)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, secret, e := apikey.Generate(apikey.CreateInput{Name: "Quota", OrganizationID: org.ID}, time.Now())
			if e == nil {
				e = repo.Create(ctx, member.ID, key, apikey.Hash(secret))
			}
			if e == nil {
				accepted.Add(1)
			} else if !errors.Is(e, limits.ErrActiveKeys) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 3 {
		t.Fatalf("concurrent creation exceeded quota: %d", accepted.Load())
	}
	keys, err := repo.List(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Revoke(ctx, member.ID, keys[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE user_api_keys SET expires_at=NOW()-interval '1 second' WHERE id=$1`, keys[1].ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		key, secret, e := apikey.Generate(apikey.CreateInput{Name: "Replacement", OrganizationID: org.ID}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		if e = repo.Create(ctx, member.ID, key, apikey.Hash(secret)); e != nil {
			t.Fatal("expired/revoked count toward cap", e)
		}
	}
	updated.Organization.Values["user_active_keys"] = nil
	if _, err = sys.SaveLimits(ctx, admin.ID, org.ID, updated.Organization); err != nil {
		t.Fatal(err)
	}
	changed := updated.System
	n = 7
	changed.Values["user_active_keys"] = &n
	if _, err = sys.SaveLimits(ctx, admin.ID, "", changed); err != nil {
		t.Fatal(err)
	}
	p, err = sys.RequestLimits(ctx, org.ID)
	if err != nil || p.Effective["user_active_keys"] != 7 {
		t.Fatal("inheritance failed", p, err)
	}
}
