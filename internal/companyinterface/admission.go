package companyinterface

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

// Authority is central persistence, never a local user/key database. Each method
// must read current state. No admission result may be reused for another request.
type Authority interface {
	Installation(context.Context, [32]byte) (Grant, error)
	Session(context.Context, [32]byte) (Session, error)
	Membership(context.Context, string, string) (string, error)
}

// CentralKeys validates the original key centrally, including expiry, revocation
// and current scopes. Subject is the key's actual user/recipient (user_id), never
// its created_by user. Installation credentials are not generation API keys.
type CentralKeys interface {
	AuthenticateCompanyKey(context.Context, string, string) (KeyIdentity, error)
}
type KeyIdentity struct {
	ID, Company, Subject string
	Scopes               []string
}

// RecipientPolicy applies the existing direct-central policy to a verified
// browser actor selecting a different key recipient. It must read live policy;
// the installation grant and local administrator convey no recipient authority.
type RecipientPolicy interface {
	AuthorizeKeyRecipient(context.Context, string, string, string) error
}

type Credentials struct {
	Installation     string
	Session          string
	LocalSessionHash [32]byte
	// KeyPresent records header presence independently of validity or emptiness.
	// A malformed/empty explicit key must never fall back to a browser session.
	KeyPresent bool
	Key        string
}

type Principal struct {
	Grant Grant
	Kind  AuthKind
	// Actor/Recipient/Role are the effective central subject, never key creator.
	// BrowserActor separately preserves the verified browser initiator for audit.
	Actor, Recipient, Role, BrowserActor, BrowserRole, KeyID, SessionID string
	SessionEpoch, SessionVersion                                        int64
	Scopes                                                              []string
}

// CentralOrigin comes from trusted central deployment configuration, never Host,
// Forwarded, a request body or the grant itself. An unconfigured gate denies all.
type Gate struct {
	Authority     Authority
	Keys          CentralKeys
	Recipients    RecipientPolicy
	CentralOrigin string
}

// AuthenticateBrowser always requires a verified central user session, including
// when generation presents an explicit API key. An invalid key cannot fall back
// to session authority. Browser routing must choose this entrypoint itself.
func (g Gate) AuthenticateBrowser(ctx context.Context, c Credentials, now time.Time) (Principal, error) {
	return g.authenticate(ctx, c, now, true)
}

// AuthenticateSDK validates the original central key independently of any local
// key database. This entrypoint is only for the trusted API/SDK route composition;
// a caller-controlled header/query cannot select SDK mode on a browser route.
func (g Gate) AuthenticateSDK(ctx context.Context, c Credentials, now time.Time) (Principal, error) {
	if !c.KeyPresent {
		return Principal{}, ErrDenied
	}
	return g.authenticate(ctx, c, now, false)
}

func (g Gate) authenticate(ctx context.Context, c Credentials, now time.Time, browser bool) (Principal, error) {
	deny := func() (Principal, error) { return Principal{}, ErrDenied }
	if g.Authority == nil || len(c.Installation) < 32 || len(c.Installation) > 512 {
		return deny()
	}
	grant, err := g.Authority.Installation(ctx, sha256.Sum256([]byte(c.Installation)))
	if err != nil || grant.Validate(now) != nil || g.CentralOrigin == "" || grant.CentralOrigin != g.CentralOrigin {
		return deny()
	}
	p := Principal{Grant: grant}
	if browser {
		if len(c.Session) < 32 || len(c.Session) > 512 {
			return deny()
		}
		s, err := g.Authority.Session(ctx, sha256.Sum256([]byte(c.Session)))
		if err != nil || s.Validate(grant, c.LocalSessionHash, now) != nil {
			return deny()
		}
		role, err := g.Authority.Membership(ctx, grant.Company, s.Actor)
		if err != nil || !organization.ValidRole(role) {
			return deny()
		}
		p.Kind, p.Actor, p.Recipient, p.BrowserActor, p.Role, p.SessionID = CentralSession, s.Actor, s.Actor, s.Actor, role, s.ID
		p.BrowserRole = role
		p.SessionEpoch, p.SessionVersion = s.Epoch, s.Version
	}
	if c.KeyPresent {
		if g.Keys == nil || len(c.Key) < 16 || len(c.Key) > 512 {
			return deny()
		}
		key, err := g.Keys.AuthenticateCompanyKey(ctx, c.Key, grant.Company)
		if err != nil || key.Company != grant.Company || !validID(key.ID) || !validID(key.Subject) {
			return deny()
		}
		role, err := g.Authority.Membership(ctx, grant.Company, key.Subject)
		if err != nil || !organization.ValidRole(role) {
			return deny()
		}
		if browser && p.BrowserActor != key.Subject {
			if g.Recipients == nil || g.Recipients.AuthorizeKeyRecipient(ctx, grant.Company, p.BrowserActor, key.Subject) != nil {
				return deny()
			}
		}
		p.Kind, p.Actor, p.Recipient, p.Role, p.KeyID = CentralKey, key.Subject, key.Subject, role, key.ID
		p.Scopes = append([]string(nil), key.Scopes...)
	}
	return p, nil
}
