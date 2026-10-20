package api

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/markup"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"testing"
)

type permissionFixture struct {
	*salesFixture
	granted map[string]bool
	credits int
}

func (f *permissionFixture) SalesPermissions(context.Context, string) (system.SalesPermissions, error) {
	return system.SalesPermissions{Version: 1, Members: []system.SalesMember{}, Grants: []system.SalesGrant{}}, nil
}
func (f *permissionFixture) SetSalesPermission(context.Context, string, string, string, system.SalesPermissionChange) error {
	return nil
}
func (f *permissionFixture) CommercialPermission(_ context.Context, actor, id, p string) error {
	if actor == "bob" && id == f.assigned && f.granted[p] {
		return nil
	}
	return system.ErrForbidden
}
func (f *permissionFixture) Markups(context.Context, string, string) (markup.Policy, error) {
	rate := money.Percent(1000)
	return markup.Policy{Company: markup.Config{Default: &rate, Exceptions: []markup.Rule{{Provider: "mock", Percent: &rate}}, Version: 1}}, nil
}
func (f *permissionFixture) SaveMarkups(ctx context.Context, a, o string, _ markup.Config) (markup.Policy, error) {
	return f.Markups(ctx, a, o)
}
func (f *permissionFixture) AddOrganizationCredit(_ context.Context, _, _ string, c org.Credit) (org.Credit, error) {
	f.credits++
	return c, nil
}
func TestSalesPermissionHTTPBoundaries(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &permissionFixture{salesFixture: &salesFixture{clientManagerFixture: &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, manager: true, assigned: "11111111-1111-4111-8111-111111111111"}, role: system.ClientManager}, granted: map[string]bool{}}
	s.system = f
	h := s.handler()
	base := "/v1/organizations/" + f.assigned
	for _, tc := range []struct{ method, path, body string }{{"GET", base + "/markups", ""}, {"GET", base + "/markups/catalog", ""}, {"PUT", base + "/model-access", `{"model_ids":[],"version":1}`}, {"POST", base + "/credits", `{"amount_usd":1,"kind":"payment","reason":"invoice","external_id":"one"}`}} {
		if w := sendTest(h, tc.method, tc.path, tc.body, "bob", ""); w.Code != 403 {
			t.Fatalf("denied %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	f.granted["pricing"] = true
	f.granted["topup"] = true
	for _, path := range []string{base + "/markups", base + "/markups/catalog", base + "/sales-permissions", "/v1/sales/permissions"} {
		if w := sendTest(h, "GET", path, "", "bob", ""); w.Code != 200 {
			t.Fatalf("permitted %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := sendTest(h, "POST", base+"/credits", `{"amount_usd":1,"kind":"payment","reason":"invoice","external_id":"one"}`, "bob", ""); w.Code != 201 || f.credits != 1 {
		t.Fatal("topup routing", w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/system/markups", "/v1/system/markups/catalog", "/v1/system/model-settings", base + "/model-access", "/v1/organizations/22222222-2222-4222-8222-222222222222/markups"} {
		if w := sendTest(h, "GET", path, "", "bob", ""); w.Code != 403 {
			t.Fatalf("scope widened %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"enabled":true,"unexpected":1}`, `{"enabled":"yes"}`} {
		if w := sendTest(h, "PUT", "/v1/sales/permissions/"+f.assigned+"/models", body, "bob", ""); w.Code != 400 {
			t.Fatalf("input %d %s", w.Code, w.Body.String())
		}
	}
}
