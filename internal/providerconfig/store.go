package providerconfig

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"
)

var ErrNotFound = errors.New("provider setting not found")

type Record struct {
	Name                  string
	Archived              bool
	Version               int64
	ProviderID            string
	Enabled               bool
	CredentialsCiphertext []byte
	CredentialSource      string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Revisions retain encrypted inference settings for accepted work across edits.
type Revisions interface {
	SaveRevision(context.Context, string, string, []byte) error
	GetRevision(context.Context, string, string) ([]byte, error)
}

type Repository interface {
	Get(context.Context, string) (Record, error)
	List(context.Context) ([]Record, error)
	Upsert(context.Context, Record) error
}

// Cipher keeps provider credentials encrypted at rest. A SHA-256 derived key
// lets operators supply a normal high-entropy secret instead of encoded bytes.
type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(secret string) (*Cipher, error) {
	if len(secret) < 16 {
		return nil, errors.New("provider credentials encryption secret must contain at least 16 characters")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create provider credentials cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create provider credentials AEAD: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate provider credentials nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (c *Cipher) Decrypt(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	nonceSize := c.aead.NonceSize()
	if len(value) <= nonceSize {
		return nil, errors.New("provider credentials ciphertext is invalid")
	}
	plaintext, err := c.aead.Open(nil, value[:nonceSize], value[nonceSize:], nil)
	if err != nil {
		return nil, errors.New("provider credentials could not be decrypted")
	}
	return plaintext, nil
}
