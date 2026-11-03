package identity

import "context"

type systemPermissionKey struct{}

// WithSystemPermission carries the authenticated credential's permission, not
// the user's role. Repositories must still verify the current account role.
func WithSystemPermission(ctx context.Context, allowed bool) context.Context {
	if !allowed || APIKeyID(ctx) == nil {
		return context.WithValue(ctx, systemPermissionKey{}, "")
	}
	return context.WithValue(ctx, systemPermissionKey{}, *APIKeyID(ctx))
}

// SystemPermissionAllowed preserves browser/internal account authorization.
// API keys fail closed unless permission was granted for this exact credential.
func SystemPermissionAllowed(ctx context.Context) bool {
	if CompanyBoundary(ctx) != "" {
		return false
	}
	id := APIKeyID(ctx)
	if id == nil {
		return true
	}
	granted, _ := ctx.Value(systemPermissionKey{}).(string)
	return granted == *id
}

// WithCompanyBoundary is a server-internal ceiling established only after
// delegated installation/user/key verification. It never grants membership or
// replaces the effective actor; repository checks still resolve live roles.
type companyBoundaryKey struct{}

func WithCompanyBoundary(ctx context.Context, company string) context.Context {
	return context.WithValue(ctx, companyBoundaryKey{}, company)
}
func CompanyBoundary(ctx context.Context) string {
	v, _ := ctx.Value(companyBoundaryKey{}).(string)
	return v
}
