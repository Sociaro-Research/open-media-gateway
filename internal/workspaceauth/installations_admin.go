package workspaceauth

import (
	"context"
	"time"
)

// InstallationGrant is a company-scoped view of an issued workspace key.
// It contains no credential or authorization-recovery proof.
type InstallationGrant struct {
	Grant
	Access    string    `json:"access"`
	CreatedAt time.Time `json:"created_at"`
	Prefix    string    `json:"prefix"`
}

type InstallationAccessCount struct {
	Company string `json:"organization_id"`
	Active  int    `json:"active_count"`
}

type InstallationAdministration interface {
	InstallationAccessSummary(context.Context, string) ([]InstallationAccessCount, error)
	OrganizationInstallationGrants(context.Context, string, string) ([]InstallationGrant, error)
	RevokeOrganizationInstallation(context.Context, string, string, string) error
}
