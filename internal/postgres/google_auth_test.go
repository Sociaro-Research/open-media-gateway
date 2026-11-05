package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/google/uuid"
)

func googleAuthDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func emailOnlyGoogleFixture(t *testing.T, db *sql.DB, email string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO users(id,email,display_name,is_system_admin,custom_display_name,theme)
		VALUES($1,$2,'Original owner',TRUE,TRUE,'dark')`, id, email); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM users WHERE id=$1`, id); err != nil {
			t.Error(err)
		}
	})
	return id
}

func TestGoogleLinksAuthoritativeEmailWithoutReplacingAccount(t *testing.T) {
	db := googleAuthDatabase(t)
	ctx := context.Background()
	repo := NewAuthRepository(db)
	for _, domain := range []string{"gmail.com", "company.example"} {
		t.Run(domain, func(t *testing.T) {
			email := uuid.NewString() + "@" + domain
			id := emailOnlyGoogleFixture(t, db, strings.ToUpper(email))
			if _, err := db.Exec(`INSERT INTO email_credentials(user_id,password_hash) VALUES($1,'retained-password-hash')`, id); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256([]byte(uuid.NewString()))
			if err := repo.CreateSession(ctx, hash[:], id, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			var before string
			if err := db.QueryRow(`SELECT (to_jsonb(u)-'google_subject'-'updated_at')::text FROM users u WHERE id=$1`, id).Scan(&before); err != nil {
				t.Fatal(err)
			}
			google := auth.GoogleIdentity{Subject: uuid.NewString(), Email: " " + email + " ", EmailVerified: true, Name: "Google name", Picture: "https://fixture.example/avatar"}
			if domain != "gmail.com" {
				google.HostedDomain = domain
			}
			user, err := repo.SignInGoogleUser(ctx, google)
			if err != nil || user.ID != id || user.Name != "Original owner" {
				t.Fatalf("existing owner was not reused: user=%+v err=%v", user, err)
			}
			var after, subject, password string
			if err = db.QueryRow(`SELECT (to_jsonb(u)-'google_subject'-'updated_at')::text,google_subject FROM users u WHERE id=$1`, id).Scan(&after, &subject); err != nil || before != after || subject != google.Subject {
				t.Fatal("link replaced account data or failed to persist subject", err)
			}
			if err = db.QueryRow(`SELECT password_hash FROM email_credentials WHERE user_id=$1`, id).Scan(&password); err != nil || password != "retained-password-hash" {
				t.Fatal("link replaced email credentials", err)
			}
			if session, err := repo.UserBySession(ctx, hash[:], time.Now()); err != nil || session.ID != id {
				t.Fatal("link replaced an existing session", err)
			}
			if again, err := repo.SignInGoogleUser(ctx, google); err != nil || again.ID != id || again.Name != "Original owner" {
				t.Fatal("repeat Google login lost original identity/profile", err)
			}
		})
	}
}

func TestGoogleEmailLinkRejectsUntrustedClaimsAndOtherSubjects(t *testing.T) {
	db := googleAuthDatabase(t)
	ctx := context.Background()
	repo := NewAuthRepository(db)
	email := uuid.NewString() + "@company.example"
	id := emailOnlyGoogleFixture(t, db, email)
	google := auth.GoogleIdentity{Subject: uuid.NewString(), Email: email, Name: "Other identity", EmailVerified: true}
	if _, err := repo.SignInGoogleUser(ctx, google); !errors.Is(err, auth.ErrGoogleAccountConflict) {
		t.Fatal("verified third-party address linked without authoritative proof", err)
	}
	google.HostedDomain = "company.example"
	google.EmailVerified = false
	if _, err := repo.SignInGoogleUser(ctx, google); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("unverified claims accepted", err)
	}
	google.EmailVerified = true
	if _, err := repo.SignInGoogleUser(ctx, google); err != nil {
		t.Fatal(err)
	}
	original := google.Subject
	google.Subject = uuid.NewString()
	if _, err := repo.SignInGoogleUser(ctx, google); !errors.Is(err, auth.ErrGoogleAccountConflict) {
		t.Fatal("different Google subject replaced an existing binding", err)
	}
	var subject string
	if err := db.QueryRow(`SELECT google_subject FROM users WHERE id=$1`, id).Scan(&subject); err != nil || subject != original {
		t.Fatal("failed login changed the existing binding", err)
	}
	// A subject already owning a different profile must never acquire this owner.
	other, err := repo.UpsertGoogleUser(ctx, google.Subject, uuid.NewString()+"@example.test", "Other user", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, other.ID) })
	if _, err = repo.SignInGoogleUser(ctx, google); !errors.Is(err, auth.ErrGoogleAccountConflict) {
		t.Fatal("existing Google identity was merged into another account", err)
	}
}

func TestGoogleConcurrentEmailLink(t *testing.T) {
	db := googleAuthDatabase(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()
	for _, sameSubject := range []bool{true, false} {
		t.Run(map[bool]string{true: "same subject", false: "different subjects"}[sameSubject], func(t *testing.T) {
			email := uuid.NewString() + "@gmail.com"
			id := emailOnlyGoogleFixture(t, db, email)
			subject := uuid.NewString()
			var wg sync.WaitGroup
			start := make(chan struct{})
			results := make(chan error, 8)
			for i := 0; i < cap(results); i++ {
				g := auth.GoogleIdentity{Subject: subject, Email: email, EmailVerified: true}
				if !sameSubject {
					g.Subject = uuid.NewString()
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					user, err := repo.SignInGoogleUser(ctx, g)
					if err == nil && user.ID != id {
						err = errors.New("concurrent login returned a different owner")
					}
					results <- err
				}()
			}
			close(start)
			wg.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				} else if !errors.Is(err, auth.ErrGoogleAccountConflict) {
					t.Fatal(err)
				}
			}
			want := 1
			if sameSubject {
				want = cap(results)
			}
			if successes != want {
				t.Fatalf("successful logins = %d, want %d", successes, want)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM users WHERE LOWER(email)=$1`, email).Scan(&count); err != nil || count != 1 {
				t.Fatal("concurrent login duplicated an account", err)
			}
		})
	}
}

func TestGoogleNewAccountAndStableSubject(t *testing.T) {
	db := googleAuthDatabase(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()
	// Third-party addresses still support ordinary sign-in; only linking to an
	// existing, differently authenticated owner requires authoritative email.
	google := auth.GoogleIdentity{Subject: uuid.NewString(), Email: uuid.NewString() + "@external.example", EmailVerified: true, Name: "Google user"}
	user, err := repo.SignInGoogleUser(ctx, google)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, user.ID) })
	google.Email = uuid.NewString() + "@other.example"
	again, err := repo.SignInGoogleUser(ctx, google)
	if err != nil || again.ID != user.ID || again.Email != google.Email {
		t.Fatal("changed email replaced stable Google identity", err)
	}
	var privileged bool
	if err = db.QueryRow(`SELECT is_system_admin OR is_client_manager FROM users WHERE id=$1`, user.ID).Scan(&privileged); err != nil || privileged {
		t.Fatal("new Google account gained privileged roles", err)
	}
	// Mailbox tags and dots are significant: never merge distinct addresses.
	base := uuid.NewString()
	for _, local := range []string{base, base + "+tag", base + ".suffix"} {
		google.Subject = uuid.NewString()
		google.Email = local + "@gmail.com"
		linked, err := repo.SignInGoogleUser(ctx, google)
		if err != nil || linked.ID == user.ID || linked.Email != google.Email {
			t.Fatal("distinct Gmail address collapsed into another owner", err)
		}
		t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, linked.ID) })
	}
}
