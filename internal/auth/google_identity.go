package auth

import (
	"errors"
	"strings"
)

var ErrGoogleAccountConflict = errors.New("Google identity cannot be linked to this account")

// GoogleIdentity contains claims from an ID token whose signature, issuer,
// audience, expiry and nonce have already been checked by the OAuth callback.
type GoogleIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	HostedDomain  string
	Name          string
	Picture       string
}

// AuthoritativeEmail follows Google's email-ownership guidance. A verified
// third-party address without a hosted domain may no longer belong to this
// Google account, so it must never authorize an email-based account link.
func (g GoogleIdentity) AuthoritativeEmail() bool {
	email := strings.ToLower(strings.TrimSpace(g.Email))
	local, domain, ok := strings.Cut(email, "@")
	if !g.EmailVerified || !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return false
	}
	return domain == "gmail.com" || strings.TrimSpace(g.HostedDomain) != ""
}
