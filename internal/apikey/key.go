// Package apikey defines personal Gateway credentials. Only a SHA-256 digest
// of a cryptographically random secret is used for authentication. Only explicitly
// managed Playground keys additionally have an encrypted account vault record.
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

const (
	OrganizationsManage = "organizations:manage"
	SystemManage        = "system:manage"
	Generation          = "generation"
	ProvidersManage     = "providers:manage"
	StorageManage       = "storage:manage"
	KeysManage          = "keys:manage"
)

var ErrForbidden = errors.New("API key management is not allowed for this role or recipient")

var ErrNotFound = errors.New("API key not found")

type Key struct {
	Purpose           string     `json:"purpose,omitempty"`
	UserID            string     `json:"user_id"`
	UserEmail         string     `json:"user_email,omitempty"`
	CreatedBy         string     `json:"created_by"`
	CreatorEmail      string     `json:"creator_email,omitempty"`
	OrganizationRole  string     `json:"-"`
	OrganizationID    string     `json:"organization_id,omitempty"`
	OrganizationAdmin bool       `json:"-"`
	ResourceMode      string     `json:"resource_mode"`
	StorageMode       string     `json:"-"`
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Prefix            string     `json:"prefix"`
	Scopes            []string   `json:"scopes"`
	CreatedAt         time.Time  `json:"created_at"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
}

// MonthlyUsage attributes tasks to their creation month in UTC. Unknown costs
// remain explicit and are never counted as free provider usage.
type MonthlyUsage struct {
	HideSettlement    bool      `json:"-"`
	ConsumedUSD       money.USD `json:"consumed_usd"`
	PackageCoveredUSD money.USD `json:"package_covered_usd"`
	ActualUSD         money.USD `json:"actual_usd"`
	ExpectedUSD       money.USD `json:"expected_usd"`
	UnconfirmedTasks  int64     `json:"unconfirmed_tasks"`
	KeyID             string    `json:"api_key_id"`
	Month             string    `json:"month"`
	EstimatedCostUSD  float64   `json:"estimated_cost_usd"`
	Tasks             int64     `json:"tasks"`
	PendingTasks      int64     `json:"pending_tasks"`
	UnknownCostTasks  int64     `json:"unknown_cost_tasks"`
}

type CreateInput struct {
	UserID         string     `json:"user_id,omitempty"`
	OrganizationID string     `json:"organization_id,omitempty"`
	ResourceMode   string     `json:"resource_mode,omitempty"`
	StorageMode    string     `json:"-"`
	Name           string     `json:"name"`
	Scopes         []string   `json:"scopes,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

type Repository interface {
	Create(context.Context, string, Key, []byte) error
	List(context.Context, string) ([]Key, error)
	Usage(context.Context, string, time.Time) ([]MonthlyUsage, error)
	Revoke(context.Context, string, string) error
	Authenticate(context.Context, []byte) (identity.User, Key, error)
}

func Generate(input CreateInput, now time.Time) (Key, string, error) {
	if input.ResourceMode == "" {
		input.ResourceMode = "personal"
	}
	if input.StorageMode == "" {
		input.StorageMode = "personal"
	}
	if (input.ResourceMode != "personal" && input.ResourceMode != "shared") || (input.StorageMode != "personal" && input.StorageMode != "shared") {
		return Key{}, "", errors.New("resource_mode and storage_mode must be personal or shared")
	}

	name := strings.TrimSpace(input.Name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return Key{}, "", errors.New("name must contain 1 to 80 characters")
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return Key{}, "", errors.New("expires_at must be in the future")
	}
	if len(input.Scopes) == 0 {
		input.Scopes = []string{Generation}
	}
	scopes := make([]string, 0, 3)
	for _, scope := range input.Scopes {
		if scope != OrganizationsManage && scope != Generation && scope != ProvidersManage && scope != KeysManage && scope != StorageManage && scope != SystemManage {
			return Key{}, "", errors.New("unknown scope: " + scope)
		}
		if !slices.Contains(scopes, scope) {
			scopes = append(scopes, scope)
		}
	}
	if !slices.Contains(scopes, Generation) {
		return Key{}, "", errors.New("generation scope is required")
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return Key{}, "", err
	}
	secret := "gw_" + base64.RawURLEncoding.EncodeToString(entropy[:])
	return Key{UserID: input.UserID, OrganizationID: input.OrganizationID, ResourceMode: input.ResourceMode, StorageMode: input.StorageMode, ID: uuid.NewString(), Name: name, Prefix: secret[:11], Scopes: scopes, CreatedAt: now.UTC(), ExpiresAt: input.ExpiresAt}, secret, nil
}

func Hash(secret string) []byte {
	hash := sha256.Sum256([]byte(secret))
	return hash[:]
}

// Financial settlement belongs to organization administrators, not consuming keys.
func (u MonthlyUsage) MarshalJSON() ([]byte, error) {
	type plain MonthlyUsage
	if !u.HideSettlement {
		return json.Marshal(plain(u))
	}
	b, e := json.Marshal(plain(u))
	if e != nil {
		return nil, e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(b, &fields); e != nil {
		return nil, e
	}
	delete(fields, "actual_usd")
	delete(fields, "package_covered_usd")
	return json.Marshal(fields)
}
