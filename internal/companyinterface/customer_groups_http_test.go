package companyinterface_test

import (
	"context"
	"encoding/json"
	"fmt"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

func TestCompanyFacadeManagementAndProjectsUseRealCentralHandlers(t *testing.T) {
	f, _ := facadeHTTP(t)
	v := f.current(t)
	ctx := context.Background()
	service := core.NewTaskService(postgres.NewTaskRepository(f.centralDB), nil, "mock", "model/with/slash")
	f.central = customerHandler(t, f, postgres.NewCompanyInterfaceAuthority(f.centralDB), service)
	base := "/v1/organizations/" + v.Tokens.Company
	expectFixture(t, facadeRequest(t, f, "PUT", base, `{"name":"Renamed company","default_weekly_limit_usd":null}`, "", true, nil), 204)
	expectFixture(t, facadeRequest(t, f, "PUT", base+"/request-logging", `{"minimize_request_logging":true}`, "", true, nil), 204)
	expectFixture(t, facadeRequest(t, f, "PUT", base, `{"name":"Bad","default_weekly_limit_usd":null,"credit_limit_usd":999}`, "", true, nil), 403)
	expectFixture(t, facadeRequest(t, f, "PUT", base, `{"name":"Bad","default_weekly_limit_usd":null,"markup_percent":0}`, "", true, nil), 403)
	var name string
	var minimized bool
	if e := f.centralDB.QueryRow(`SELECT name,minimize_request_logging FROM organizations WHERE id=$1`, v.Tokens.Company).Scan(&name, &minimized); e != nil || name != "Renamed company" || !minimized {
		t.Fatal("company changes not central", e)
	}
	_, member, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	if _, e := f.centralDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'member')`, v.Tokens.Company, member.ID); e != nil {
		t.Fatal(e)
	}
	expectFixture(t, facadeRequest(t, f, "PUT", base+"/members/"+member.ID, `{"role":"admin","weekly_limit_usd":12.5}`, "", true, nil), 204)
	expectFixture(t, facadeRequest(t, f, "DELETE", base+"/members/"+v.Tokens.Actor, "", "", true, nil), 409)
	invite := facadeRequest(t, f, "POST", base+"/invitations", `{"role":"member"}`, "", true, nil)
	expectFixture(t, invite, 201)
	var invitation struct{ ID, Token string }
	if e := json.Unmarshal(invite.Body.Bytes(), &invitation); e != nil || invitation.ID == "" || invitation.Token == "" {
		t.Fatal("invite missing")
	}
	body := `{"token":"` + invitation.Token + `"}`
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/invitations/preview", body, "", true, nil), 200)
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/invitations/accept", body, "", true, nil), 200)
	expectFixture(t, facadeRequest(t, f, "DELETE", base+"/invitations/"+invitation.ID, "", "", true, nil), 204)
	for _, path := range []string{base + "/support", base + "/credits?offset=0", base + "/balance-history?offset=0", "/v1/api-keys/usage?organization_id=" + v.Tokens.Company + "&month=2026-11&timezone=Europe%2FBerlin", "/v1/organizations?view=summary&from=2026-11-01&to=2026-11-03&timezone=Europe%2FBerlin"} {
		expectFixture(t, facadeRequest(t, f, "GET", path, "", "", true, nil), 200)
	}
	// An invitation token is an object selector, not authority to another company.
	sys := postgres.NewSystemRepository(f.centralDB)
	if e := sys.SetAdmin(ctx, "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	other, e := sys.CreateOrganization(ctx, v.Tokens.Actor, "Foreign company", v.Tokens.Actor, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := sys.CreateOrganizationInvite(ctx, v.Tokens.Actor, other.ID, "member")
	if e != nil {
		t.Fatal(e)
	}
	for _, op := range []string{"preview", "accept"} {
		expectFixture(t, facadeRequest(t, f, "POST", "/v1/invitations/"+op, `{"token":"`+foreign.Token+`"}`, "", true, nil), 403)
	}
	var accepted bool
	if e = f.centralDB.QueryRow(`SELECT accepted_by IS NOT NULL FROM organization_invites WHERE id=$1`, foreign.ID).Scan(&accepted); e != nil || accepted {
		t.Fatal("foreign invite changed", e)
	}
	// Existing project ownership/version logic executes centrally under the key.
	created := facadeRequest(t, f, "POST", "/v1/projects", `{"name":"Central project"}`, v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, created, 201)
	var project core.Project
	if e = json.Unmarshal(created.Body.Bytes(), &project); e != nil || project.ID == "" {
		t.Fatal("project missing")
	}
	expectFixture(t, facadeRequest(t, f, "PATCH", "/v1/projects/"+project.ID, fmt.Sprintf(`{"name":"Renamed project","version":%d}`, project.Version), v.Tokens.PlaygroundKey, true, nil), 200)
	expectFixture(t, facadeRequest(t, f, "PATCH", "/v1/projects/"+project.ID, fmt.Sprintf(`{"name":"Stale","version":%d}`, project.Version), v.Tokens.PlaygroundKey, true, nil), 409)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/projects", "", v.Tokens.PlaygroundKey, false, nil), 200)
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/generations?project_id="+project.ID+"&limit=1&offset=0", "", v.Tokens.PlaygroundKey, true, nil), 200)
	expectFixture(t, facadeRequest(t, f, "DELETE", "/v1/projects/"+project.ID, fmt.Sprintf(`{"version":%d}`, project.Version+1), v.Tokens.PlaygroundKey, true, nil), 204)
	if countFixture(t, f.localDB, "generation_projects") != 0 {
		t.Fatal("local shadow projects created")
	}
	// A slash-bearing public model ID survives exact escaped-path dispatch.
	models := facadeRequest(t, f, "GET", "/v1/models", "", v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, models, 200)
	var catalogue struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e = json.Unmarshal(models.Body.Bytes(), &catalogue); e != nil || len(catalogue.Data) == 0 {
		t.Fatalf("no fixture model: %s", models.Body.String())
	}
	id := catalogue.Data[0].ID
	if !strings.Contains(id, "/") {
		t.Fatalf("fixture is not slash-bearing: %q", id)
	}
	for _, encoded := range []string{id, url.PathEscape(id)} {
		detail := facadeRequest(t, f, "GET", "/v1/models/"+encoded, "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, detail, 200)
		var model struct {
			ID string `json:"id"`
		}
		json.Unmarshal(detail.Body.Bytes(), &model)
		if model.ID != id {
			t.Fatal("model binding changed")
		}
	}
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/models/%2e%2e%2fproviders", "", v.Tokens.PlaygroundKey, true, nil), 403)
}

// Compare the reserved facade with the actual Go central mux, retaining the
// escaped URL until the delegation contract has accepted its interpretation.
type pathDispatchAuthority struct {
	ci.CustomerAuthority
	projections atomic.Int32
}

func (a *pathDispatchAuthority) User(ctx context.Context, id string) (identity.User, error) {
	a.projections.Add(1)
	return a.CustomerAuthority.User(ctx, id)
}
func TestCompanyFacadeEscapedPathInterpretation(t *testing.T) {
	f, wire := facadeHTTP(t)
	v := f.current(t)
	service := core.NewTaskService(postgres.NewTaskRepository(f.centralDB), nil, "mock", "model/with/slash")
	authority := &pathDispatchAuthority{CustomerAuthority: postgres.NewCompanyInterfaceAuthority(f.centralDB)}
	f.central = customerHandler(t, f, authority, service)
	id := "mock/model/with/slash"
	tests := []struct {
		name, method, path string
		want               int
	}{
		{"encoded slash", "GET", "/v1/models/" + url.PathEscape(id), 200},
		{"double encoded slash", "GET", "/v1/models/mock%252Fmodel%252Fwith%252Fslash", 403},
		{"encoded dot segment", "GET", "/v1/models/mock/%2e%2e/providers", 403},
		{"encoded question", "GET", "/v1/models/mock%3Fmodel", 403},
		{"encoded hash", "GET", "/v1/models/mock%23model", 403},
		{"encoded backslash", "GET", "/v1/models/mock%5Cmodel", 403},
		{"encoded organization", "GET", "/v1/organizations/%" + fmt.Sprintf("%02x", v.Tokens.Company[0]) + v.Tokens.Company[1:], 403},
		{"encoded organization separator", "GET", "/v1/organizations%2F" + v.Tokens.Company, 403},
		{"neighbor system", "GET", "/v1/models/%2e%2e%2Fsystem%2Ftemplates", 403},
		{"neighbor organization", "GET", "/v1/models/%2e%2e%2Forganizations%2F" + v.Tokens.Company, 403},
		{"system path", "GET", "/v1/system/templates", 403},
		{"unknown model verb", "PUT", "/v1/models/" + url.PathEscape(id), 403},
		{"unknown route", "GET", "/v1/not-a-customer-route", 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Direct central response is diagnostic: the company grant intentionally
			// rejects aliases which an ordinary central mux may interpret or redirect.
			direct := httptest.NewRequest(tc.method, fixtureCentral+tc.path, nil)
			direct.Header.Set("Authorization", "Bearer "+v.Tokens.PlaygroundKey)
			dw := httptest.NewRecorder()
			f.central.ServeHTTP(dw, direct)
			authority.projections.Store(0)
			central := customerRequest(t, f.central, v, true, tc.method, tc.path, "", v.Tokens.PlaygroundKey, nil)
			before := wire.calls.Load()
			local := facadeRequest(t, f, tc.method, tc.path, "", v.Tokens.PlaygroundKey, true, nil)
			t.Logf("raw=%s direct-central=%d central-delegated=%d reserved-facade=%d", tc.path, dw.Code, central.Code, local.Code)
			expectFixture(t, central, tc.want)
			expectFixture(t, local, tc.want)
			if tc.want == 403 && (authority.projections.Load() != 0 || wire.calls.Load() != before) {
				t.Fatal("rejected path reached inner handler projection or remote hop")
			}
			if tc.want == 200 {
				expectFixture(t, dw, 200)
				for _, body := range []string{dw.Body.String(), central.Body.String(), local.Body.String()} {
					var model struct{ ID string }
					if json.Unmarshal([]byte(body), &model) != nil || model.ID != id {
						t.Fatalf("model interpretation changed: %s", body)
					}
				}
			}
		})
	}
}

func TestCompanyFacadePreparationPathSelectsSameCentralOperation(t *testing.T) {
	f, _ := facadeHTTP(t)
	v := f.current(t)
	service := core.NewTaskService(postgres.NewTaskRepository(f.centralDB), nil, "mock", "model/with/slash")
	f.central = customerHandler(t, f, postgres.NewCompanyInterfaceAuthority(f.centralDB), service)
	for _, tc := range []struct {
		path, contract, code string
		status               int
	}{
		{"/v1/models/mock/model/with/slash/preparations", "central-model", "model_not_found", 404},
		{"/v1/models/mock%2Fmodel%2Fwith%2Fslash/preparations", "model-preparations", "resource_service_unavailable", 503},
	} {
		t.Run(tc.contract, func(t *testing.T) {
			if ci.Classify("GET", tc.path).ID != tc.contract {
				t.Fatal("incorrect operation contract")
			}
			direct := httptest.NewRequest("GET", fixtureCentral+tc.path, nil)
			direct.Header.Set("Authorization", "Bearer "+v.Tokens.PlaygroundKey)
			dw := httptest.NewRecorder()
			f.central.ServeHTTP(dw, direct)
			central := customerRequest(t, f.central, v, true, "GET", tc.path, "", v.Tokens.PlaygroundKey, nil)
			local := facadeRequest(t, f, "GET", tc.path, "", v.Tokens.PlaygroundKey, true, nil)
			for _, out := range []*httptest.ResponseRecorder{dw, central, local} {
				expectFixture(t, out, tc.status)
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if json.Unmarshal(out.Body.Bytes(), &body) != nil || body.Error.Code != tc.code {
					t.Fatalf("wrong selected operation: %s", out.Body.String())
				}
			}
			t.Logf("raw=%s contract=%s operation-error=%s", tc.path, tc.contract, tc.code)
		})
	}
}
