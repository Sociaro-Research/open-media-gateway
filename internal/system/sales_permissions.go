package system

import (
	"context"
	"errors"
	"time"
)

var ErrSalesPermission = errors.New("choose a valid permission and version")

type SalesGrant struct {
	IssuerPicture string `json:"issuer_picture,omitempty"`

	ID         string    `json:"id"`
	EmployeeID string    `json:"employee_id"`
	Permission string    `json:"permission"`
	IssuerID   string    `json:"issuer_id"`
	IssuerName string    `json:"issuer_name"`
	CreatedAt  time.Time `json:"created_at"`
}
type SalesPermissions struct {
	Version       int64         `json:"version"`
	ActorID       string        `json:"actor_id"`
	Administrator bool          `json:"administrator"`
	Members       []SalesMember `json:"members"`
	Grants        []SalesGrant  `json:"grants"`
}
type SalesPermissionChange struct {
	Enabled bool  `json:"enabled"`
	Version int64 `json:"version"`
}

func SalesPermissionValid(p string) bool {
	return p == "pricing" || p == "models" || p == "topup" || p == "bonuses"
}

type SalesPermissionsRepository interface {
	SalesPermissions(context.Context, string) (SalesPermissions, error)
	SetSalesPermission(context.Context, string, string, string, SalesPermissionChange) error
	CommercialPermission(context.Context, string, string, string) error
}
