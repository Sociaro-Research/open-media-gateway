// Package system defines installation-wide settings and administration.
package system

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"time"
)

var ErrForbidden = errors.New("administrator access is required")
var ErrRoleConflict = errors.New("remove administrator and manager roles before assigning the client manager role; remove the client manager role before granting administrator or manager access")

var ErrLastAdmin = errors.New("the last administrator cannot be removed")
var ErrNotFound = errors.New("user not found")
var ErrConflict = errors.New("settings changed; reload before saving")
var ErrBootstrapped = errors.New("an administrator already exists; use admin grant for server-side recovery")

type SettingsReader interface {
	Settings(context.Context) (Settings, error)
}

type Settings struct {
	MarkupPercent           money.Percent `json:"markup_percent"`
	AllowInlineReferences   bool          `json:"allow_inline_references"`
	SharedStorageLimitBytes *int64        `json:"shared_storage_limit_bytes"`
	SharedResources         bool          `json:"shared_resources"`
	GenerationUI            bool          `json:"generation_ui"`
	PersonalProviders       bool          `json:"personal_providers"`
	Version                 int64         `json:"version"`
}

func Defaults() Settings {
	return Settings{AllowInlineReferences: true, GenerationUI: true, PersonalProviders: true, Version: 1}
}
func (s Settings) Validate() error {
	if err := s.MarkupPercent.Validate(); err != nil {
		return err
	}
	if s.SharedStorageLimitBytes != nil && (*s.SharedStorageLimitBytes < 0 || *s.SharedStorageLimitBytes > 1000000000000000) {
		return errors.New("storage limit must be between 0 and 1000000000000000 bytes")
	}
	if s.Version < 1 {
		return errors.New("version is required")
	}
	return nil
}

type ClientManagers interface {
	IsClientManager(context.Context, string) (bool, error)
	SetClientManager(context.Context, string, string, bool) error
	AssignClientManager(context.Context, string, string, string) error
}

type User struct {
	Picture string `json:"picture,omitempty"`

	ClientManager bool      `json:"is_client_manager"`
	SalesRole     string    `json:"sales_role,omitempty"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	Admin         bool      `json:"is_system_admin"`
	CreatedAt     time.Time `json:"created_at"`
}
type Repository interface {
	Settings(context.Context) (Settings, error)
	IsAdmin(context.Context, string) (bool, error)
	Users(context.Context, string, bool, int, int) ([]User, error)
	SetAdmin(context.Context, string, string, bool, bool) error
	SaveSettings(context.Context, string, Settings) (Settings, error)
	ImportShared(context.Context, string) error
}

// Public omits billing controls from the unauthenticated console policy.
func (s Settings) Public() map[string]any {
	b, _ := json.Marshal(s)
	var result map[string]any
	_ = json.Unmarshal(b, &result)
	delete(result, "markup_percent")
	return result
}
