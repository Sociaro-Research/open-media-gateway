package identity

import (
	"context"
	"github.com/google/uuid"
)

const SystemOwnerID = "00000000-0000-0000-0000-000000000000"

type User struct {
	Contacts      []Contact `json:"contacts"`
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	Theme         string    `json:"theme,omitempty"`
	ColorPreset   string    `json:"color_preset"`
	ReportColumns []string  `json:"report_columns,omitempty"`
	Picture       string    `json:"picture,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
}

type contextKey struct{}

func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, contextKey{}, user)
}

func WithOwnerID(ctx context.Context, id string) context.Context {
	return WithUser(ctx, User{ID: id})
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(contextKey{}).(User)
	return user, ok && user.ID != ""
}

func OwnerID(ctx context.Context) string {
	if o := OrganizationFromContext(ctx); o.ID != "" {
		return o.ID
	}
	if id, ok := ctx.Value(dataOwnerKey{}).(string); ok {
		return id
	}
	if user, ok := UserFromContext(ctx); ok {
		return user.ID
	}
	return SystemOwnerID
}

// APIKeyID identifies the credential that initiated a request, independently of ownership.
type apiKeyContextKey struct{}

func WithAPIKeyID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, apiKeyContextKey{}, id)
}

func APIKeyID(ctx context.Context) *string {
	id, ok := ctx.Value(apiKeyContextKey{}).(string)
	if !ok || id == "" {
		return nil
	}
	return &id
}

// SharedOwnerID owns installation resources, never a user's data or credentials.
const SharedOwnerID = "00000000-0000-0000-0000-000000000001"

type resourceContextKey struct{}
type ResourceOwners struct{ Provider, Storage string }

func WithResourceOwners(ctx context.Context, provider, storage string) context.Context {
	return context.WithValue(ctx, resourceContextKey{}, ResourceOwners{Provider: provider, Storage: storage})
}
func ProviderOwnerID(ctx context.Context) string {
	if r, ok := ctx.Value(resourceContextKey{}).(ResourceOwners); ok && r.Provider != "" {
		return r.Provider
	}
	return OwnerID(ctx)
}
func StorageOwnerID(ctx context.Context) string {
	if r, ok := ctx.Value(resourceContextKey{}).(ResourceOwners); ok && r.Storage != "" {
		return r.Storage
	}
	return OwnerID(ctx)
}

// Organization separates the authenticated actor from the owner of data and costs.
type organizationKey struct{}
type Organization struct {
	ID    string
	Role  string
	Admin bool
}

func WithOrganization(ctx context.Context, id string, admin bool) context.Context {
	return context.WithValue(ctx, organizationKey{}, Organization{ID: id, Admin: admin})
}
func OrganizationFromContext(ctx context.Context) Organization {
	o, _ := ctx.Value(organizationKey{}).(Organization)
	return o
}
func ActorID(ctx context.Context) string {
	if u, ok := UserFromContext(ctx); ok {
		return u.ID
	}
	return SystemOwnerID
}

// FinancialAuthorFilter restricts member financial reports, not shared tasks or files.
func FinancialAuthorFilter(ctx context.Context) string {
	o := OrganizationFromContext(ctx)
	if o.ID != "" && !o.Admin {
		return ActorID(ctx)
	}
	return ""
}

// Personal data uses a separate namespace from both user credentials and legacy organizations.
type dataOwnerKey struct{}

func PersonalOwnerID(user string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("media-gateway/personal/"+user)).String()
}
func WithPersonalData(ctx context.Context) context.Context {
	return context.WithValue(ctx, dataOwnerKey{}, PersonalOwnerID(ActorID(ctx)))
}

// Role-bearing contexts are used at the authenticated boundary; worker contexts remain unrestricted.
func WithOrganizationRole(ctx context.Context, id, role string) context.Context {
	return context.WithValue(ctx, organizationKey{}, Organization{ID: id, Role: role, Admin: role == "system_admin" || role == "supermanager" || role == "admin" || role == "content_officer" || role == "client_manager"})
}
func ContentAuthorFilter(ctx context.Context) string {
	role := OrganizationFromContext(ctx).Role
	if role == "developer" || role == "member" {
		return ActorID(ctx)
	}
	return ""
}
