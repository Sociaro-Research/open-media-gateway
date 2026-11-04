package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func TestCompanyMediaSignedReferenceUsesActualRecipientAndLiveBrowser(t *testing.T) {
	f, _, library, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	recipient, key := resourceCoworkerKey(t, f, "member")
	memberctx := identity.WithOrganizationRole(identity.WithUser(context.Background(), identity.User{ID: recipient}), v.Tokens.Company, "member")
	own, e := library.Upload(memberctx, "member.png", bytes.NewReader(testmedia.PNG512("member-owned")))
	if e != nil {
		t.Fatal(e)
	}
	other, e := library.Upload(ctx, "other.png", bytes.NewReader(testmedia.PNG512("manager-owned")))
	if e != nil {
		t.Fatal(e)
	}
	list := resourceDirect(f, "GET", "/v1/references", "", v.Tokens.PlaygroundKey, "")
	expectFixture(t, list, 200)
	var rows struct{ Data []struct{ ID, URL string } }
	json.Unmarshal(list.Body.Bytes(), &rows)
	links := map[string]string{}
	for _, row := range rows.Data {
		links[row.ID] = row.URL
	}
	if links[own.ID] == "" || links[other.ID] == "" {
		t.Fatal("fixture reference missing")
	}
	for _, browser := range []bool{true, false} {
		got := facadeRequest(t, f, "GET", links[own.ID], "", key, browser, nil)
		expectFixture(t, got, 200)
		if !bytes.Equal(got.Body.Bytes(), testmedia.PNG512("member-owned")) {
			t.Fatal("recipient bytes")
		}
		expectFixture(t, facadeRequest(t, f, "GET", links[other.ID], "", key, browser, nil), 404)
	}
	// Changing only the browser membership rejects browser access, while the
	// independent original recipient's SDK key remains valid and correctly scoped.
	_, manager, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	if _, e = f.centralDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, v.Tokens.Company, manager.ID); e != nil {
		t.Fatal(e)
	}
	if e = postgres.NewSystemRepository(f.centralDB).SetOrganizationMember(context.Background(), manager.ID, v.Tokens.Company, v.Tokens.Actor, "supermanager", nil, false, true); e != nil {
		t.Fatal(e)
	}
	expectFixture(t, facadeRequest(t, f, "GET", links[own.ID], "", key, true, nil), 403)
	expectFixture(t, facadeRequest(t, f, "GET", links[own.ID], "", key, false, nil), 200)
}
func TestCompanyMediaReportRedactionPreservesCompanyRole(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	task := completeMediaTask(t, f, store, ctx, testmedia.PNG512("report policy"))
	_, manager, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	if _, e := f.centralDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, v.Tokens.Company, manager.ID); e != nil {
		t.Fatal(e)
	}
	if e := postgres.NewSystemRepository(f.centralDB).SetOrganizationMember(context.Background(), manager.ID, v.Tokens.Company, v.Tokens.Actor, "admin", nil, false, false); e != nil {
		t.Fatal(e)
	}
	path := "/v1/organizations/" + v.Tokens.Company + "/reports/" + task.ID
	for _, delegated := range []bool{false, true} {
		var body []byte
		if delegated {
			got := facadeRequest(t, f, "GET", path, "", "", true, nil)
			expectFixture(t, got, 200)
			body = got.Body.Bytes()
		} else {
			got := resourceDirect(f, "GET", path, "", v.Tokens.PlaygroundKey, "")
			expectFixture(t, got, 200)
			body = got.Body.Bytes()
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || string(fields["content_access"]) != "false" || fields["request"] != nil || fields["links"] != nil || fields["processing"] != nil {
			t.Fatal("manager content leak", string(body))
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		direct := resourceDirect(f, method, path+"/content", "", v.Tokens.PlaygroundKey, "")
		got := facadeRequest(t, f, method, path+"/content", "", "", true, nil)
		expectFixture(t, direct, http.StatusForbidden)
		expectFixture(t, got, http.StatusForbidden)
		if method == "HEAD" && got.Body.Len() != 0 {
			t.Fatal("error HEAD body")
		}
	}
}
