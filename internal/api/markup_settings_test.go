package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/markup"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"strings"
	"testing"
)

type markupFixture struct {
	*organizationFixture
	policy markup.Policy
	writes int
}

func (f *markupFixture) Markups(context.Context, string, string) (markup.Policy, error) {
	return f.policy, nil
}
func (f *markupFixture) SaveMarkups(_ context.Context, _ string, org string, c markup.Config) (markup.Policy, error) {
	before := f.policy.System
	if org != "" {
		before = f.policy.Company
	}
	if c.Version != before.Version {
		return markup.Policy{}, system.ErrConflict
	}
	c.Version++
	if org == "" {
		f.policy.System = c
	} else {
		f.policy.Company = c
	}
	f.writes++
	return f.policy, nil
}
func TestMarkupSettingsBoundaryAndValidation(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &markupFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
	_ = json.Unmarshal([]byte(`{"system":{"default_percent":20,"exceptions":[],"version":1},"company":{"default_percent":null,"exceptions":[],"version":1}}`), &f.policy)
	s.system = f
	h := s.handler()
	for _, path := range []string{"/v1/system/markups", "/v1/organizations/" + fixtureOrg + "/markups"} {
		for _, method := range []string{"GET", "PUT"} {
			if w := sendTest(h, method, path, `{"default_percent":0,"exceptions":[],"version":1}`, "bob", ""); w.Code != 403 {
				t.Fatalf("%s %s: %d", method, path, w.Code)
			}
		}
		if w := sendTest(h, "GET", path, "", "alice", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		for _, body := range []string{`{"default_percent":0,"exceptions":[{"provider":"unknown","markup_percent":0}],"version":1}`, `{"default_percent":0,"exceptions":[{"provider":"openai","markup_percent":null}],"version":1}`, `{"default_percent":0,"exceptions":[{"provider":"openai","markup_percent":1},{"provider":"openai","markup_percent":0}],"version":1}`} {
			if w := sendTest(h, "PUT", path, body, "alice", ""); w.Code != 400 {
				t.Fatal(w.Code, w.Body)
			}
		}
		body := `{"default_percent":0,"exceptions":[{"provider":"openai","markup_percent":10},{"provider":"openai","model":"gpt-image-1","markup_percent":0}],"version":1}`
		if w := sendTest(h, "PUT", path, body, "alice", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if w := sendTest(h, "PUT", path, body, "alice", ""); w.Code != 409 {
			t.Fatal(w.Code, w.Body)
		}
	}
	_, secret := issueTestKey(t, h, "alice", `{"name":"Pricing admin","scopes":["generation","system:manage"]}`)
	path := "/v1/organizations/" + fixtureOrg + "/markups"
	if w := sendTest(h, "GET", path, "", "", secret); w.Code != 200 {
		t.Fatalf("system key cannot read company pricing: %d %s", w.Code, w.Body)
	}
	if w := sendTest(h, "PUT", path, `{"default_percent":null,"exceptions":[],"version":2}`, "", secret); w.Code != 200 {
		t.Fatalf("system key cannot update company pricing: %d %s", w.Code, w.Body)
	}
	_, limited := issueTestKey(t, h, "alice", `{"name":"Generation only"}`)
	if w := sendTest(h, "GET", path, "", "", limited); w.Code != 403 {
		t.Fatalf("generation key read pricing: %d", w.Code)
	}
	if f.writes != 3 {
		t.Fatal(f.writes)
	}
	for _, path := range []string{"/v1/info", "/v1/organizations"} {
		if w := sendTest(h, "GET", path, "", "bob", ""); strings.Contains(w.Body.String(), "exceptions") || strings.Contains(w.Body.String(), "default_percent") {
			t.Fatal("disclosed markups", w.Body)
		}
	}
}
