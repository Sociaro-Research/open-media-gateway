package companyinterface

import (
	"context"
	"errors"
	"net/url"

	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/google/uuid"
)

// LocalStore is server-private encrypted custody, never browser-submitted state.
type LocalStore interface {
	LocalPreparation(context.Context, string, string) (Start, error)
	CoordinatorPending(context.Context, string) (Start, int64, bool, error)
	BeginLocal(context.Context, Start) (int64, error)
	ActivateLocal(context.Context, Start, int64, Tokens) error
	ClearLocal(context.Context, string, int64) error
	RecoverLocalAttempt(context.Context, string) (LocalRecovery, error)
	PreserveRecoveredInstallation(context.Context, LocalRecovery, Reconciliation, LocalInstallation) error
	SettleLocalRecovery(context.Context, LocalRecovery, Reconciliation) error
}

// Coordinator owns the fixed central client. No method accepts a remote URL,
// credential, asserted central actor or reconciliation result from the browser.
type Coordinator struct {
	store      LocalStore
	central    *Client
	own, label string
	protocol   func() Protocol
}

func NewCoordinator(store LocalStore, central *Client, own string) (*Coordinator, error) {
	normalized, e := installation.NormalizeOrigin(own)
	if e != nil || normalized != own || store == nil || central == nil || own == central.Origin() {
		return nil, ErrDenied
	}
	u, _ := url.Parse(own)
	label := u.Host
	if len(label) > 80 {
		label = label[:80]
	}
	return &Coordinator{store: store, central: central, own: own, label: label, protocol: ImplementedProtocol}, nil
}
func (c *Coordinator) LocalOrigin() string   { return c.own }
func (c *Coordinator) CentralOrigin() string { return c.central.Origin() }
func (c *Coordinator) ready(ctx context.Context) error {
	// Both checks precede even installation-proof creation. A partial/old central
	// cannot leave a pending connection or change the existing local mode.
	if e := c.central.RequireFullSurface(ctx); e != nil {
		return e
	}
	return Negotiate(c.protocol())
}
func (c *Coordinator) Begin(ctx context.Context, owner, local, mode string) (Challenge, error) {
	if !ValidSecret(local) {
		return Challenge{}, ErrDenied
	}
	switch mode {
	case "connect", "login", "legacy-upgrade", "rebind":
	default:
		return Challenge{}, ErrDenied
	}
	if e := c.ready(ctx); e != nil {
		return Challenge{}, e
	}
	in, _, exists, e := c.store.CoordinatorPending(ctx, local)
	if e != nil {
		return Challenge{}, e
	}
	if exists {
		if in.Mode != mode || in.LocalOrigin != c.own {
			return Challenge{}, ErrConflict
		}
		recovery, err := c.store.RecoverLocalAttempt(ctx, in.AttemptID)
		if err != nil {
			return Challenge{}, err
		}
		if recovery.CentralOrigin != c.central.Origin() || recovery.State != "pending" {
			return Challenge{}, ErrConflict
		}
		return c.central.Start(ctx, in)
	}
	in, e = c.store.LocalPreparation(ctx, owner, mode)
	if e != nil {
		return Challenge{}, e
	}
	in.AttemptID = uuid.NewString()
	in.Proof, e = NewSecret()
	if e != nil {
		return Challenge{}, e
	}
	in.LocalSession = local
	in.LocalOrigin = c.own
	in.Label = c.label
	in.Mode = mode
	in.Protocol = c.protocol()
	if _, e = c.store.BeginLocal(ctx, in); e != nil {
		return Challenge{}, e
	}
	// A failed/uncertain reply never deletes this committed pending request.
	return c.central.Start(ctx, in)
}
func redemption(in Start) Redemption {
	return Redemption{AttemptID: in.AttemptID, OperationID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("company-interface/redeem/v1/"+in.AttemptID)).String(), InstallationProof: in.InstallationProof, Proof: in.Proof, LocalSession: in.LocalSession, LocalOrigin: in.LocalOrigin, ConnectionRevision: in.ConnectionRevision}
}
func (c *Coordinator) pending(ctx context.Context, local, id string) (Start, int64, error) {
	if !ValidSecret(local) || !ValidID(id) {
		return Start{}, 0, ErrDenied
	}
	in, epoch, exists, e := c.store.CoordinatorPending(ctx, local)
	if e != nil {
		return in, epoch, e
	}
	if !exists || in.AttemptID != id {
		return Start{}, 0, ErrConflict
	}
	recovery, e := c.store.RecoverLocalAttempt(ctx, id)
	if e != nil {
		return Start{}, 0, e
	}
	if recovery.CentralOrigin != c.central.Origin() || in.LocalOrigin != c.own {
		return Start{}, 0, ErrConflict
	}
	return in, epoch, nil
}
func (c *Coordinator) Poll(ctx context.Context, local, id string) error {
	if e := c.ready(ctx); e != nil {
		return e
	}
	in, epoch, e := c.pending(ctx, local, id)
	if e != nil {
		return e
	}
	recovery, e := c.store.RecoverLocalAttempt(ctx, id)
	if e != nil {
		return e
	}
	if recovery.State != "pending" {
		return ErrConflict
	}
	result, e := c.central.Redeem(ctx, redemption(in))
	if e != nil {
		return e
	}
	return c.store.ActivateLocal(ctx, in, epoch, result)
}

// Cancel invalidates the local pending context before remote cleanup. Unknown
// outcomes retain proof; typed confirmation can only come from the fixed client.
// Cleanup remains possible when capability negotiation no longer succeeds.
func (c *Coordinator) Cancel(ctx context.Context, local, id string) error {
	in, epoch, e := c.pending(ctx, local, id)
	if e != nil {
		return e
	}
	recovery, e := c.store.RecoverLocalAttempt(ctx, id)
	if e != nil {
		return e
	}
	if recovery.State == "pending" {
		if e = c.store.ClearLocal(ctx, local, epoch); e != nil {
			return e
		}
	}
	request := redemption(in)
	// Cancel is idempotent. An already approved operation requires reconciliation;
	// an unavailable reply cannot justify throwing away its original proof.
	if e = c.central.Cancel(ctx, request); e != nil && !errors.Is(e, ErrConflict) && !errors.Is(e, ErrDenied) {
		return e
	}
	confirmation, e := c.central.Reconcile(ctx, request)
	if e != nil {
		return e
	}
	if confirmation.State == "approved" && !confirmation.SessionRevoked {
		if e = c.central.RevokeIssuedFamily(ctx, request); e != nil {
			return e
		}
		confirmation, e = c.central.Reconcile(ctx, request)
		if e != nil {
			return e
		}
	}
	if confirmation.State == "approved" && !confirmation.GrantRevoked && in.Mode != "login" {
		recovered, err := c.central.RecoverInstallation(ctx, request)
		if err != nil {
			return err
		}
		if e = c.store.PreserveRecoveredInstallation(ctx, recovery, confirmation, recovered); e != nil {
			return e
		}
	}
	// Login cannot recover an installation secret. Settlement verifies the
	// independently encrypted, already durable exact same-grant custody itself.
	// Only an authenticated confirmation of the exact terminal family/grant
	// outcome can settle proof custody. No user credentials leave the server.
	return c.store.SettleLocalRecovery(ctx, recovery, confirmation)
}
