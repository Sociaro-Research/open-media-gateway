package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/limits"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"net/http"
	"strings"
	"testing"
	"time"
)

type limitsFixture struct {
	*organizationFixture
	policy limits.Policy
	writes int
}

func (f *limitsFixture) RequestLimits(context.Context, string) (limits.Policy, error) {
	return f.policy, nil
}
func (f *limitsFixture) LimitSettings(context.Context, string, string) (limits.Policy, error) {
	return f.policy, nil
}
func (f *limitsFixture) SaveLimits(_ context.Context, _ string, org string, c limits.Config) (limits.Policy, error) {
	old := f.policy.System
	if org != "" {
		old = f.policy.Organization
	}
	if c.Version != old.Version {
		return limits.Policy{}, system.ErrConflict
	}
	c.Version++
	if org == "" {
		f.policy.System = c
	} else {
		f.policy.Organization = c
	}
	f.policy.Resolve()
	f.writes++
	return f.policy, nil
}
func newLimitsFixture() *limitsFixture {
	f := &limitsFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
	f.policy.System = limits.Config{Version: 1, Values: map[string]*int{}}
	f.policy.Organization = limits.Config{Version: 1, Values: map[string]*int{}}
	for k, v := range limits.Defaults {
		f.policy.System.Values[k] = new(int)
		*f.policy.System.Values[k] = v
		f.policy.Organization.Values[k] = nil
	}
	f.policy.Resolve()
	return f
}

type captureLimiter struct {
	b   []limits.Bucket
	err error
}

func (c *captureLimiter) Take(_ context.Context, b []limits.Bucket) error { c.b = b; return c.err }
func TestRequestLimitHTTPAdmissionAndScope(t *testing.T) {
	s, keys := newPersonalTestServer()
	f := newLimitsFixture()
	limiter := &captureLimiter{}
	s.system = f
	s.keys = WithRequestLimits(keys, f, limiter)
	h := s.handler()
	_, secret := issueTestKey(t, h, "alice", `{"name":"test"}`)
	for _, path := range []string{"/v1/generations", "/v1/videos", "/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/audio/generations", "/v1/generations"} {
		limiter.err = &limits.Exceeded{Name: "user_generations_minute", RetryAfter: 1500 * time.Millisecond}
		w := sendTest(h, "POST", path, `{}`, "", secret)
		if w.Code != 429 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), "rate_limit_error") {
			t.Fatal(path, w.Code, w.Body, w.Header())
		}
		if len(limiter.b) != 4 || !strings.Contains(limiter.b[1].ID, "user:alice") {
			t.Fatal("wrong quota identity", limiter.b)
		}
	}
	for _, path := range []string{"/v1/generations", "/v1/generations/test", "/v1/generations", "/v1/generations/test", "/v1/videos/test"} {
		if w := sendTest(h, "GET", path, "", "", secret); w.Code != 429 {
			t.Fatal(path, w.Code)
		}
		if len(limiter.b) != 2 || limiter.b[0].Name != "key_reads_minute" {
			t.Fatal(limiter.b)
		}
	}
	if w := sendTest(h, "POST", "/v1/api-keys", `{"name":"denied"}`, "alice", ""); w.Code != 429 {
		t.Fatal(w.Code, w.Body)
	}
	if len(limiter.b) != 1 || limiter.b[0].Name != "user_key_creations_minute" {
		t.Fatal(limiter.b)
	}
	limiter.err = errors.New("Redis unavailable")
	if w := sendTest(h, "POST", "/v1/generations", `{}`, "", secret); w.Code != 503 {
		t.Fatal("fail-open quota", w.Code)
	}
	limiter.err = nil
	for _, path := range []string{"/v1/system/limits", "/v1/organizations/" + fixtureOrg + "/limits"} {
		for _, method := range []string{"GET", "PUT"} {
			if w := sendTest(h, method, path, `{}`, "bob", ""); w.Code != 403 {
				t.Fatal("member accessed settings", w.Code)
			}
			if w := sendTest(h, method, path, `{}`, "", secret); w.Code != 403 {
				t.Fatal("unscoped admin key accessed settings", w.Code)
			}
		}
		if w := sendTest(h, "GET", path, "", "alice", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if w := sendTest(h, "PUT", path, `{"values":{},"version":1}`, "alice", ""); w.Code != 400 {
			t.Fatal("incomplete limits accepted", w.Code)
		}
	}
	body, _ := json.Marshal(f.policy.Organization)
	if w := sendTest(h, "PUT", "/v1/organizations/"+fixtureOrg+"/limits", string(body), "alice", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := sendTest(h, "PUT", "/v1/organizations/"+fixtureOrg+"/limits", string(body), "alice", ""); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestEstimateDoesNotConsumeGenerationQuota(t *testing.T) {
	for _, path := range []string{"/v1/generations/estimate", "/v1/generations/estimate"} {
		r, _ := http.NewRequest("POST", path, nil)
		if requestOperation(r) != "" {
			t.Fatal(path)
		}
	}
}
