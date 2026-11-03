package companyinterface

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"time"
)

var ErrConflict = errors.New("company interface operation superseded")
var ErrPending = errors.New("company interface approval pending")

const ChallengeLifetime = 10 * time.Minute
const RecoveryLifetime = 7 * 24 * time.Hour
const AccessLifetime = 15 * time.Minute
const RefreshLifetime = 30 * 24 * time.Hour

// Start is a server-to-server request. Proofs and secrets must never enter a URL,
// browser response or log. Mode is an explicitly disclosed approval intention.
type Start struct {
	AttemptID, InstallationProof, Proof, LocalSession, LocalOrigin, Label string
	Mode, PreviousGrantID, GrantCredential                                string
	ConnectionRevision                                                    int64
	Protocol                                                              Protocol
}

// Attempt is the durable, non-secret binding. Its fingerprint also separates
// attempts, origins, local browser sessions and connection revisions.
type Attempt struct {
	ID, LocalOrigin, CentralOrigin, Label, Mode, PreviousGrantID string
	InstallationHash, ProofHash, LocalSessionHash, GrantHash     [32]byte
	ConnectionRevision                                           int64
	Purpose                                                      string
	Version                                                      int
}

type Challenge struct {
	ID, Code, State string
	ExpiresAt       time.Time
}
type Approval struct {
	OperationID, Code string
	Consent           bool
}
type Redemption struct {
	AttemptID, OperationID, InstallationProof, Proof, LocalSession, LocalOrigin string
	ConnectionRevision                                                          int64
}
type Refresh struct {
	OperationID, GrantCredential, RefreshToken, LocalSession, LocalOrigin, SessionID string
	Version                                                                          int64
}

// Tokens is only for an authenticated server-to-server response, then encrypted
// local persistence. Browser approval receives a Challenge, never this object.
type Tokens struct {
	AttemptID, GrantID, Company, Actor, SessionID, LocalOrigin, CentralOrigin string
	InstallationCredential, AccessToken, RefreshToken, PlaygroundKey          string
	InstallationHash, LocalSessionHash                                        [32]byte
	ConnectionRevision, GrantEpoch, SessionEpoch, Version                     int64
	AccessExpiresAt, RefreshExpiresAt                                         time.Time
}

func Digest(s string) [32]byte   { return sha256.Sum256([]byte(s)) }
func Fingerprint(v any) [32]byte { b, _ := json.Marshal(v); return sha256.Sum256(b) }
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func ValidID(s string) bool { return validID(s) }
func ValidSecret(s string) bool {
	b, e := base64.RawURLEncoding.DecodeString(s)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

// ReceiptCipher has an independent purpose domain. AAD binds exact operation and
// request fingerprint; swapping or modifying a stored receipt fails closed.
type ReceiptCipher struct{ aead cipher.AEAD }

func NewReceiptCipher(master string) (*ReceiptCipher, error) {
	if len(master) < 16 {
		return nil, ErrDenied
	}
	sum := Digest("open-media-gateway:company-interface:receipts:v1\x00" + master)
	block, e := aes.NewCipher(sum[:])
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	return &ReceiptCipher{a}, nil
}
func (c *ReceiptCipher) Seal(v any, binding any) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	aad, _ := json.Marshal(binding)
	n := make([]byte, c.aead.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	return c.aead.Seal(n, n, raw, aad), nil
}
func (c *ReceiptCipher) Open(encrypted []byte, binding any, v any) error {
	n := c.aead.NonceSize()
	if len(encrypted) <= n {
		return ErrDenied
	}
	aad, _ := json.Marshal(binding)
	raw, e := c.aead.Open(nil, encrypted[:n], encrypted[n:], aad)
	if e != nil {
		return ErrDenied
	}
	if json.Unmarshal(raw, v) != nil {
		return ErrDenied
	}
	return nil
}

// LocalInstallation is server-private installation-wide state. Per-user logout
// must never delete its permanent credential; it authenticates no actor.
type LocalInstallation struct {
	GrantID, Company, CentralOrigin, LocalOrigin, Credential string
	InstallationHash                                         [32]byte
	GrantEpoch                                               int64
}

// InstallationDigest preserves the durable identity used by the old protocol.
// Credential/proof/session hashes use separate domains and are not interchangeable.
func InstallationDigest(proof string) [32]byte {
	var sum [32]byte
	copy(sum[:], installation.ProofDigest(proof))
	return sum
}

// LocalRecovery is server-private custody of one uncertain attempt. Its original
// central destination/epoch survive logout and never authorize live activation.
type LocalRecovery struct {
	Request       Start
	CentralOrigin string
	ContextEpoch  int64
	State         string
}

// Reconciliation is an authenticated server-to-server result, never a browser
// assertion. It distinguishes one issued family's logout from grant revocation.
// It intentionally has no actor, access/refresh token or generation key.
type Reconciliation struct {
	Binding                            Attempt
	Fingerprint                        [32]byte
	State, GrantID, Company, SessionID string
	GrantEpoch                         int64
	GrantRevoked, SessionRevoked       bool
}

func BindStart(central string, in Start) Attempt {
	return Attempt{ID: in.AttemptID, LocalOrigin: in.LocalOrigin, CentralOrigin: central, Label: in.Label, Mode: in.Mode, PreviousGrantID: in.PreviousGrantID, InstallationHash: InstallationDigest(in.InstallationProof), ProofHash: Digest(in.Proof), LocalSessionHash: Digest(in.LocalSession), GrantHash: Digest(in.GrantCredential), ConnectionRevision: in.ConnectionRevision, Purpose: Purpose, Version: Version}
}
