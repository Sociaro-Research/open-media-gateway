package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoginMethodsPersistWithoutChangingAccountsOrDelivery(t *testing.T) {
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
	schema := "login_methods_" + uuid.NewString()
	if _, err = base.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
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
	users := NewAuthRepository(db)
	admin, err := users.UpsertGoogleUser(ctx, "subject", "admin@example.test", "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO email_credentials(user_id,password_hash) VALUES($1,'unchanged-password-hash')`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO email_auth_challenges(token_hash,email,purpose,expires_at) VALUES($1,'pending@example.test','verify',NOW()+INTERVAL '1 hour')`, []byte("pending-token")); err != nil {
		t.Fatal(err)
	}
	if err = users.CreateSession(ctx, []byte("session-token"), admin.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	repo := NewSystemRepository(db)
	mail, err := repo.SaveEmailDelivery(ctx, admin.ID, auth.DeliveryRecord{Provider: "resend", From: "sender@example.test", KeyCiphertext: []byte("encrypted-fixture"), Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.BootstrapGoogleLogin(ctx, auth.LoginProviderRecord{ClientID: "original.apps.googleusercontent.com", SecretCiphertext: []byte("encrypted-google-fixture")}); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.LoginProviders(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	initial := rows[1]
	if err != nil || !initial.Enabled {
		t.Fatal(initial, err)
	}
	if _, err = repo.SaveLoginProvider(ctx, "", initial); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("anonymous changed login settings", err)
	}
	var snapshot string
	if err = db.QueryRow(`SELECT jsonb_build_object('users',(SELECT jsonb_agg(to_jsonb(u)) FROM users u),'credentials',(SELECT jsonb_agg(to_jsonb(c)) FROM email_credentials c),'challenges',(SELECT jsonb_agg(to_jsonb(c)) FROM email_auth_challenges c),'sessions',(SELECT jsonb_agg(to_jsonb(s)) FROM user_sessions s))::text`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	next := initial
	next.Enabled = false
	changed, err := repo.SaveLoginProvider(ctx, admin.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = NewSystemRepository(db).LoginProviders(ctx)
	reloaded := rows[1]
	if err != nil || reloaded.Version != changed.Version || reloaded.Enabled {
		t.Fatal(reloaded, err)
	}
	if _, err = repo.SaveLoginProvider(ctx, admin.ID, initial); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale update accepted", err)
	}
	if user, err := users.UserBySession(ctx, []byte("session-token"), time.Now()); err != nil || user.ID != admin.ID {
		t.Fatal("session changed", user, err)
	}
	changed.Enabled = true
	if _, err = repo.SaveLoginProvider(ctx, admin.ID, changed); err != nil {
		t.Fatal(err)
	}
	if err = repo.BootstrapGoogleLogin(ctx, auth.LoginProviderRecord{ClientID: "obsolete", SecretCiphertext: []byte("obsolete")}); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.LoginProviders(ctx)
	if err != nil || rows[1].ClientID != initial.ClientID || string(rows[1].SecretCiphertext) != string(initial.SecretCiphertext) || rows[0].Version != 1 {
		t.Fatal("bootstrap or provider edits overwrote independent settings", rows, err)
	}
	var audit string
	if err = db.QueryRow(`SELECT jsonb_agg(details)::text FROM audit_events WHERE action='login_provider'`).Scan(&audit); err != nil || strings.Contains(audit, "encrypted-google-fixture") || strings.Contains(audit, "SecretCiphertext") {
		t.Fatal("secret in audit", err)
	}
	var after string
	if err = db.QueryRow(`SELECT jsonb_build_object('users',(SELECT jsonb_agg(to_jsonb(u)) FROM users u),'credentials',(SELECT jsonb_agg(to_jsonb(c)) FROM email_credentials c),'challenges',(SELECT jsonb_agg(to_jsonb(c)) FROM email_auth_challenges c),'sessions',(SELECT jsonb_agg(to_jsonb(s)) FROM user_sessions s))::text`).Scan(&after); err != nil || after != snapshot {
		t.Fatal("account state changed", err)
	}
	unchanged, err := repo.EmailDelivery(ctx)
	if err != nil || unchanged.Provider != mail.Provider || unchanged.From != mail.From || unchanged.Version != mail.Version || string(unchanged.KeyCiphertext) != string(mail.KeyCiphertext) {
		t.Fatal("delivery settings changed", err)
	}
	var hash string
	if err = db.QueryRow(`SELECT password_hash FROM email_credentials WHERE user_id=$1`, admin.ID).Scan(&hash); err != nil || hash != "unchanged-password-hash" {
		t.Fatal("password changed", err)
	}
	var challenges, events int
	if err = db.QueryRow(`SELECT COUNT(*) FROM email_auth_challenges`).Scan(&challenges); err != nil || challenges != 1 {
		t.Fatal("pending verification changed", err)
	}
	// The initial credential fixture is audited too, followed by two toggles.
	if err = db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='login_provider'`).Scan(&events); err != nil || events != 3 {
		t.Fatal("missing audit", events, err)
	}
}
