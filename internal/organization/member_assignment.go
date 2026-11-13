package organization

import (
	"context"
	"errors"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

var ErrAlreadyMember = errors.New("This user is already a member of this company. Change their role in the member list.")

// ErrUnknownUser is still an ErrNotFound (HTTP 404 not_found) for callers. It also
// covers existing accounts that cannot join (client managers), so it does not say "no account".
var ErrUnknownUser error = diagnostic{ErrNotFound, "User not found or unavailable for this organization."}

// MemberAssigner separates adding/restoring membership from intentional role
// updates. Implementations must reject active members atomically with the write.
type MemberAssigner interface {
	AssignOrganizationMember(ctx context.Context, actor, organization, user, role string, limit *money.USD, limitProvided bool) error
}
