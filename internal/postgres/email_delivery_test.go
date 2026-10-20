package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/audit"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
)

type configuredMailbox struct {
	*authMailbox
	delivery *auth.DeliveryManager
}

func (m configuredMailbox) Configured(ctx context.Context) (bool, error) {
	return m.delivery.Configured(ctx)
}

func TestEmailRegistrationConfiguredBySystemAdministrator(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "email_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	params := u.Query()
	params.Set("search_path", schema)
	u.RawQuery = params.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	users := NewAuthRepository(db)
	repo := NewSystemRepository(db)
	admin, err := users.UpsertGoogleUser(ctx, "admin-sub", "admin@example.test", "Administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetAdmin(ctx, "", admin.ID, true, false); err != nil {
		t.Fatal(err)
	}
	ordinary, err := users.UpsertGoogleUser(ctx, "ordinary-sub", "ordinary@example.test", "Member", "")
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
	delivery := auth.NewDeliveryManager(repo, cipher)
	mailbox := configuredMailbox{authMailbox: &authMailbox{}, delivery: delivery}
	manager, err := auth.New(ctx, users, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://example.test", EmailSender: mailbox})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/email/{action}", manager.Email)
	mux.HandleFunc("GET /auth/methods", manager.Methods)
	call := func(action string, data map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(data)
		r := httptest.NewRequest("POST", "/auth/email/"+action, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	methods := func() map[string]bool {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/auth/methods", nil))
		v := map[string]bool{}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	credentials := map[string]string{"email": "new-user@example.test", "password": "A long secret passphrase 123!"}
	if !methods()["email"] || methods()["email_delivery"] {
		t.Fatal("password login should be available independently of registration")
	}
	if w := call("register", map[string]string{"email": credentials["email"]}); w.Code != 503 {
		t.Fatal("unconfigured registration accepted", w.Code)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM email_auth_challenges`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unconfigured registration persisted", err)
	}
	in := auth.DeliveryUpdate{Provider: "resend", From: "Sociaro <login@example.test>", APIKey: "fixture-secret-key", Version: 1}
	if _, err = delivery.Update(ctx, ordinary.ID, in); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("ordinary user configured mail", err)
	}
	settings, err := delivery.Update(ctx, admin.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !methods()["email"] {
		t.Fatal("saving did not activate registration without restart")
	}
	if len(mailbox.links) != 0 {
		t.Fatal("saving settings sent mail")
	}
	if w := call("register", map[string]string{"email": credentials["email"]}); w.Code != 202 {
		t.Fatal("registration failed", w.Code, w.Body.String())
	}
	verified := call("verify", map[string]string{"token": mailbox.token()})
	if verified.Code != 200 {
		t.Fatal("confirmation failed", verified.Code, verified.Body.String())
	}
	if w := call("complete", map[string]string{"password": credentials["password"]}, verified.Result().Cookies()...); w.Code != 200 {
		t.Fatal("password setup failed", w.Code, w.Body.String())
	}

	login := call("login", credentials)
	if login.Code != 200 {
		t.Fatal("verified login failed", login.Code)
	}
	authenticated := httptest.NewRequest("GET", "https://example.test/v1/me", nil)
	for _, cookie := range login.Result().Cookies() {
		if cookie.MaxAge >= 0 {
			authenticated.AddCookie(cookie)
		}
	}
	if user, err := manager.Authenticate(authenticated); err != nil || user.Email != credentials["email"] {
		t.Fatal("login did not create the expected session", err)
	}
	if _, err = delivery.Update(ctx, admin.ID, in); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale edit accepted", err)
	}
	stored, err := repo.EmailDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.KeyCiphertext), in.APIKey) {
		t.Fatal("plaintext key persisted")
	}
	events, err := repo.AuditEvents(ctx, audit.Filter{From: time.Unix(0, 0), Until: time.Now().Add(time.Hour), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), in.APIKey) || strings.Contains(string(encoded), "ciphertext") {
		t.Fatal("key in activity log")
	}
	// A new process reads the same persisted connection, without startup env credentials.
	nextProcess := auth.NewDeliveryManager(repo, cipher)
	if ready, err := nextProcess.Configured(ctx); err != nil || !ready {
		t.Fatal("settings did not persist", err)
	}
	if _, err = delivery.Update(ctx, admin.ID, auth.DeliveryUpdate{Provider: "", Version: settings.Version}); err != nil {
		t.Fatal(err)
	}
	if err = nextProcess.Bootstrap(ctx, "old-env-key", "old@example.test"); err != nil {
		t.Fatal(err)
	}
	if methods()["email_delivery"] {
		t.Fatal("removed connection restored from environment")
	}
	if w := call("register", map[string]string{"email": "another@example.test"}); w.Code != 503 {
		t.Fatal("disabled registration accepted", w.Code)
	}
	if w := call("login", credentials); w.Code != 200 {
		t.Fatal("existing password login depends on delivery", w.Code)
	}
	if user, err := manager.Authenticate(authenticated); err != nil || user.Email != credentials["email"] {
		t.Fatal("disabling delivery invalidated the existing session", err)
	}
	stored, err = repo.EmailDelivery(ctx)
	if err != nil || len(stored.KeyCiphertext) != 0 {
		t.Fatal("key not removed", err)
	}
	// Reconnecting delivery restores registration; existing login keeps working.
	in.Version = stored.Version
	if _, err = delivery.Update(ctx, admin.ID, in); err != nil {
		t.Fatal(err)
	}
	if !methods()["email"] {
		t.Fatal("email login not restored after reconnecting delivery")
	}
	if w := call("login", credentials); w.Code != 200 {
		t.Fatal("reconnecting delivery did not restore login", w.Code)
	}
}
