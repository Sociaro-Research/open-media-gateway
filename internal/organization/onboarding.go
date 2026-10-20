package organization

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrRecipient = errors.New("Sign in with the email address that received this invitation.")
var ErrOnboardingLimit = errors.New("You can change at most 20 different companies in one hour. Try again later.")

type ClientInput struct {
	OperationID string `json:"operation_id"`
	Name        string `json:"name"`
	Country     string `json:"country"`
	Domain      string `json:"domain"`
	ContactName string `json:"contact_name"`
	Email       string `json:"email"`
	Comment     string `json:"comment"`
}

func (in *ClientInput) Normalize() error {
	in.Name = CompanyName(in.Name)
	in.Country = strings.TrimSpace(in.Country)
	if len(in.Country) == 2 {
		in.Country = strings.ToUpper(in.Country)
	}
	in.ContactName = strings.TrimSpace(in.ContactName)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Comment = strings.TrimSpace(in.Comment)
	address, err := mail.ParseAddress(in.Email)
	if _, e := uuid.Parse(in.OperationID); e != nil || err != nil || address.Address != in.Email || len(in.Email) > 254 || strings.ContainsAny(in.Email, "\r\n") || in.Name == "" || len(in.Name) > 160 || in.ContactName == "" || len(in.ContactName) > 160 || in.Country == "" || len(in.Country) > 100 || len(in.Comment) > 2000 {
		return ErrInvalid
	}
	in.Domain, err = CompanyDomain(in.Domain)
	if err != nil {
		return err
	}
	return nil
}

type Client struct {
	OrganizationID string     `json:"organization_id"`
	Name           string     `json:"name"`
	Country        string     `json:"country"`
	Domain         string     `json:"domain"`
	ContactName    string     `json:"contact_name"`
	Email          string     `json:"email"`
	Comment        string     `json:"comment"`
	Status         string     `json:"status"`
	Version        int64      `json:"version"`
	ExpiresAt      time.Time  `json:"expires_at"`
	ActivatedAt    *time.Time `json:"activated_at,omitempty"`
}

// Duplicate deliberately excludes foreign contact information and balances.
type Duplicate struct {
	Criterion      string `json:"criterion"`
	Manager        string `json:"manager"`
	OrganizationID string `json:"organization_id,omitempty"`
}

func (d *Duplicate) Error() string {
	criterion := d.Criterion
	if criterion == "domain" {
		criterion = "website"
	}
	return "A company with this " + criterion + " already exists. Responsible manager: " + d.Manager + "."
}

type OwnerDelivery struct {
	ID, Lease, InviteID, Recipient, Name, Origin string
	Hash                                         []byte
	ExpiresAt                                    time.Time
	Attempts                                     int
}

type OnboardingRepository interface {
	ClientOnboardings(context.Context, string) ([]Client, error)
	CreateClient(context.Context, string, ClientInput, OwnerDelivery) (Client, error)
	ChangeOwnerInvitation(context.Context, string, string, string, int64, bool, OwnerDelivery) (Client, error)
	ClaimOwnerDelivery(context.Context) (OwnerDelivery, bool, error)
	FinishOwnerDelivery(context.Context, OwnerDelivery, bool) error
}
type InvitationSender interface {
	SendNotification(context.Context, string, string, string, string) error
}

type Onboarding struct {
	Repo   OnboardingRepository
	Sender InvitationSender
	Origin string
	secret []byte
}

func NewOnboarding(repo OnboardingRepository, sender InvitationSender, origin, secret string) (*Onboarding, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) || len(secret) < 16 {
		return nil, ErrInvalid
	}
	return &Onboarding{Repo: repo, Sender: sender, Origin: strings.TrimRight(origin, "/"), secret: []byte(secret)}, nil
}

// Domain-separated HMAC allows durable retries while persisting only a hash,
// never a plaintext or decryptable invitation credential.
func (m *Onboarding) token(id string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte("gateway/first-owner-invitation/v1/" + id))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (m *Onboarding) delivery() OwnerDelivery {
	d := OwnerDelivery{ID: uuid.NewString(), Origin: m.Origin, ExpiresAt: time.Now().UTC().Add(14 * 24 * time.Hour)}
	hash := sha256.Sum256([]byte(m.token(d.ID)))
	d.Hash = hash[:]
	return d
}
func (m *Onboarding) Create(ctx context.Context, actor string, in ClientInput) (Client, error) {
	if e := in.Normalize(); e != nil {
		return Client{}, e
	}
	return m.Repo.CreateClient(ctx, actor, in, m.delivery())
}
func (m *Onboarding) Change(ctx context.Context, actor, id, operation string, version int64, revoke bool) (Client, error) {
	if _, e := uuid.Parse(operation); e != nil || version < 1 {
		return Client{}, ErrInvalid
	}
	return m.Repo.ChangeOwnerInvitation(ctx, actor, id, operation, version, revoke, m.delivery())
}
func (m *Onboarding) Run(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		m.Deliver(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (m *Onboarding) Deliver(ctx context.Context) {
	for n := 0; n < 10 && ctx.Err() == nil; n++ {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		d, found, e := m.Repo.ClaimOwnerDelivery(c)
		cancel()
		if e != nil {
			slog.Error("owner invitation queue unavailable")
			return
		}
		if !found {
			return
		}
		token := m.token(d.ID)
		hash := sha256.Sum256([]byte(token))
		e = errors.New("invitation delivery unavailable")
		if hmac.Equal(hash[:], d.Hash) && m.Sender != nil {
			body := fmt.Sprintf("You have been invited to become the first Supermanager of %s.\n\nSign in or create an account using %s, then accept the invitation. You can manage your company's members and API keys.\n\n%s/organizations#invite=%s\n\nThis one-time invitation expires on %s.\n", d.Name, d.Recipient, d.Origin, token, d.ExpiresAt.UTC().Format(time.RFC3339))
			c, cancel = context.WithTimeout(ctx, 15*time.Second)
			e = m.Sender.SendNotification(c, d.Recipient, "Invitation to manage your company", body, "owner-invite-"+d.ID)
			cancel()
		}
		c, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		finish := m.Repo.FinishOwnerDelivery(c, d, e == nil)
		cancel()
		if finish != nil {
			slog.Error("owner invitation delivery state unavailable", "delivery_id", d.ID)
		}
	}
}
