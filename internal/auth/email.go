package auth

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
)

var ErrEmailCredential = errors.New("invalid email credential or challenge")

type PasswordAccount struct {
	User         identity.User
	PasswordHash string
}

const registrationCookieName = "media_gateway_registration"

type EmailChallenge struct {
	Email, Purpose       string
	InvitationCiphertext []byte
}
type EmailSession struct {
	Hash      []byte
	ExpiresAt time.Time
}
type EmailRepository interface {
	EmailAccount(context.Context, string) (PasswordAccount, error)
	SaveEmailChallenge(context.Context, string, string, []byte, time.Time, []byte) (bool, error)
	EmailChallenge(context.Context, []byte, string, time.Time) (EmailChallenge, error)
	VerifyEmailRegistration(context.Context, []byte, EmailSession, time.Time) (EmailChallenge, error)
	CompleteEmailChallenge(context.Context, []byte, string, string, time.Time, *EmailSession) (identity.User, error)
	CreateEmailSession(context.Context, string, string, []byte, time.Time) error
	AllowEmailAttempt(context.Context, []byte, int, time.Duration) (bool, error)
}

type emailAuth struct {
	repo              EmailRepository
	sender            EmailSender
	origin, dummyHash string
	work              chan struct{}
	invitationCipher  *providerconfig.Cipher
}

func (m *Manager) configureEmail(sender EmailSender, origin string) error {
	repo, ok := m.repo.(EmailRepository)
	if !ok {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()))) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("AUTH_PUBLIC_URL must be an HTTPS origin (HTTP is allowed only on localhost)")
	}
	dummy, err := hashPassword("unused random password verification timing")
	if err != nil {
		return err
	}
	// Domain separation keeps invitation encryption independent of session signing.
	invitationCipher, err := providerconfig.NewCipher("email-invitation:" + string(m.signingKey))
	if err != nil {
		return err
	}
	m.email = &emailAuth{repo: repo, sender: sender, origin: strings.TrimRight(origin, "/"), dummyHash: dummy, work: make(chan struct{}, 4), invitationCipher: invitationCipher}
	return nil
}

func (m *Manager) Methods(w http.ResponseWriter, r *http.Request) {
	settings, err := m.loginMethods(r.Context())
	if err != nil {
		emailError(w, 503, "auth_unavailable", "Sign-in is temporarily unavailable.")
		return
	}
	required, err := m.setupRequired(r.Context())
	if err != nil {
		m.emailFailure(w)
		return
	}
	ready, _ := m.emailDeliveryAvailable(r.Context())
	emailJSON(w, http.StatusOK, map[string]bool{"setup_required": required, "google": !required && settings.Google, "email": !required && settings.Email && m.email != nil, "email_delivery": ready, "email_confirmation": !required && settings.Email && m.email != nil})
}

func (m *Manager) emailDeliveryAvailable(ctx context.Context) (bool, error) {
	if m.email == nil || m.email.sender == nil {
		return false, nil
	}
	if sender, ok := m.email.sender.(interface {
		Configured(context.Context) (bool, error)
	}); ok {
		return sender.Configured(ctx)
	}
	return true, nil
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 || !strings.Contains(value, "@") || strings.ContainsAny(value, "\r\n") {
		return "", ErrEmailCredential
	}
	// Keep one comparison policy across Google, registration, login and recovery;
	// do not strip dots, plus-tags or otherwise guess provider-specific aliases.
	return value, nil
}

func emailJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func emailError(w http.ResponseWriter, status int, code, message string) {
	emailJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (m *Manager) Email(w http.ResponseWriter, r *http.Request) {
	if !m.allowLogin(w, r, "email") {
		return
	}
	if m.email == nil {
		emailError(w, 503, "email_unavailable", "Email sign-in is not available yet.")
		return
	}
	// JSON-only requests plus the server's cross-origin protection prevent login
	// CSRF, including simple cross-site form submissions without Origin headers.
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if kind != "application/json" {
		emailError(w, 415, "invalid_request", "Use a JSON request.")
		return
	}
	var in struct {
		Email           string `json:"email"`
		InvitationToken string `json:"invitation_token"`
		Password        string `json:"password"`
		Token           string `json:"token"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		emailError(w, 400, "invalid_request", "Check the email and password fields.")
		return
	}
	if d.Decode(&struct{}{}) != io.EOF {
		emailError(w, 400, "invalid_request", "Invalid request.")
		return
	}
	action := r.PathValue("action")
	if action != "register" && action != "login" && action != "forgot" && action != "verify" && action != "reset" && action != "registration" && action != "complete" {
		http.NotFound(w, r)
		return
	}
	if in.InvitationToken != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(in.InvitationToken)
		if (action != "register" && action != "forgot") || err != nil || len(decoded) != 32 || len(in.InvitationToken) != 43 {
			emailError(w, 400, "invalid_request", "Use a valid invitation link when requesting a confirmation email.")
			return
		}
	}
	if action == "register" || action == "forgot" {
		ready, err := m.emailDeliveryAvailable(r.Context())
		if err != nil || !ready {
			emailError(w, 503, "email_delivery_unavailable", "Email registration and password recovery are not available yet. Please try again later.")
			return
		}
	}
	if action == "registration" || action == "complete" {
		// The email-link token is exchanged for a restricted HttpOnly cookie.
		// A request body cannot substitute a token for this registration grant.
		in.Token = ""
		if cookie, err := r.Cookie(registrationCookieName); err == nil {
			in.Token = cookie.Value
		}
		if action == "registration" && len(in.Token) != 43 {
			emailJSON(w, 200, map[string]bool{"pending": false})
			return
		}
	}
	if action == "register" && in.Password != "" {
		emailError(w, 400, "invalid_request", "Enter your email first. Choose a password after confirming your email.")
		return
	}
	var err error
	if action == "register" || action == "login" || action == "forgot" {
		in.Email, err = normalizeEmail(in.Email)
		if err != nil {
			emailError(w, 400, "invalid_email", "Enter a valid email address.")
			return
		}
	} else if len(in.Token) != 43 {
		invalidEmailLink(w, action)
		return
	}
	if action == "complete" || action == "reset" {
		if !validPassword(in.Password) {
			emailError(w, 400, "invalid_password", "Use 15–128 characters for your password.")
			return
		}
	}
	if len(in.Password) > 512 {
		emailError(w, 400, "invalid_password", "Password is too long.")
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// Never trust caller-supplied forwarding headers. The network quota also
	// bounds aggregate traffic when behind a reverse proxy; per-address quotas
	// persist across restarts and replicas in PostgreSQL.
	quotas := []struct {
		key    string
		max    int
		window time.Duration
	}{{"network:" + host, 300, 15 * time.Minute}}
	if in.Email != "" {
		quotas = append(quotas, struct {
			key    string
			max    int
			window time.Duration
		}{"account:" + action + ":" + in.Email, 10, 15 * time.Minute})
	}
	if action == "register" || action == "forgot" {
		quotas = append(quotas, struct {
			key    string
			max    int
			window time.Duration
		}{"mail:" + in.Email, 3, time.Hour})
	}
	if in.Token != "" && action != "registration" {
		quotas = append(quotas, struct {
			key    string
			max    int
			window time.Duration
		}{"token:" + in.Token, 10, 15 * time.Minute})
	}
	for _, q := range quotas {
		ok, err := m.email.repo.AllowEmailAttempt(r.Context(), tokenHash(q.key), q.max, q.window)
		if err != nil {
			emailError(w, 503, "auth_unavailable", "Sign-in is temporarily unavailable. Try again later.")
			return
		}
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(q.window.Seconds())))
			emailError(w, 429, "too_many_attempts", "Too many attempts. Please try again later.")
			return
		}
	}
	select {
	case m.email.work <- struct{}{}:
		defer func() { <-m.email.work }()
	default:
		emailError(w, 503, "auth_busy", "Sign-in is busy. Please try again shortly.")
		return
	}
	switch action {
	case "register", "forgot":
		purpose := "reset"
		ttl := 30 * time.Minute
		if action == "register" {
			purpose = "verify"
			ttl = time.Hour
		}
		token, err := randomToken(32)
		if err != nil {
			m.emailFailure(w)
			return
		}
		var invitation []byte
		if in.InvitationToken != "" {
			invitation, err = m.email.invitationCipher.Encrypt([]byte(in.InvitationToken))
			if err != nil {
				m.emailFailure(w)
				return
			}
		}
		saved, err := m.email.repo.SaveEmailChallenge(r.Context(), in.Email, purpose, tokenHash(token), time.Now().Add(ttl), invitation)
		if err != nil {
			m.emailFailure(w)
			return
		}
		if saved {
			link := m.email.origin + "/#email_" + purpose + "=" + url.QueryEscape(token)
			if err := m.email.sender.Send(r.Context(), in.Email, purpose, link, "auth-"+hex.EncodeToString(tokenHash(token))); err != nil {
				emailError(w, 503, "email_delivery_failed", "Email could not be sent. Please try again later.")
				return
			}
		}
		message := "If this address can be registered, a confirmation email is on its way. Already registered? Sign in or reset your password."
		if action == "forgot" {
			message = "If an account exists for this address, a password reset email is on its way."
		}
		emailJSON(w, 202, map[string]string{"message": message})
	case "login":
		account, err := m.email.repo.EmailAccount(r.Context(), in.Email)
		hash := m.email.dummyHash
		if err == nil {
			hash = account.PasswordHash
		}
		matched := checkPassword(in.Password, hash)
		if err != nil || !matched {
			emailError(w, 401, "invalid_credentials", "Email or password is incorrect. Confirm your email before signing in.")
			return
		}
		token, err := randomToken(32)
		if err != nil {
			m.emailFailure(w)
			return
		}
		expiry := time.Now().UTC().Add(m.sessionTTL)
		// Compare the verified hash again when creating a session. A password reset
		// that races this login must not allow a session using the previous password.
		if err = m.email.repo.CreateEmailSession(r.Context(), account.User.ID, hash, tokenHash(token), expiry); err != nil {
			emailError(w, 401, "invalid_credentials", "Please sign in again.")
			return
		}
		m.clearRegistrationCookie(w)
		m.setSessionCookie(w, token, expiry)
		emailJSON(w, 200, map[string]string{"message": "Signed in."})
	case "registration":
		challenge, err := m.email.repo.EmailChallenge(r.Context(), tokenHash(in.Token), "setup", time.Now())
		if errors.Is(err, ErrEmailCredential) {
			m.clearRegistrationCookie(w)
			emailJSON(w, 200, map[string]bool{"pending": false})
		} else if err != nil {
			m.emailFailure(w)
		} else {
			response, err := m.emailChallengeResponse(challenge)
			if err != nil {
				m.emailFailure(w)
				return
			}
			response["pending"] = true
			emailJSON(w, 200, response)
		}
	case "verify":
		grant, err := randomToken(32)
		if err != nil {
			m.emailFailure(w)
			return
		}
		expiry := time.Now().UTC().Add(30 * time.Minute)
		challenge, err := m.email.repo.VerifyEmailRegistration(r.Context(), tokenHash(in.Token), EmailSession{Hash: tokenHash(grant), ExpiresAt: expiry}, time.Now())
		if err != nil {
			if errors.Is(err, ErrEmailCredential) {
				invalidEmailLink(w, action)
			} else {
				m.emailFailure(w)
			}
			return
		}
		response, err := m.emailChallengeResponse(challenge)
		if err != nil {
			m.emailFailure(w)
			return
		}
		response["message"] = "Email confirmed. Choose your password to finish registration."
		http.SetCookie(w, &http.Cookie{Name: registrationCookieName, Value: grant, Path: "/auth/email", Expires: expiry, MaxAge: 1800, HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteStrictMode})
		emailJSON(w, 200, response)
	case "complete", "reset":
		purpose := "reset"
		if action == "complete" {
			purpose = "setup"
		}
		challenge, err := m.email.repo.EmailChallenge(r.Context(), tokenHash(in.Token), purpose, time.Now())
		if err != nil {
			if errors.Is(err, ErrEmailCredential) {
				invalidEmailLink(w, action)
			} else {
				m.emailFailure(w)
			}
			return
		}
		response, err := m.emailChallengeResponse(challenge)
		if err != nil {
			m.emailFailure(w)
			return
		}
		hash, err := hashPassword(in.Password)
		if err != nil {
			m.emailFailure(w)
			return
		}
		var session *EmailSession
		var sessionToken string
		if action == "complete" {
			sessionToken, err = randomToken(32)
			if err != nil {
				m.emailFailure(w)
				return
			}
			session = &EmailSession{Hash: tokenHash(sessionToken), ExpiresAt: time.Now().UTC().Add(m.sessionTTL)}
		}
		// Only a verified registration grant can create an account. Its password
		// is chosen now; legacy passwords attached to email links are never used.
		if _, err = m.email.repo.CompleteEmailChallenge(r.Context(), tokenHash(in.Token), purpose, hash, time.Now(), session); err != nil {
			if errors.Is(err, ErrEmailCredential) {
				invalidEmailLink(w, action)
			} else {
				m.emailFailure(w)
			}
			return
		}
		message := "Password saved. Sign in with your new password."
		m.clearRegistrationCookie(w)
		if action == "complete" {
			message = "Your account is ready. You are now signed in."
			m.setSessionCookie(w, sessionToken, session.ExpiresAt)
		}
		response["message"] = message
		emailJSON(w, 200, response)
	}
}

// Called only after proof of mailbox ownership: a link token or restricted grant.
// The invitation remains subject to ordinary authenticated preview/accept checks.
func (m *Manager) emailChallengeResponse(challenge EmailChallenge) (map[string]any, error) {
	response := map[string]any{"email": challenge.Email}
	if len(challenge.InvitationCiphertext) > 0 {
		invitation, err := m.email.invitationCipher.Decrypt(challenge.InvitationCiphertext)
		if err != nil {
			return nil, err
		}
		response["invitation_token"] = string(invitation)
	}
	return response, nil
}

func invalidEmailLink(w http.ResponseWriter, action string) {
	message := "This link is invalid or has expired. Request a new one."
	if action == "verify" {
		message = "This confirmation link has expired or already been used. Finish registration in the browser where you opened it, or request a new confirmation email."
	}
	if action == "complete" {
		message = "Your registration session has expired or already been used. Request a new confirmation email to continue."
	}
	emailError(w, 400, "invalid_link", message)
}

func (m *Manager) emailFailure(w http.ResponseWriter) {
	emailError(w, 503, "auth_unavailable", "Sign-in is temporarily unavailable. Please try again later.")
}
func (m *Manager) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", Expires: expires, MaxAge: int(m.sessionTTL.Seconds()), HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteLaxMode})
}

func (m *Manager) clearRegistrationCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: registrationCookieName, Value: "", Path: "/auth/email", MaxAge: -1, HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteStrictMode})
}
