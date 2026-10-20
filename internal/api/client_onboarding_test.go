package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
)

type onboardingFixture struct {
	org.OnboardingRepository
	created, changed int
	failure          error
}

func (f *onboardingFixture) CreateClient(_ context.Context, _ string, in org.ClientInput, _ org.OwnerDelivery) (org.Client, error) {
	f.created++
	return org.Client{Name: in.Name, Email: in.Email, Status: "sending"}, f.failure
}
func (f *onboardingFixture) ClientOnboardings(context.Context, string) ([]org.Client, error) {
	return []org.Client{}, nil
}
func (f *onboardingFixture) ChangeOwnerInvitation(context.Context, string, string, string, int64, bool, org.OwnerDelivery) (org.Client, error) {
	f.changed++
	return org.Client{}, nil
}

func TestClientOnboardingBrowserScopeAndErrors(t *testing.T) {
	s, _ := newPersonalTestServer()
	_, key := issueTestKey(t, s.handler(), "bob", `{"name":"Old","scopes":["generation","organizations:manage"]}`)
	s.system = &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, manager: true}
	f := &onboardingFixture{}
	s.onboarding, _ = org.NewOnboarding(f, nil, "https://console.fixture.test", strings.Repeat("s", 32))
	h := s.handler()
	body := `{"operation_id":"` + uuid.NewString() + `","name":"Company","domain":"company.fixture.test","country":"CY","contact_name":"Owner","email":"owner@fixture.test"}`
	for _, actor := range []string{"alice", "charlie"} {
		if w := sendTest(h, "POST", "/v1/client-onboardings", body, actor, ""); w.Code != 403 && !(actor == "charlie" && w.Code == 401) {
			t.Fatalf("nonmanager %s: %d %s", actor, w.Code, w.Body.String())
		}
	}
	if w := sendTest(h, "POST", "/v1/client-onboardings", body, "bob", key); w.Code != 403 && w.Code != 401 {
		t.Fatalf("old key: %d %s", w.Code, w.Body.String())
	}
	w := sendTest(h, "POST", "/v1/client-onboardings", body, "bob", "")
	if w.Code != 200 || f.created != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("browser create: %d %s", w.Code, w.Body.String())
	}
	for _, input := range []string{`{}`, strings.Replace(body, `"domain":"company.fixture.test"`, `"domain":""`, 1), strings.Replace(body, `"country":"CY"`, `"country":""`, 1), strings.Replace(body, `"email":"owner@fixture.test"`, `"email":"Bad"`, 1)} {
		if w := sendTest(h, "POST", "/v1/client-onboardings", input, "bob", ""); w.Code != 400 {
			t.Fatalf("invalid input: %d", w.Code)
		}
	}
	f.failure = &org.Duplicate{Criterion: "domain", Manager: "Another sales manager"}
	w = sendTest(h, "POST", "/v1/client-onboardings", body, "bob", "")
	var data map[string]any
	if w.Code != 409 || json.Unmarshal(w.Body.Bytes(), &data) != nil || strings.Contains(w.Body.String(), "organization_id") {
		t.Fatalf("duplicate response: %d %s", w.Code, w.Body.String())
	}
	f.failure = org.ErrOnboardingLimit
	if w := sendTest(h, "POST", "/v1/client-onboardings", body, "bob", ""); w.Code != 429 {
		t.Fatalf("quota: %d", w.Code)
	}
	if w := sendTest(h, "GET", "/v1/client-onboardings", "", "bob", ""); w.Code != 200 {
		t.Fatalf("list: %d", w.Code)
	}
	op := `{"operation_id":"` + uuid.NewString() + `","version":1}`
	id := uuid.NewString()
	for _, action := range []string{"resend", "revoke"} {
		if w := sendTest(h, "POST", "/v1/client-onboardings/"+id+"/"+action, op, "bob", ""); w.Code != 200 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
	if w := sendTest(h, "POST", "/v1/client-onboardings/"+id+"/anything", op, "bob", ""); w.Code != 403 || f.changed != 2 {
		t.Fatalf("unknown action mutated: %d %d", w.Code, f.changed)
	}
}
