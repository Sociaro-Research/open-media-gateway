package companyinterface_test

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"net/http"
	"strings"
	"testing"
	"time"
)

type projectionMediaAuthority struct {
	ci.CustomerAuthority
	phase       string
	memberships int
	reached     bool
	deadline    time.Duration
}

func (s *projectionMediaAuthority) block(ctx context.Context) error {
	s.reached = true
	if deadline, ok := ctx.Deadline(); ok {
		s.deadline = time.Until(deadline)
	}
	<-ctx.Done()
	return ctx.Err()
}
func (s *projectionMediaAuthority) Membership(ctx context.Context, company, actor string) (string, error) {
	s.memberships++
	if s.phase == "membership" && s.memberships == 3 {
		return "", s.block(ctx)
	}
	return s.CustomerAuthority.Membership(ctx, company, actor)
}
func (s *projectionMediaAuthority) User(ctx context.Context, actor string) (identity.User, error) {
	if s.phase == "user" {
		return identity.User{}, s.block(ctx)
	}
	return s.CustomerAuthority.User(ctx, actor)
}
func TestCompanyMediaStartupProjectionDeadline(t *testing.T) {
	for _, phase := range []string{"membership", "user"} {
		t.Run(phase, func(t *testing.T) {
			var authority *projectionMediaAuthority
			f, _, _, _ := mediaHTTPFixture(t, func(d *api.Dependencies) {
				authority = &projectionMediaAuthority{CustomerAuthority: d.CompanyCustomer, phase: phase}
				d.CompanyCustomer = authority
			})
			v := f.current(t)
			start := time.Now()
			path := "/v1/organizations/" + v.Tokens.Company + "/reports/00000000-0000-0000-0000-000000000001/content"
			got := facadeRequest(t, f, "GET", path, "", "", true, nil)
			expectFixture(t, got, http.StatusForbidden)
			if !authority.reached || authority.deadline <= 0 || authority.deadline > ci.MediaAuthorityTimeout || time.Since(start) > 8*time.Second {
				t.Fatal("projection not bounded", authority.reached, authority.deadline, time.Since(start))
			}
			if got.Header().Get(ci.MediaCompleteHeader) != "" || strings.Contains(got.Body.String(), v.Tokens.InstallationCredential) {
				t.Fatal("timed out projection emitted completion/secret")
			}
		})
	}
}
