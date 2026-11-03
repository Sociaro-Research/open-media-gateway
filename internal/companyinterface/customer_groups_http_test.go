package companyinterface_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
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
