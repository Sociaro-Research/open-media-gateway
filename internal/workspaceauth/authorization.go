// Package workspaceauth implements bounded installation authorization using the
// existing browser identity, company roles and generation-only API credentials.
// Its device-style protocol needs outbound HTTPS only; it is not an OAuth server.
package workspaceauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

var ErrDisabled = errors.New("customer workspace onboarding is disabled on this host")
var ErrUnavailable = errors.New("authorization is missing, expired or no longer usable")
var ErrForbidden = errors.New("a browser account with ownership of this company is required")
var ErrPending = errors.New("authorization_pending")
var ErrSlow = errors.New("slow_down")
var ErrLegacy = errors.New("legacy connection preserved; upgrade both installations and use explicit legacy key management before starting a dedicated connection")
var ErrKeyLimit = errors.New("the host active-key limit is reached; revoke unused installation grants and retry this request")
var ErrConflict = errors.New("authorization changed; start again or review the existing choice")

const Lifetime = 10 * time.Minute
const KeyLifetime = 30 * 24 * time.Hour
const Interval = 5 * time.Second

type Challenge struct {
	InstallationProof string    `json:"-"`
	InstallationHash  []byte    `json:"-"`
	Provisioning      string    `json:"provisioning,omitempty"`
	CompanyName       string    `json:"company_name,omitempty"`
	Code              string    `json:"user_code"`
	Label             string    `json:"client_name"`
	Origin            string    `json:"client_origin"`
	ReplaceKey        string    `json:"-"`
	State             string    `json:"status"`
	Expires           time.Time `json:"expires_at"`
	Actor             string    `json:"-"`
	Company           string    `json:"organization_id,omitempty"`
	KeyID             string    `json:"key_id,omitempty"`
	TokenCipher       []byte    `json:"-"`
}
type Company struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Approval struct {
	Company string `json:"organization_id"`
	Consent bool   `json:"consent"`
	Deny    bool   `json:"deny"`
	Replace bool   `json:"replace_previous"`
}
type Grant struct {
	CompanyName  string     `json:"company_name"`
	Provisioning string     `json:"provisioning"`
	ID           string     `json:"id"`
	Company      string     `json:"organization_id"`
	Name         string     `json:"name"`
	ClientOrigin string     `json:"client_origin"`
	Expires      time.Time  `json:"expires_at"`
	Revoked      *time.Time `json:"revoked_at,omitempty"`
}
type Repository interface {
	Start(context.Context, []byte, Challenge) (Challenge, error)
	Describe(context.Context, string, string) (Challenge, []Company, bool, error)
	Approve(context.Context, string, string, Approval) (Challenge, error)
	Redeem(context.Context, []byte, apikey.Key, []byte, []byte) (Challenge, error)
	Cancel(context.Context, []byte) error
	Grants(context.Context, string) ([]Grant, error)
	Revoke(context.Context, string, string) error
	AllowAttempt(context.Context, []byte, int, time.Duration) (bool, error)
}
type Manager struct {
	Repo   Repository
	Cipher *providerconfig.Cipher
}

func New(repo Repository, cipher *providerconfig.Cipher) *Manager { return &Manager{repo, cipher} }
func DeviceCode() (string, error) {
	var raw [32]byte
	_, err := rand.Read(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:]), err
}
func Digest(value string) []byte { h := sha256.Sum256([]byte(value)); return h[:] }
func UserCode(device string) string {
	h := sha256.Sum256([]byte("workspace-user:" + device))
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:8])
}
func NormalizeCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}
func ValidDevice(device string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(device)
	return err == nil && len(raw) == 32
}
func (m *Manager) Start(ctx context.Context, device string, in Challenge) (Challenge, error) {
	if !ValidDevice(in.InstallationProof) {
		return Challenge{}, ErrLegacy
	}
	in.InstallationHash = Digest("installation:" + in.InstallationProof)
	in.InstallationProof = ""
	if !ValidDevice(device) || len(in.Label) < 1 || len(in.Label) > 80 || len(in.Origin) > 300 {
		return Challenge{}, ErrUnavailable
	}
	in.Code = UserCode(device)
	in.Expires = time.Now().UTC().Add(Lifetime)
	in.State = "pending"
	return m.Repo.Start(ctx, Digest(device), in)
}
func (m *Manager) Poll(ctx context.Context, device string) (Challenge, string, error) {
	if !ValidDevice(device) {
		return Challenge{}, "", ErrUnavailable
	}
	expiry := time.Now().UTC().Add(KeyLifetime)
	key, token, err := apikey.Generate(apikey.CreateInput{Name: "Remote installation", ResourceMode: "shared", StorageMode: "shared", Scopes: []string{apikey.Generation}, ExpiresAt: &expiry}, time.Now())
	if err != nil {
		return Challenge{}, "", err
	}
	encrypted, err := m.Cipher.Encrypt([]byte(token))
	if err != nil {
		return Challenge{}, "", err
	}
	challenge, err := m.Repo.Redeem(ctx, Digest(device), key, apikey.Hash(token), encrypted)
	if err != nil {
		return challenge, "", err
	}
	raw, err := m.Cipher.Decrypt(challenge.TokenCipher)
	return challenge, string(raw), err
}
