package organization

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"time"
)

var ErrForbidden = errors.New("organization access denied")
var ErrConflict = errors.New("conflict: preserve the last supermanager; invitation or operation may already be used")
var ErrInvitationUnavailable = errors.New("This invitation is no longer available. Ask a manager for a new invitation.")
var ErrNotFound = errors.New("organization or invitation not found")
var ErrInvalid = errors.New("invalid organization request")

type Organization struct {
	Usage                  *UsageSummary            `json:"usage,omitempty"`
	MinimizeRequestLogging bool                     `json:"minimize_request_logging"`
	BillingProfile         *payments.BillingProfile `json:"billing_profile,omitempty"`
	MarkupPercent          *money.Percent           `json:"markup_percent,omitempty"`
	EffectiveMarkupPercent *money.Percent           `json:"effective_markup_percent,omitempty"`
	ClientManagerID        *string                  `json:"client_manager_id"`
	ClientManagerEmail     string                   `json:"client_manager_email"`
	ID                     string                   `json:"id"`
	Name                   string                   `json:"name"`
	Domain                 string                   `json:"domain"`
	Role                   string                   `json:"role"`
	CreditLimit            money.USD                `json:"credit_limit_usd"`
	DefaultWeeklyLimit     *money.USD               `json:"default_weekly_limit_usd"`
	Balance                money.USD                `json:"balance_usd"`
	Available              money.USD                `json:"available_usd"`
	WeeklyLimit            *money.USD               `json:"weekly_limit_usd"`
	WeeklyCommitted        money.USD                `json:"weekly_committed_usd"`
	StorageBytes           int64                    `json:"storage_bytes"`
	CreatedAt              time.Time                `json:"created_at"`
}

// UsageSummary contains customer charges and request activity, never provider costs.
type UsageSummary struct {
	Spend        money.USD  `json:"spend_usd"`
	UnknownCosts int64      `json:"unknown_costs"`
	LastActivity *time.Time `json:"last_activity_at"`
}

type UsageReader interface {
	OrganizationsWithUsage(context.Context, string, bool, time.Time, time.Time) ([]Organization, error)
}
type Member struct {
	Contacts []identity.Contact `json:"contacts,omitempty"`
	Picture  string             `json:"picture,omitempty"`

	HideFinancial   bool       `json:"-"`
	TotalConsumed   money.USD  `json:"total_consumed_usd"`
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Email           string     `json:"email"`
	Role            string     `json:"role"`
	WeeklyLimit     *money.USD `json:"weekly_limit_usd"`
	WeeklyCommitted money.USD  `json:"weekly_committed_usd"`
}
type Credit struct {
	ID         string    `json:"id"`
	Actor      string    `json:"actor_id"`
	Amount     money.USD `json:"amount_usd"`
	Kind       string    `json:"kind"`
	Reason     string    `json:"reason"`
	ExternalID string    `json:"external_id"`
	CreatedAt  time.Time `json:"created_at"`
}
type Invite struct {
	Role            string     `json:"role,omitempty"`
	FirstOwner      bool       `json:"first_owner,omitempty"`
	AlreadyMember   bool       `json:"already_member,omitempty"`
	ID              string     `json:"id"`
	OrganizationID  string     `json:"organization_id"`
	Name            string     `json:"organization_name"`
	ExpiresAt       time.Time  `json:"expires_at"`
	AcceptedBy      *string    `json:"accepted_by,omitempty"`
	AcceptedByEmail string     `json:"accepted_by_email,omitempty"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	Token           string     `json:"token,omitempty"`
}
type Repository interface {
	Organizations(context.Context, string, bool) ([]Organization, error)
	OrganizationAccess(context.Context, string, string) (string, error)
	CreateOrganization(context.Context, string, string, string, string) (Organization, error)
	OrganizationSettings(context.Context, string, string, string, money.USD, *money.USD, bool, ...*money.Percent) error
	OrganizationMembers(context.Context, string, string) ([]Member, error)
	// An omitted limit (limitProvided=false) inherits the default for new members
	// and preserves the individual limit for active members.
	SetOrganizationMember(ctx context.Context, actor, organization, user, role string, limit *money.USD, limitProvided, remove bool) error
	OrganizationCredits(context.Context, string, string, int) ([]Credit, error)
	AddOrganizationCredit(context.Context, string, string, Credit) (Credit, error)
	OrganizationInvites(context.Context, string, string) ([]Invite, error)
	CreateOrganizationInvite(context.Context, string, string, ...string) (Invite, error)
	RevokeOrganizationInvite(context.Context, string, string, string) error
	OrganizationInvitation(context.Context, string, string, bool) (Invite, error)
}

// Members receive their usable allowance, not the organization's financial ledger.
func (o Organization) MarshalJSON() ([]byte, error) {
	type plain Organization
	if Statistics(o.Role) {
		return json.Marshal(plain(o))
	}
	available := o.Available
	if o.WeeklyLimit != nil && *o.WeeklyLimit-o.WeeklyCommitted < available {
		available = *o.WeeklyLimit - o.WeeklyCommitted
	}
	return json.Marshal(struct {
		ID              string     `json:"id"`
		Name            string     `json:"name"`
		Role            string     `json:"role"`
		Available       money.USD  `json:"available_usd"`
		WeeklyLimit     *money.USD `json:"weekly_limit_usd"`
		WeeklyCommitted money.USD  `json:"weekly_committed_usd"`
	}{o.ID, o.Name, o.Role, available, o.WeeklyLimit, o.WeeklyCommitted})
}

func (m Member) MarshalJSON() ([]byte, error) {
	type plain Member
	if !m.HideFinancial {
		return json.Marshal(plain(m))
	}
	return json.Marshal(struct {
		ID       string             `json:"id"`
		Name     string             `json:"name"`
		Email    string             `json:"email"`
		Role     string             `json:"role"`
		Picture  string             `json:"picture,omitempty"`
		Contacts []identity.Contact `json:"contacts,omitempty"`
	}{m.ID, m.Name, m.Email, m.Role, m.Picture, m.Contacts})
}
