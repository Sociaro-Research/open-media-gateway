package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/google/uuid"
)

type authMailbox struct {
	mu    sync.Mutex
	links []string
}

func (m *authMailbox) Send(_ context.Context, _, _, link, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links = append(m.links, link)
	return nil
}
func (m *authMailbox) token() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, _ := url.Parse(m.links[len(m.links)-1])
	return strings.SplitN(u.Fragment, "=", 2)[1]
}

func TestEmailAuthenticationLifecycle(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
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
	repo := NewAuthRepository(db)
	mailbox := &authMailbox{}
	cfg := auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://example.test", EmailSender: mailbox}
	manager, err := auth.New(ctx, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/email/{action}", manager.Email)
	mux.HandleFunc("GET /auth/methods", manager.Methods)
	handler := http.NewCrossOriginProtection().Handler(mux)
	peer := uuid.NewString() + ":1234"
	call := func(action string, data map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		body, _ := json.Marshal(data)
		r := httptest.NewRequest("POST", "https://example.test/auth/email/"+action, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = peer
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d wanted %d: %s", w.Code, status, w.Body.String())
		}
	}
	email := uuid.NewString() + "@example.test"
	password := "A long secret passphrase 123!"
	defer db.Exec(`DELETE FROM users WHERE LOWER(email)=$1`, email)
	expect(call("register", map[string]string{"email": "  " + strings.ToUpper(email) + "  "}), 202)
	token := mailbox.token()
	var count int
	db.QueryRow(`SELECT count(*) FROM users WHERE email=$1`, email).Scan(&count)
	if count != 0 {
		t.Fatal("unverified user created")
	}
	expect(call("login", map[string]string{"email": email, "password": password}), 401)
	expect(call("register", map[string]string{"email": email, "password": password}), 400)
	var pendingHash string
	if err = db.QueryRow(`SELECT password_hash FROM email_auth_challenges WHERE email=$1`, email).Scan(&pendingHash); err != nil || pendingHash != "" {
		t.Fatal("registration stored a password before verification", err)
	}
	expect(call("complete", map[string]string{"token": token, "password": password}), 400)
	// Confirmation must not require the registration password. A replaced token
	// still cannot be used, and merely fetching a URL cannot consume one.
	expect(call("verify", map[string]string{"token": strings.Repeat("x", 43)}), 400)
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest("GET", "https://example.test/auth/email/verify", nil))
	expect(get, http.StatusMethodNotAllowed)
	expect(call("register", map[string]string{"email": email}), 202)
	expect(call("verify", map[string]string{"token": token}), 400)
	token = mailbox.token()
	// Confirmation is single-use even for simultaneous clicks.
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- call("verify", map[string]string{"token": token})
		}()
	}
	wg.Wait()
	close(responses)
	successes := 0
	var verified *httptest.ResponseRecorder
	for response := range responses {
		if response.Code == 200 {
			successes++
			verified = response
		} else if response.Code != 400 || len(response.Result().Cookies()) != 0 {
			t.Fatal("failed confirmation set a session", response.Code)
		}
	}
	if successes != 1 {
		t.Fatal("verification was not single use")
	}
	grantCookies := verified.Result().Cookies()
	if len(grantCookies) != 1 {
		t.Fatal("confirmation must issue only a limited grant")
	}
	grantCookie := grantCookies[0]
	if grantCookie.Name != "media_gateway_registration" || grantCookie.Path != "/auth/email" || !grantCookie.Secure || !grantCookie.HttpOnly || grantCookie.SameSite != http.SameSiteStrictMode || grantCookie.MaxAge != 1800 {
		t.Fatal("unsafe registration grant")
	}
	limited := httptest.NewRequest("GET", "https://example.test/v1/me", nil)
	limited.AddCookie(grantCookie)
	if _, err = manager.Authenticate(limited); err == nil {
		t.Fatal("registration grant authenticated the user")
	}
	if err = db.QueryRow(`SELECT count(*) FROM users WHERE email=$1`, email).Scan(&count); err != nil || count != 0 {
		t.Fatal("verification created a user before password setup", err)
	}
	expect(call("login", map[string]string{"email": email, "password": password}), 401)
	state := call("registration", map[string]string{}, grantCookie)
	expect(state, 200)
	if !strings.Contains(state.Body.String(), `"pending":true`) || !strings.Contains(state.Body.String(), email) {
		t.Fatal("password setup cannot resume")
	}
	state = call("registration", map[string]string{})
	expect(state, 200)
	if !strings.Contains(state.Body.String(), `"pending":false`) || strings.Contains(state.Body.String(), email) {
		t.Fatal("registration state leaked without grant")
	}
	expect(call("complete", map[string]string{"password": "short"}, grantCookie), 400)
	// The original email token is not a password-setup grant, even in a cookie.
	fakeGrant := *grantCookie
	fakeGrant.Value = token
	expect(call("complete", map[string]string{"password": password}, &fakeGrant), 400)
	// Two password submissions race for one grant: only one creates a session.
	completedResponses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completedResponses <- call("complete", map[string]string{"password": password, "email": "other@example.test"}, grantCookie)
		}()
	}
	wg.Wait()
	close(completedResponses)
	var completed *httptest.ResponseRecorder
	successes = 0
	for response := range completedResponses {
		if response.Code == 200 {
			completed = response
			successes++
		} else {
			expect(response, 400)
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("duplicate completion issued cookie")
			}
		}
	}
	if successes != 1 {
		t.Fatal("registration grant was not single use")
	}
	if err = db.QueryRow(`SELECT count(*) FROM organization_members m JOIN users u ON u.id=m.user_id WHERE u.email=$1`, email).Scan(&count); err != nil || count != 0 {
		t.Fatal("registration granted organization access", err)
	}
	account, err := repo.EmailAccount(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(account.PasswordHash, password) || !strings.HasPrefix(account.PasswordHash, "$argon2id$") {
		t.Fatal("password stored incorrectly")
	}
	// Only password completion creates the normal authenticated session.
	var cookies []*http.Cookie
	clearedGrant := false
	for _, cookie := range completed.Result().Cookies() {
		if cookie.Name == "media_gateway_session" {
			cookies = append(cookies, cookie)
		}
		if cookie.Name == grantCookie.Name && cookie.MaxAge < 0 {
			clearedGrant = true
		}
	}
	if !clearedGrant || len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe session cookie")
	}
	signed := httptest.NewRequest("GET", "https://example.test/v1/me", nil)
	signed.AddCookie(cookies[0])
	user, err := manager.Authenticate(signed)
	if err != nil || user.ID != account.User.ID {
		t.Fatal("session not recognized")
	}
	expect(call("login", map[string]string{"email": email, "password": password}), 200)
	repeated := call("verify", map[string]string{"token": token})
	expect(repeated, 400)
	if !strings.Contains(repeated.Body.String(), "already been used") || len(repeated.Result().Cookies()) != 0 {
		t.Fatal("reused confirmation did not explain how to sign in")
	}
	expect(call("forgot", map[string]string{"email": email}), 202)
	reset := mailbox.token()
	expect(call("verify", map[string]string{"token": reset}), 400)
	resetResponse := call("reset", map[string]string{"token": reset, "password": "Another long passphrase 456!"})
	expect(resetResponse, 200)
	for _, cookie := range resetResponse.Result().Cookies() {
		if cookie.Name == "media_gateway_session" {
			t.Fatal("password reset automatically signed in")
		}
	}
	if _, err = manager.Authenticate(signed); err == nil {
		t.Fatal("old session survived password reset")
	}
	expect(call("reset", map[string]string{"token": reset, "password": password}), 400)
	expect(call("login", map[string]string{"email": email, "password": password}), 401)
	expect(call("login", map[string]string{"email": email, "password": "Another long passphrase 456!"}), 200)
	if err = repo.CreateEmailSession(ctx, user.ID, account.PasswordHash, []byte("stale-session"), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("old password hash created a session after reset")
	}
	// A Google user can establish an email password through mailbox recovery,
	// retaining the same owner ID. Registration cannot overwrite its credentials.
	googleEmail := uuid.NewString() + "@example.test"
	google, err := repo.UpsertGoogleUser(ctx, uuid.NewString(), googleEmail, "Google user", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, google.ID)
	sent := len(mailbox.links)
	expect(call("register", map[string]string{"email": googleEmail}), 202)
	if len(mailbox.links) != sent {
		t.Fatal("existing account password registration sent")
	}
	expect(call("forgot", map[string]string{"email": googleEmail}), 202)
	expect(call("reset", map[string]string{"token": mailbox.token(), "password": password}), 200)
	linked, err := repo.EmailAccount(ctx, googleEmail)
	if err != nil || linked.User.ID != google.ID {
		t.Fatal("Google identity was duplicated")
	}
	if _, err = repo.UpsertGoogleUser(ctx, uuid.NewString(), email, "Other Google identity", ""); err == nil {
		t.Fatal("implicit email-based account linking allowed")
	}
	// No address or token is exposed by the API, and unavailable delivery creates nothing.
	cfg.EmailSender = nil
	disabled, err := auth.New(ctx, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://example.test/auth/email/register", strings.NewReader(`{"email":"pending@example.test","password":"A long secret passphrase 123!"}`))
	r.SetPathValue("action", "register")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	disabled.Email(w, r)
	expect(w, 503)
	unknown := call("forgot", map[string]string{"email": uuid.NewString() + "@example.test"})
	expect(unknown, 202)
	if strings.Contains(unknown.Body.String(), "token") {
		t.Fatal("token exposed")
	}
	// Account creation and session creation must be atomic. A failed session
	// insert preserves the challenge and does not leave a partially created user.
	atomicEmail := uuid.NewString() + "@example.test"
	defer db.Exec(`DELETE FROM users WHERE LOWER(email)=$1`, atomicEmail)
	expect(call("register", map[string]string{"email": atomicEmail}), 202)
	atomicToken := mailbox.token()
	// A pre-upgrade link may have a password chosen by somebody else. Ignore it.
	if _, err = db.Exec(`UPDATE email_auth_challenges SET password_hash=$2 WHERE email=$1`, atomicEmail, account.PasswordHash); err != nil {
		t.Fatal(err)
	}
	atomicVerified := call("verify", map[string]string{"token": atomicToken})
	expect(atomicVerified, 200)
	atomicGrant := atomicVerified.Result().Cookies()[0]
	atomicHash := sha256.Sum256([]byte(atomicGrant.Value))
	duplicateHash := sha256.Sum256([]byte(uuid.NewString()))
	if err = repo.CreateSession(ctx, duplicateHash[:], google.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, err = repo.CompleteEmailChallenge(ctx, atomicHash[:], "setup", account.PasswordHash, time.Now(), &auth.EmailSession{Hash: duplicateHash[:], ExpiresAt: time.Now().Add(time.Hour)})
	if err == nil {
		t.Fatal("duplicate session unexpectedly inserted")
	}
	if err = db.QueryRow(`SELECT count(*) FROM users WHERE LOWER(email)=$1`, atomicEmail).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed session left a user", err)
	}
	// A verified grant can finish password setup even if mail delivery goes down.
	r = httptest.NewRequest("POST", "https://example.test/auth/email/complete", strings.NewReader(`{"password":"A new owner passphrase 789!"}`))
	r.SetPathValue("action", "complete")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(atomicGrant)
	r.RemoteAddr = peer
	w = httptest.NewRecorder()
	disabled.Email(w, r)
	expect(w, 200)
	expect(call("complete", map[string]string{"password": "A new owner passphrase 789!"}, atomicGrant), 400)
	expect(call("login", map[string]string{"email": atomicEmail, "password": password}), 401)
	expect(call("login", map[string]string{"email": atomicEmail, "password": "A new owner passphrase 789!"}), 200)
	// Expiry and shared, persistent throttling.
	expiryEmail := uuid.NewString() + "@example.test"
	expect(call("register", map[string]string{"email": expiryEmail}), 202)
	expired := mailbox.token()
	digest := sha256.Sum256([]byte(expired))
	db.Exec(`UPDATE email_auth_challenges SET expires_at=NOW()-interval '1 second' WHERE token_hash=$1`, digest[:])
	expect(call("verify", map[string]string{"token": expired}), 400)
	grantEmail := uuid.NewString() + "@example.test"
	expect(call("register", map[string]string{"email": grantEmail}), 202)
	grantToken := mailbox.token()
	r = httptest.NewRequest("POST", "https://example.test/auth/email/verify", strings.NewReader(`{"token":"`+grantToken+`"}`))
	r.SetPathValue("action", "verify")
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = peer
	w = httptest.NewRecorder()
	disabled.Email(w, r)
	expect(w, 200)
	// Existing confirmation links remain usable without sending another email.
	expiringGrant := w.Result().Cookies()[0]
	grantHash := sha256.Sum256([]byte(expiringGrant.Value))
	if _, err = db.Exec(`UPDATE email_auth_challenges SET expires_at=NOW()-interval '1 second' WHERE token_hash=$1`, grantHash[:]); err != nil {
		t.Fatal(err)
	}
	expect(call("complete", map[string]string{"password": password}, expiringGrant), 400)
	state = call("registration", map[string]string{}, expiringGrant)
	expect(state, 200)
	if !strings.Contains(state.Body.String(), `"pending":false`) {
		t.Fatal("expired grant resumed")
	}
	collisionEmail := uuid.NewString() + "@example.test"
	expect(call("register", map[string]string{"email": collisionEmail}), 202)
	collisionToken := mailbox.token()
	collision, err := repo.UpsertGoogleUser(ctx, uuid.NewString(), collisionEmail, "Google owner", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, collision.ID)
	expect(call("verify", map[string]string{"token": collisionToken}), 400)
	if _, err = repo.EmailAccount(ctx, collisionEmail); err == nil {
		t.Fatal("confirmation replaced Google account")
	}
	limitedEmail := uuid.NewString() + "@example.test"
	for i := 0; i < 10; i++ {
		expect(call("login", map[string]string{"email": limitedEmail, "password": password}), 401)
	}
	expect(call("login", map[string]string{"email": limitedEmail, "password": password}), 429)
	cross := httptest.NewRequest("POST", "https://example.test/auth/email/login", strings.NewReader(`{}`))
	cross.Header.Set("Origin", "https://evil.test")
	cross.Header.Set("Content-Type", "application/json")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, cross)
	expect(denied, 403)
	plain := httptest.NewRequest("POST", "https://example.test/auth/email/login", strings.NewReader(`{}`))
	plain.SetPathValue("action", "login")
	invalid := httptest.NewRecorder()
	manager.Email(invalid, plain)
	expect(invalid, 415)
}
