// Package companyinterface defines the separate company-interface protocol.
// It is not an extension of workspaceauth's generation credential. This package
// supplies admission primitives; no portal route or connection mode uses it yet.
package companyinterface

import (
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/google/uuid"
)

const Purpose = "company-interface"
const Version = 1

var ErrProtocol = errors.New("company interface update required")
var ErrDenied = errors.New("company interface request denied")

// RequiredCapabilities returns a new slice. Implementations must not advertise
// these capabilities until their complete production paths have been verified.
// full-company-surface/v1 means ALL fourteen company surfaces: bootstrap/Profile,
// Playground, Projects/Journal, Templates, picker/resources, upload/storage,
// members/invites, keys, Requests, billing, usage/reports/audit, company settings,
// public API/SDK and every asset/media URL. This foundation implements no such
// advertisement and does not qualify for that capability.
func RequiredCapabilities() []string {
	return []string{"full-company-surface/v1", "installation-grant/v1", "central-user-session/v1", "central-api-key/v1", "approval-redemption-recovery/v1", "refresh-recovery/v1", "route-contracts/v1", "upload-bridge/v1", "payment-return/v1", "context-epoch/v1", "legacy-consent/v1"}
}

type Protocol struct {
	Purpose      string   `json:"purpose"`
	Version      int      `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// Negotiate must succeed before any activation write. There is deliberately no
// downgrade to generation-only authorization or a partially connected mode.
func Negotiate(p Protocol) error {
	if p.Purpose != Purpose || p.Version != Version {
		return ErrProtocol
	}
	seen := map[string]bool{}
	for _, c := range p.Capabilities {
		if c == "" || seen[c] {
			return ErrProtocol
		}
		seen[c] = true
	}
	for _, c := range RequiredCapabilities() {
		if !seen[c] {
			return ErrProtocol
		}
	}
	return nil
}

// Grant authenticates an installation only. There is intentionally no actor,
// role, scope, or generation API key in this record. Nil ExpiresAt is permanent;
// user access/session/key lifetimes remain independent.
type Grant struct {
	ID               string
	InstallationHash [32]byte
	Company          string
	CentralOrigin    string
	LocalOrigin      string
	Purpose          string
	Version          int
	Epoch            int64
	Revoked          bool
	ExpiresAt        *time.Time
}

func validID(s string) bool {
	id, e := uuid.Parse(s)
	return e == nil && id != uuid.Nil && id.String() == s
}

func (g Grant) Validate(now time.Time) error {
	central, e1 := installation.NormalizeOrigin(g.CentralOrigin)
	local, e2 := installation.NormalizeOrigin(g.LocalOrigin)
	if !validID(g.ID) || !validID(g.Company) || g.InstallationHash == [32]byte{} ||
		g.Purpose != Purpose || g.Version != Version || g.Epoch < 1 || g.Revoked ||
		(g.ExpiresAt != nil && !now.Before(*g.ExpiresAt)) || e1 != nil || e2 != nil ||
		central != g.CentralOrigin || local != g.LocalOrigin || len(central) < 8 || central[:8] != "https://" {
		return ErrDenied
	}
	return nil
}

type Session struct {
	ID               string
	GrantID          string
	Company          string
	Actor            string
	LocalSessionHash [32]byte
	GrantEpoch       int64
	Epoch            int64
	Version          int64
	ExpiresAt        time.Time
	Revoked          bool
}

func (s Session) Validate(g Grant, localSession [32]byte, now time.Time) error {
	if !validID(s.ID) || !validID(s.Actor) || s.GrantID != g.ID || s.Company != g.Company ||
		s.GrantEpoch != g.Epoch || s.Epoch < 1 || s.Version < 1 || s.Revoked ||
		!now.Before(s.ExpiresAt) || localSession == [32]byte{} || s.LocalSessionHash != localSession {
		return ErrDenied
	}
	return nil
}
