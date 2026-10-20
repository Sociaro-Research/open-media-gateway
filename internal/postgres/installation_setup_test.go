package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/pressly/goose/v3"
)

func TestInstallationOwnerSetup(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewAuthRepository(db)
	cipher, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
	cfg := auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://gateway.test", SetupCipher: cipher}
	m, err := auth.New(ctx, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.InstallationSetupCode(ctx, repo, cipher)
	if err != nil || len(code) != 43 {
		t.Fatal("missing code", err)
	}
	other, err := auth.New(ctx, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	same, err := auth.InstallationSetupCode(ctx, repo, cipher)
	if err != nil || same != code {
		t.Fatal("restart changed code", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/setup", m.Setup)
	mux.HandleFunc("POST /auth/email/{action}", m.Email)
	mux.HandleFunc("GET /auth/methods", m.Methods)
	handler := http.NewCrossOriginProtection().Handler(mux)
	call := func(path string, body map[string]string, crossOrigin bool) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "https://gateway.test"+path, strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "127.0.0.1:1234"
		if crossOrigin {
			r.Header.Set("Origin", "https://other.test")
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("HTTP %d wanted %d: %s", w.Code, status, w.Body)
		}
	}
	body := map[string]string{"code": code, "email": "owner@example.test", "name": "Owner", "password": "A private owner passphrase 123!"}
	methods := func() map[string]bool {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "https://gateway.test/auth/methods", nil))
		expect(w, 200)
		var data map[string]bool
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(w.Body.String(), code) {
			t.Fatal("code leaked in methods")
		}
		return data
	}
	if state := methods(); !state["setup_required"] || state["email"] || state["google"] {
		t.Fatal(state)
	}
	expect(call("/auth/setup", body, true), 403)
	bad := map[string]string{"code": strings.Repeat("x", 43), "email": body["email"], "name": body["name"], "password": body["password"]}
	expect(call("/auth/setup", bad, false), 403)
	bad["code"] = code
	bad["password"] = "short"
	expect(call("/auth/setup", bad, false), 400)
	expect(call("/auth/email/register", map[string]string{"email": "stranger@example.test"}, false), 409)
	// A failed final session write must roll back owner, password and code use.
	_, err = db.Exec(`CREATE FUNCTION reject_setup_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture session failure'; END $$; CREATE TRIGGER reject_setup_session BEFORE INSERT ON user_sessions FOR EACH ROW EXECUTE FUNCTION reject_setup_session()`)
	if err != nil {
		t.Fatal(err)
	}
	expect(call("/auth/setup", body, false), 503)
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial owner", count, err)
	}
	if !methods()["setup_required"] {
		t.Fatal("failure consumed code")
	}
	if _, err = db.Exec(`DROP TRIGGER reject_setup_session ON user_sessions; DROP FUNCTION reject_setup_session()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		wg.Go(func() { results <- call("/auth/setup", body, false) })
	}
	wg.Wait()
	close(results)
	var success *httptest.ResponseRecorder
	rejected := 0
	for w := range results {
		if w.Code == 201 {
			if success != nil {
				t.Fatal("two owners")
			}
			success = w
		} else if w.Code == 409 {
			rejected++
		} else {
			t.Fatal(w.Code, w.Body)
		}
	}
	if success == nil || rejected != 1 {
		t.Fatal("concurrent setup did not serialize")
	}
	var cookie *http.Cookie
	for _, c := range success.Result().Cookies() {
		if c.Name == "media_gateway_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("missing secure session")
	}
	r := httptest.NewRequest("GET", "https://gateway.test/", nil)
	r.AddCookie(cookie)
	user, err := other.Authenticate(r)
	if err != nil {
		t.Fatal("replica rejected session", err)
	}
	if admin, err := NewSystemRepository(db).IsAdmin(ctx, user.ID); err != nil || !admin {
		t.Fatal("owner lacks role", err)
	}
	state, err := repo.InstallationSetup(ctx)
	if err != nil || state.Required || len(state.CodeHash) > 0 || len(state.CodeCiphertext) > 0 {
		t.Fatal("setup not consumed", err)
	}
	if _, err = auth.InstallationSetupCode(ctx, repo, cipher); !errors.Is(err, auth.ErrSetupClosed) {
		t.Fatal("closed code readable", err)
	}
	expect(call("/auth/setup", body, false), 409)
	if state := methods(); state["setup_required"] || !state["email"] || state["email_delivery"] {
		t.Fatal(state)
	}
	expect(call("/auth/email/login", map[string]string{"email": body["email"], "password": body["password"]}, false), 200)
	expect(call("/auth/email/register", map[string]string{"email": "next@example.test"}, false), 503)
	expect(call("/auth/email/forgot", map[string]string{"email": body["email"]}, false), 503)
	var details string
	if err = db.QueryRow(`SELECT details::text FROM audit_events WHERE action='administrator_setup'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, code) || strings.Contains(details, body["password"]) {
		t.Fatal("setup secret in audit")
	}
	if _, err = db.Exec(`DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	if state, err = repo.InstallationSetup(ctx); err != nil || state.Required {
		t.Fatal("setup reopened after user deletion", err)
	}
}

func TestInstallationSetupProtectsUpgradesAndHeadlessBootstrap(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless", true: "upgrade"}[upgrade], func(t *testing.T) {
			db := auditDatabase(t, false)
			ctx := context.Background()
			goose.SetBaseFS(migrations)
			if err := goose.SetDialect("postgres"); err != nil {
				t.Fatal(err)
			}
			if err := goose.UpTo(db, "migrations", 65); err != nil {
				t.Fatal(err)
			}
			if upgrade {
				if _, err := db.Exec(`INSERT INTO users(id,email,is_system_admin) VALUES('aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa','existing@example.test',TRUE)`); err != nil {
					t.Fatal(err)
				}
			}
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			repo := NewAuthRepository(db)
			cipher, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
			if !upgrade {
				if _, err := auth.InstallationSetupCode(ctx, repo, cipher); err != nil {
					t.Fatal(err)
				}
				if _, _, err := NewSystemRepository(db).InitializeAdministrator(ctx, "headless@example.test", "Operator", "Bootstrap"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := auth.InstallationSetupCode(ctx, repo, cipher); !errors.Is(err, auth.ErrSetupClosed) {
				t.Fatal("existing installation exposed setup", err)
			}
			if _, err := db.Exec(`DELETE FROM users`); err != nil {
				t.Fatal(err)
			}
			if state, err := repo.InstallationSetup(ctx); err != nil || state.Required || len(state.CodeHash) > 0 {
				t.Fatal("setup reopened", err)
			}
		})
	}
}

func TestInstallationSetupRateLimit(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewAuthRepository(db)
	cipher, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
	m, err := auth.New(ctx, repo, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://gateway.test", SetupCipher: cipher})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 31 {
		r := httptest.NewRequest("POST", "https://gateway.test/auth/setup", strings.NewReader(`{"code":"wrong"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.Setup(w, r)
		want := 403
		if i == 30 {
			want = 429
		}
		if w.Code != want {
			t.Fatal(i, w.Code, w.Body)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid attempts created users", err)
	}
	// PostgreSQL retains only a hash of the network quota key.
	h := sha256.Sum256([]byte("installation-setup:192.0.2.1"))
	if err := db.QueryRow(`SELECT attempts FROM email_auth_limits WHERE key_hash=$1`, h[:]).Scan(&count); err != nil || count != 31 {
		t.Fatal("quota not durable", count, err)
	}
}
