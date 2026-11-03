package companyinterface_test

import (
	"context"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
	"net/http"
	"testing"
	"time"
)

type templateDeadlineWire struct {
	base http.RoundTripper
	t    *testing.T
	want time.Duration
}

func (w *templateDeadlineWire) RoundTrip(r *http.Request) (*http.Response, error) {
	if len(r.URL.Path) > len(ci.CustomerBrowserPrefix) && r.URL.Path[:len(ci.CustomerBrowserPrefix)] == ci.CustomerBrowserPrefix {
		deadline, ok := r.Context().Deadline()
		left := time.Until(deadline)
		if !ok || left < w.want-5*time.Second || left > w.want {
			w.t.Errorf("local/client customer deadline %s, want near %s", left, w.want)
		}
	}
	return w.base.RoundTrip(r)
}

type templateDeadlineAuthority struct {
	ci.CustomerAuthority
	t    *testing.T
	want time.Duration
}

func (a *templateDeadlineAuthority) User(ctx context.Context, id string) (identity.User, error) {
	deadline, ok := ctx.Deadline()
	left := time.Until(deadline)
	if !ok || left < a.want-5*time.Second || left > a.want {
		a.t.Errorf("central customer deadline %s, want near %s", left, a.want)
	}
	return a.CustomerAuthority.User(ctx, id)
}
func TestCompanyFacadeTemplateValidationBudget(t *testing.T) {
	f, wire := facadeHTTP(t)
	v := f.current(t)
	for _, tc := range []struct {
		name, path, method, body string
		want                     time.Duration
		status                   int
	}{
		{"inspection", "/v1/templates/" + uuid.NewString() + "/validate-media", "POST", `{"revision":2,"provider":"mock","model":"fixture"}`, 150 * time.Second, 404},
		{"reference inspection", "/v1/references/validate", "POST", `{"reference_ids":[]}`, 150 * time.Second, 404},
		{"ordinary", "/v1/me", "GET", "", ci.ProtocolTimeout, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authority := &templateDeadlineAuthority{CustomerAuthority: postgres.NewCompanyInterfaceAuthority(f.centralDB), t: t, want: tc.want}
			f.central = customerHandler(t, f, authority)
			f.client = ci.NewFixtureClient(fixtureCentral, &http.Client{Transport: &templateDeadlineWire{base: wire, t: t, want: tc.want}, Timeout: ci.ProtocolTimeout})
			f.restart(t)
			// Missing recipe deliberately returns404 after real authenticated dispatch;
			// this probe tests deadline propagation, not media validation semantics.
			expectFixture(t, facadeRequest(t, f, tc.method, tc.path, tc.body, v.Tokens.PlaygroundKey, true, nil), tc.status)
		})
	}
}
