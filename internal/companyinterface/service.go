package companyinterface

import (
	"context"
	"time"
)

// Lifecycle is the central implementation boundary. HTTP adapters supply a
// trusted central browser identity in context only for browser operations.
type Lifecycle interface {
	Start(context.Context, Start) (Challenge, error)
	Describe(context.Context, string) (Challenge, Attempt, error)
	Approve(context.Context, Approval) (Challenge, error)
	Redeem(context.Context, Redemption) (Tokens, error)
	Refresh(context.Context, Refresh) (Tokens, error)
	Cancel(context.Context, Redemption) error
	Reconcile(context.Context, Redemption) (Reconciliation, error)
	RecoverInstallation(context.Context, Redemption) (LocalInstallation, error)
	Revoke(context.Context, string) error
	Logout(context.Context, Credentials) error
	RevokeIssuedFamily(context.Context, Redemption) error
	RevokeFamilyProof(context.Context, Refresh) (FamilyRevocation, error)
	AllowAttempt(context.Context, []byte, int, time.Duration) (bool, error)
}

// No full-surface capability may be inferred from the existence of auth routes.
// This current composition deliberately fails RequireFullSurface before local
// mode transitions; later implementation must earn every required capability.
func ImplementedProtocol() Protocol {
	return Protocol{Purpose: Purpose, Version: Version, Capabilities: []string{"authorization-transport/v1"}}
}
