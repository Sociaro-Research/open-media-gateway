// Package playgroundkey stores only explicitly created Playground credentials.
// Generation still authenticates the ordinary bearer secret through apikey.
package playgroundkey

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/google/uuid"
)

const Purpose = "playground_templates"
const Name = "Playground & Templates"

var ErrMissing = errors.New("no saved Playground key")
var ErrInactive = errors.New("saved Playground key is no longer active")
var ErrConflict = errors.New("Playground key creation was superseded; review the current key")
var ErrUnavailable = errors.New("saved Playground key is unavailable")

type Record struct {
	Owner, Organization, KeyID string
	Ciphertext                 []byte
}
type Repository interface {
	PlaygroundKey(context.Context, string, string) (Record, error)
	CreatePlaygroundKey(context.Context, string, string, string, apikey.Key, []byte, []byte) (Record, error)
}
type Manager struct {
	Repo Repository
	Keys apikey.Repository
	aead cipher.AEAD
}

// Derive a separate encryption domain from the existing operator-managed master.
// AAD prevents moving a ciphertext between accounts, workspaces or key records.
func New(repo Repository, keys apikey.Repository, master string) (*Manager, error) {
	if len(master) < 16 {
		return nil, ErrUnavailable
	}
	sum := sha256.Sum256([]byte("open-media-gateway:playground-key:v1\x00" + master))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, ErrUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	return &Manager{Repo: repo, Keys: keys, aead: aead}, nil
}
func binding(r Record) []byte {
	b, _ := json.Marshal([]string{Purpose, r.Owner, r.Organization, r.KeyID})
	return b
}
func (m *Manager) seal(r Record, secret string) ([]byte, error) {
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrUnavailable
	}
	return m.aead.Seal(nonce, nonce, []byte(secret), binding(r)), nil
}
func (m *Manager) open(ctx context.Context, r Record) (apikey.Key, string, error) {
	if len(r.Ciphertext) <= m.aead.NonceSize() {
		return apikey.Key{}, "", ErrUnavailable
	}
	n := m.aead.NonceSize()
	raw, err := m.aead.Open(nil, r.Ciphertext[:n], r.Ciphertext[n:], binding(r))
	if err != nil {
		return apikey.Key{}, "", ErrUnavailable
	}
	secret := string(raw)
	user, key, err := m.Keys.Authenticate(ctx, apikey.Hash(secret))
	if errors.Is(err, apikey.ErrNotFound) {
		return apikey.Key{}, "", ErrInactive
	}
	if err != nil {
		return apikey.Key{}, "", ErrUnavailable
	}
	if user.ID != r.Owner || key.ID != r.KeyID || key.OrganizationID != r.Organization || key.Purpose != Purpose || len(key.Scopes) != 1 || key.Scopes[0] != apikey.Generation {
		return apikey.Key{}, "", ErrUnavailable
	}
	// Authentication's projection is intentionally small. Return the actual owned
	// metadata (including expiry and provenance), never fabricated key details.
	keys, err := m.Keys.List(ctx, user.ID)
	if err != nil {
		return apikey.Key{}, "", ErrUnavailable
	}
	for _, current := range keys {
		if current.ID == key.ID {
			if current.RevokedAt != nil || current.ExpiresAt != nil && !current.ExpiresAt.After(time.Now()) {
				return apikey.Key{}, "", ErrInactive
			}
			return current, secret, nil
		}
	}
	return apikey.Key{}, "", ErrUnavailable
}
func (m *Manager) Restore(ctx context.Context, owner, organization string) (apikey.Key, string, error) {
	if organization != "" {
		parsed, err := uuid.Parse(organization)
		if err != nil {
			return apikey.Key{}, "", apikey.ErrForbidden
		}
		organization = parsed.String()
	}
	r, err := m.Repo.PlaygroundKey(ctx, owner, organization)
	if err != nil {
		return apikey.Key{}, "", err
	}
	return m.open(ctx, r)
}
func (m *Manager) Create(ctx context.Context, owner, organization, request string) (apikey.Key, string, error) {
	if organization != "" {
		parsed, err := uuid.Parse(organization)
		if err != nil {
			return apikey.Key{}, "", apikey.ErrForbidden
		}
		organization = parsed.String()
	}
	mode := "personal"
	if organization != "" {
		mode = "shared"
	}
	key, secret, err := apikey.Generate(apikey.CreateInput{UserID: owner, OrganizationID: organization, Name: Name, Scopes: []string{apikey.Generation}, ResourceMode: mode, StorageMode: mode}, time.Now())
	if err != nil {
		return apikey.Key{}, "", ErrUnavailable
	}
	key.CreatedBy = owner
	key.Purpose = Purpose
	r := Record{Owner: owner, Organization: organization, KeyID: key.ID}
	encrypted, err := m.seal(r, secret)
	if err != nil {
		return apikey.Key{}, "", err
	}
	r, err = m.Repo.CreatePlaygroundKey(ctx, owner, organization, request, key, apikey.Hash(secret), encrypted)
	if err != nil {
		return apikey.Key{}, "", err
	}
	return m.open(ctx, r)
}
