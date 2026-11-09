package organization

import (
	"context"
	"errors"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

var ErrAlreadyMember = errors.New("This user is already a member of this company. Change their role in the member list.")

// MemberAssigner separates adding/restoring membership from intentional role
// updates. Implementations must reject active members atomically with the write.
type MemberAssigner interface {
	AssignOrganizationMember(ctx context.Context, actor, organization, user, role string, limit *money.USD, limitProvided bool) error
}
