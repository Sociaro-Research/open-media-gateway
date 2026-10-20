package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
)

var ErrSetupClosed = errors.New("initial owner setup is unavailable or already completed")
var ErrSetupCode = errors.New("invalid installation setup code")

type SetupState struct {
	Required       bool
	CodeHash       []byte
	CodeCiphertext []byte
}

type SetupInput struct {
	CodeHash                  []byte
	Email, Name, PasswordHash string
	Session                   EmailSession
}

type SetupRepository interface {
	InstallationSetup(context.Context) (SetupState, error)
	EnsureInstallationSetup(context.Context, []byte, []byte) error
	CompleteInstallationSetup(context.Context, SetupInput) error
}

// InstallationSetupCode is available only to trusted server callers. HTTP never
// returns the code or its ciphertext. Encryption permits recovery after restart
// without logging a reusable secret or adding another persistent secret volume.
func InstallationSetupCode(ctx context.Context, repo SetupRepository, cipher *providerconfig.Cipher) (string, error) {
	state, err := repo.InstallationSetup(ctx)
	if err != nil {
		return "", err
	}
	if !state.Required {
		return "", ErrSetupClosed
	}
	if len(state.CodeHash) == 0 {
		code, err := randomToken(32)
		if err != nil {
			return "", err
		}
		encrypted, err := cipher.Encrypt([]byte(code))
		if err != nil {
			return "", err
		}
		if err = repo.EnsureInstallationSetup(ctx, tokenHash(code), encrypted); err != nil {
			return "", err
		}
		state, err = repo.InstallationSetup(ctx)
		if err != nil {
			return "", err
		}
	}
	if !state.Required {
		return "", ErrSetupClosed
	}
	plain, err := cipher.Decrypt(state.CodeCiphertext)
	if err != nil {
		return "", err
	}
	if len(plain) != 43 || subtle.ConstantTimeCompare(tokenHash(string(plain)), state.CodeHash) != 1 {
		return "", ErrSetupCode
	}
	return string(plain), nil
}

func (m *Manager) setupRequired(ctx context.Context) (bool, error) {
	if m.setup == nil {
		return false, nil
	}
	state, err := m.setup.InstallationSetup(ctx)
	return state.Required, err
}

func (m *Manager) Setup(w http.ResponseWriter, r *http.Request) {
	if m.setup == nil || m.email == nil {
		http.NotFound(w, r)
		return
	}
	state, err := m.setup.InstallationSetup(r.Context())
	if err != nil {
		m.emailFailure(w)
		return
	}
	if !state.Required {
		emailError(w, 409, "setup_closed", "Initial owner setup is already completed. Sign in with your existing account.")
		return
	}
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if kind != "application/json" {
		emailError(w, 415, "invalid_request", "Use a JSON request.")
		return
	}
	var in struct{ Code, Email, Name, Password string }
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
		emailError(w, 400, "invalid_request", "Check the setup fields.")
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ok, err := m.email.repo.AllowEmailAttempt(r.Context(), tokenHash("installation-setup:"+host), 30, 15*time.Minute)
	if err != nil {
		m.emailFailure(w)
		return
	}
	if !ok {
		w.Header().Set("Retry-After", "900")
		emailError(w, 429, "too_many_attempts", "Too many setup attempts. Please try again later.")
		return
	}
	if len(in.Code) != 43 || subtle.ConstantTimeCompare(tokenHash(in.Code), state.CodeHash) != 1 {
		emailError(w, 403, "invalid_setup_code", "The setup code is incorrect. Get the code from this installation's server.")
		return
	}
	in.Email, err = normalizeEmail(in.Email)
	in.Name = strings.TrimSpace(in.Name)
	if err != nil || in.Name == "" || !utf8.ValidString(in.Name) || utf8.RuneCountInString(in.Name) > 100 || !validPassword(in.Password) {
		emailError(w, 400, "invalid_request", "Enter your name, a valid email and a password of 15–128 characters.")
		return
	}
	select {
	case m.email.work <- struct{}{}:
		defer func() { <-m.email.work }()
	default:
		emailError(w, 503, "auth_busy", "Setup is busy. Please try again shortly.")
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		m.emailFailure(w)
		return
	}
	token, err := randomToken(32)
	if err != nil {
		m.emailFailure(w)
		return
	}
	expiry := time.Now().UTC().Add(m.sessionTTL)
	err = m.setup.CompleteInstallationSetup(r.Context(), SetupInput{CodeHash: tokenHash(in.Code), Email: in.Email, Name: in.Name, PasswordHash: hash, Session: EmailSession{Hash: tokenHash(token), ExpiresAt: expiry}})
	if errors.Is(err, ErrSetupClosed) {
		emailError(w, 409, "setup_closed", "Initial owner setup is already completed. Sign in with your existing account.")
		return
	}
	if errors.Is(err, ErrSetupCode) {
		emailError(w, 403, "invalid_setup_code", "The setup code is incorrect.")
		return
	}
	if err != nil {
		m.emailFailure(w)
		return
	}
	m.clearRegistrationCookie(w)
	m.setSessionCookie(w, token, expiry)
	emailJSON(w, 201, map[string]string{"message": "Your owner account is ready. You are now signed in."})
}
