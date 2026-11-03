package companyinterface

import "context"

// ActiveContext is server-private decrypted custody. It is never serialized to
// a browser and is valid only while the stored global/context fence still holds.
type ActiveContext struct {
	LocalSession    string
	Epoch, Revision int64
	Tokens          Tokens
}

// LocalOperation retains immutable encrypted rotation/logout intent separately
// from live context state. Logout must not erase an uncertain rotation's proof.
type LocalOperation struct {
	ID, Kind, State, CentralOrigin, GrantID, Actor string
	Epoch, Revision, GrantEpoch, SessionEpoch      int64
	Request                                        Refresh
}

// ActiveStore persists operation intent before network I/O and commits adoption
// with exact actor/family/version/context CAS. No transaction spans remote I/O.
type ActiveStore interface {
	LocalActive(context.Context, string) (ActiveContext, error)
	BeginLocalRotation(context.Context, ActiveContext) (LocalOperation, error)
	CommitLocalRotation(context.Context, LocalOperation, Tokens) error
	BeginLocalLogout(context.Context, string, int64) (LocalOperation, error)
	SettleLocalLogout(context.Context, LocalOperation, FamilyRevocation) error
	CheckLocalEpoch(context.Context, string, int64, int64, int64, string, string) error
}

// FamilyRevocation is an authenticated terminal acknowledgement, not an actor
// credential. Its hash only binds the request; the request still proves both
// installation credential and the original high-entropy refresh secret.
type FamilyRevocation struct {
	GrantID, SessionID, LocalOrigin string
	GrantEpoch, Version             int64
	LocalSessionHash, RequestHash   [32]byte
	Revoked                         bool
}

func (c *Coordinator) activeStore() (ActiveStore, error) {
	s, ok := c.store.(ActiveStore)
	if !ok {
		return nil, ErrUnavailable
	}
	return s, nil
}

// RefreshSession never retries an uncertain mutation with a fresh operation ID.
// Concurrent callers recover the same committed operation or get a conflict;
// neither path revokes a healthy family.
func (c *Coordinator) RefreshSession(ctx context.Context, local string, epoch, version int64) (ActiveContext, error) {
	var empty ActiveContext
	store, e := c.activeStore()
	if e != nil {
		return empty, e
	}
	before, e := store.LocalActive(ctx, local)
	if e != nil {
		return empty, e
	}
	if before.Epoch != epoch || before.Tokens.Version != version || before.Tokens.CentralOrigin != c.central.Origin() || before.Tokens.LocalOrigin != c.own {
		return empty, ErrConflict
	}
	op, e := store.BeginLocalRotation(ctx, before)
	if e != nil {
		return empty, e
	}
	result, e := c.central.Refresh(ctx, op.Request)
	if e != nil {
		return empty, e
	}
	if e = store.CommitLocalRotation(ctx, op, result); e != nil {
		current, err := store.LocalActive(ctx, local)
		if err != nil || current.Epoch != before.Epoch || current.Revision != before.Revision || current.Tokens.Actor != before.Tokens.Actor || current.Tokens.SessionID != before.Tokens.SessionID || current.Tokens.Version != result.Version || current.Tokens.AccessToken != result.AccessToken || current.Tokens.RefreshToken != result.RefreshToken {
			return empty, e
		}
		return current, nil
	}
	return store.LocalActive(ctx, local)
}
func (c *Coordinator) LogoutSession(ctx context.Context, local string, epoch int64) error {
	store, e := c.activeStore()
	if e != nil {
		return e
	}
	op, e := store.BeginLocalLogout(ctx, local, epoch)
	if e != nil {
		return e
	}
	if op.CentralOrigin != c.central.Origin() {
		return ErrConflict
	}
	// Use the original persisted origin/grant/family, not current-global state.
	result, e := c.central.RevokeFamilyProof(ctx, op.Request)
	if e != nil {
		return e
	}
	return store.SettleLocalLogout(ctx, op, result)
}
