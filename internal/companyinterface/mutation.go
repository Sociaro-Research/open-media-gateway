package companyinterface

import "context"

// MutationAuthority is server-only evidence from the authenticated adapter. It
// is never decoded from HTTP, serialized to a browser, or reused by workers.
// Hashes bind original proofs; they are not bearer credentials.
type MutationAuthority struct {
	Original, Effective                                     Principal `json:"-"`
	InstallationHash, AccessHash, KeyHash, LocalSessionHash [32]byte  `json:"-"`
	KeyPresent                                              bool      `json:"-"`
	Scope, Operation                                        string    `json:"-"`
}
type mutationAuthorityKey struct{}

func WithMutationAuthority(ctx context.Context, original, effective Principal, credentials Credentials, scope, operation string) context.Context {
	original = copyMutationPrincipal(original)
	effective = copyMutationPrincipal(effective)
	a := MutationAuthority{Original: original, Effective: effective, InstallationHash: Digest(credentials.Installation), AccessHash: Digest(credentials.Session), KeyHash: Digest(credentials.Key), LocalSessionHash: credentials.LocalSessionHash, KeyPresent: credentials.KeyPresent, Scope: scope, Operation: operation}
	return context.WithValue(ctx, mutationAuthorityKey{}, a)
}
func MutationAuthorityFromContext(ctx context.Context) (MutationAuthority, bool) {
	a, ok := ctx.Value(mutationAuthorityKey{}).(MutationAuthority)
	a.Original = copyMutationPrincipal(a.Original)
	a.Effective = copyMutationPrincipal(a.Effective)
	return a, ok
}

func copyMutationPrincipal(p Principal) Principal {
	p.Scopes = append([]string(nil), p.Scopes...)
	if p.Grant.ExpiresAt != nil {
		expires := *p.Grant.ExpiresAt
		p.Grant.ExpiresAt = &expires
	}
	return p
}
