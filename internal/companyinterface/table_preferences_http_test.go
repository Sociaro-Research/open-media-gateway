package companyinterface_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
)

func TestCompanyBrowserTablePreferencesBelongToBrowserActor(t *testing.T) {
	f := activeHTTP(t)
	v := f.current(t)
	ctx := context.Background()
	h := customerHandler(t, f, postgres.NewCompanyInterfaceAuthority(f.centralDB))
	_, coworker, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	if _, err := f.centralDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'member')`, v.Tokens.Company, coworker.ID); err != nil {
		t.Fatal(err)
	}
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Other recipient", UserID: coworker.ID, OrganizationID: v.Tokens.Company, ResourceMode: "shared", StorageMode: "shared"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = postgres.NewAPIKeyRepository(f.centralDB).Create(ctx, v.Tokens.Actor, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	body := `{"table_columns":{"organizations":["organization","balance"]},"table_wide":{"organizations":true,"requests":true},"report_columns":["date","estimated_cost","profit_status"]}`
	response := customerRequest(t, h, v, true, "PATCH", "/v1/me", body, secret, nil)
	expectFixture(t, response, 200)
	var user identity.User
	if err = json.Unmarshal(response.Body.Bytes(), &user); err != nil || user.ID != v.Tokens.Actor || len(user.TableColumns["organizations"]) != 2 || !user.TableWide["requests"] || len(user.ReportColumns) != 3 {
		t.Fatalf("BrowserActor/projection lost: %s %v", response.Body, err)
	}
	read := customerRequest(t, h, v, true, "GET", "/v1/me", "", secret, nil)
	expectFixture(t, read, 200)
	if !strings.Contains(read.Body.String(), `"table_columns"`) {
		t.Fatal("reload lost preferences", read.Body)
	}
	var other string
	if err = f.centralDB.QueryRow(`SELECT table_columns::text FROM users WHERE id=$1`, coworker.ID).Scan(&other); err != nil || other != "{}" {
		t.Fatal("key recipient was modified", other, err)
	}
	expectFixture(t, customerRequest(t, h, v, false, "PATCH", "/v1/me", body, secret, nil), 403)
	expectFixture(t, customerRequest(t, h, v, true, "PATCH", "/v1/me", body, secret, func(r *http.Request) { r.Header.Del(ci.SessionHeader) }), 403)
}
