package remoteworkspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"time"

	"github.com/google/uuid"
)

// Recovery contains legacy-only encrypted custody. It is never an activation
// input and is never accepted from a browser or returned through public JSON.
type Recovery struct {
	Origin              string        `json:"origin"`
	OperationID         string        `json:"id"`
	Owner               string        `json:"-"`
	Attempt             Attempt       `json:"-"`
	IdentityCipher      []byte        `json:"-"`
	ExpectedVersion     int64         `json:"-"`
	ExpectedRevision    int64         `json:"-"`
	Destination         string        `json:"-"`
	Version             int64         `json:"version"`
	Revision            int64         `json:"connection_revision"`
	SavedPresent        bool          `json:"-"`
	Saved               RecoverySaved `json:"-"`
	SavedIdentityCipher []byte        `json:"-"`
	State               string        `json:"state"`
	Created             time.Time     `json:"-"`
	Confirmed           *time.Time    `json:"-"`
}

// Explicit server-private DTO avoids Record.MarshalJSON's secret-free public
// projection. Each credential is already encrypted by the legacy cipher.
type RecoverySaved struct {
	Access                                                  string
	Owner                                                   string
	Version                                                 int64
	Active                                                  bool
	Origin, OrganizationID, OrganizationName, UserID, KeyID string
	TokenHash, Ciphertext                                   []byte
	VerifiedAt                                              *time.Time
	Provisioning                                            string
	ConnectionRevision                                      int64
}

func SavedCustody(v Record) RecoverySaved {
	return RecoverySaved{v.Access, v.Owner, v.Version, v.Active, v.Origin, v.OrganizationID, v.OrganizationName, v.UserID, v.KeyID, v.TokenHash, v.Ciphertext, v.VerifiedAt, v.Provisioning, v.ConnectionRevision}
}

type RecoveryInput struct {
	OperationID string `json:"operation_id"`
	AttemptID   string `json:"id"`
	Version     int64  `json:"version"`
	Revision    int64  `json:"connection_revision"`
	Destination string `json:"-"`
}
type RecoveryRepository interface {
	QuarantineAuthorization(context.Context, string, RecoveryInput) (Recovery, error)
	LegacyRecovery(context.Context, string, string) (Recovery, error)
	SettleLegacyRecovery(context.Context, string, Recovery) error
}

func (m *Manager) QuarantineAuthorization(ctx context.Context, owner string, in RecoveryInput) (Recovery, error) {
	repo, ok := m.Repo.(RecoveryRepository)
	if !ok {
		return Recovery{}, ErrUnavailable
	}
	destination, e := Origin(in.Destination)
	if e != nil || destination != in.Destination || in.Version < 0 || in.Revision < 0 {
		return Recovery{}, ErrConflict
	}
	if _, e = uuid.Parse(in.OperationID); e != nil {
		return Recovery{}, ErrConflict
	}
	if _, e = uuid.Parse(in.AttemptID); e != nil {
		return Recovery{}, ErrConflict
	}
	return repo.QuarantineAuthorization(ctx, owner, in)
}
func (m *Manager) CleanupLegacyRecovery(ctx context.Context, owner, id string) error {
	repo, ok := m.Repo.(RecoveryRepository)
	if !ok {
		return ErrUnavailable
	}
	saved, e := repo.LegacyRecovery(ctx, owner, id)
	if e != nil {
		return e
	}
	if saved.State == "cancelled" {
		return nil
	}
	origin, e := Origin(saved.Attempt.Origin)
	if e != nil || origin != saved.Attempt.Origin {
		return ErrConflict
	}
	proof, e := m.Cipher.Decrypt(saved.Attempt.Cipher)
	if e != nil {
		return e
	}
	response, e := m.authorizationRequest(ctx, origin, "/auth/workspace/cancel", map[string]string{"device_code": string(proof)})
	if e != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return ErrUnavailable
	}
	var confirmation struct {
		Cancelled bool `json:"cancelled"`
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, MaxJSON+1))
	if e != nil || len(raw) > MaxJSON || json.Unmarshal(raw, &confirmation) != nil || !confirmation.Cancelled {
		return ErrUnavailable
	}
	return repo.SettleLegacyRecovery(ctx, owner, saved)
}

type RecoveryStatus struct {
	NextCursor      string     `json:"next_cursor,omitempty"`
	Revision        int64      `json:"current_connection_revision"`
	RecoveryAllowed bool       `json:"recovery_allowed"`
	Items           []Recovery `json:"items"`
}

func (m *Manager) RecoveryStatus(ctx context.Context, owner string) (RecoveryStatus, error) {
	repo, ok := m.Repo.(interface {
		LegacyRecoveryStatus(context.Context, string) (RecoveryStatus, error)
	})
	if !ok {
		return RecoveryStatus{}, ErrUnavailable
	}
	return repo.LegacyRecoveryStatus(ctx, owner)
}

type RecoveryCursor struct {
	Created time.Time `json:"created"`
	ID      string    `json:"id"`
}

func EncodeRecoveryCursor(v Recovery) string {
	raw, _ := json.Marshal(RecoveryCursor{Created: v.Created, ID: v.OperationID})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func DecodeRecoveryCursor(raw string) (RecoveryCursor, error) {
	var cursor RecoveryCursor
	if raw == "" {
		return cursor, nil
	}
	if len(raw) > 256 {
		return cursor, ErrRoute
	}
	body, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return cursor, ErrRoute
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Created.IsZero() {
		return RecoveryCursor{}, ErrRoute
	}
	if _, e = uuid.Parse(cursor.ID); e != nil {
		return RecoveryCursor{}, ErrRoute
	}
	return cursor, nil
}
func (m *Manager) RecoveryPage(ctx context.Context, owner, cursor string) (RecoveryStatus, error) {
	repo, ok := m.Repo.(interface {
		LegacyRecoveryPage(context.Context, string, string) (RecoveryStatus, error)
	})
	if !ok {
		return RecoveryStatus{}, ErrUnavailable
	}
	return repo.LegacyRecoveryPage(ctx, owner, cursor)
}
