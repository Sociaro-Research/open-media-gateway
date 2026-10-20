package organization

import (
	"context"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
)

// SupportPerson exposes only the identity and deliberately shared contact links.
type SupportPerson struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Picture  string             `json:"picture,omitempty"`
	Contacts []identity.Contact `json:"contacts"`
}
type Support struct {
	ClientManager *SupportPerson `json:"client_manager"`
}
type SupportReader interface {
	OrganizationSupport(context.Context, string, string) (Support, error)
}
