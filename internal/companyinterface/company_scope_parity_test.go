package companyinterface_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompanyFacadeOrganizationReadScopesMatchCentral(t *testing.T) {
	f, _, _ := generationWorkflowFixture(t)
	v := f.current(t)
	for _, scope := range []string{"generation", "generation organizations:manage"} {
		body, _ := json.Marshal(map[string]any{"name": "scope parity", "organization_id": v.Tokens.Company, "scopes": strings.Fields(scope)})
		issue := httptest.NewRequest("POST", fixtureCentral+"/v1/api-keys", strings.NewReader(string(body)))
		issue.Header.Set("Content-Type", "application/json")
		issue.AddCookie(f.centralCookie)
		issued := httptest.NewRecorder()
		f.central.ServeHTTP(issued, issue)
		expectFixture(t, issued, 201)
		var key struct{ Secret string }
		if json.Unmarshal(issued.Body.Bytes(), &key) != nil || key.Secret == "" {
			t.Fatal("central key not issued")
		}
		if scope == "generation" {
			invite := facadeRequest(t, f, "POST", "/v1/organizations/"+v.Tokens.Company+"/invitations", `{"role":"member"}`, "", true, nil)
			expectFixture(t, invite, 201)
			var invitation struct{ Token string }
			if json.Unmarshal(invite.Body.Bytes(), &invitation) != nil || invitation.Token == "" {
				t.Fatal("missing invitation")
			}
			for _, operation := range []struct{ method, path, body string }{{"POST", "/v1/invitations/preview", `{"token":"` + invitation.Token + `"}`}, {"GET", "/v1/me", ""}, {"HEAD", "/v1/me", ""}} {
				t.Run(operation.method+operation.path, func(t *testing.T) {
					request := httptest.NewRequest(operation.method, fixtureCentral+operation.path, strings.NewReader(operation.body))
					request.Header.Set("Authorization", "Bearer "+key.Secret)
					request.Header.Set("Content-Type", "application/json")
					direct := httptest.NewRecorder()
					f.central.ServeHTTP(direct, request)
					expectFixture(t, direct, 200)
					local := facadeRequest(t, f, operation.method, operation.path, operation.body, key.Secret, false, nil)
					if local.Code != direct.Code {
						t.Errorf("scope parity: direct=%d facade=%d", direct.Code, local.Code)
					}
				})
			}
		}
		base := "/v1/organizations/" + v.Tokens.Company
		for _, path := range []string{"/v1/organizations", base, base + "/members", base + "/invitations", base + "/references", base + "/credits", base + "/balance-history", base + "/generations"} {
			for _, method := range []string{"GET", "HEAD"} {
				t.Run(scope+"/"+method+path, func(t *testing.T) {
					direct := httptest.NewRequest(method, fixtureCentral+path, nil)
					direct.Header.Set("Authorization", "Bearer "+key.Secret)
					dw := httptest.NewRecorder()
					f.central.ServeHTTP(dw, direct)
					expected := 200
					needsManagement := method == "HEAD" || strings.HasSuffix(path, "/balance-history")
					if needsManagement && scope == "generation" {
						expected = 403
					}
					if dw.Code != expected {
						t.Fatalf("direct central policy changed: %d want%d", dw.Code, expected)
					}
					local := facadeRequest(t, f, method, path, "", key.Secret, false, nil)
					if local.Code != dw.Code {
						t.Errorf("scope parity: direct=%d facade=%d", dw.Code, local.Code)
					}
					if method == "HEAD" && (dw.Body.Len() != 0 || local.Body.Len() != 0) {
						t.Fatal("HEAD body leaked")
					}
				})
			}
		}
	}
}
