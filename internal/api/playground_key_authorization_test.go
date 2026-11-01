package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/playgroundkey"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

type unavailablePlaygroundWorkspaces struct{ *postgres.SystemRepository }

func (unavailablePlaygroundWorkspaces) Organizations(context.Context, string, bool) ([]org.Organization, error) {
	return nil, errors.New("synthetic workspace lookup failure")
}

func TestManagedPlaygroundKeyWorkspaceAuthorization(t *testing.T) {
	db := workspaceDatabase(t)
	ctx := context.Background()
	people := postgres.NewAuthRepository(db)
	users := map[string]identity.User{}
	for _, name := range []string{"owner", "manager", "outsider"} {
		user, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", name, "")
		if err != nil {
			t.Fatal(err)
		}
		users[name] = user
	}
	company := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Managed key authorization fixture')`, company); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'developer'),($1,$3,'supermanager')`, company, users["owner"].ID, users["manager"].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE system_settings SET shared_resources=TRUE`); err != nil {
		t.Fatal(err)
	}
	keys := postgres.NewAPIKeyRepository(db)
	workspaces := postgres.NewSystemRepository(db)
	managed, err := playgroundkey.New(keys, keys, strings.Repeat("synthetic-authorization-master-", 2))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := newPersonalTestServer()
	s.keys, s.system, s.playgroundKeys = keys, workspaces, managed
	s.auth = playgroundBrowserAuth{users: users}
	call := func(action, actor string) *httptest.ResponseRecorder {
		input := map[string]string{"organization_id": company}
		if action == "create" {
			input["request_id"] = uuid.NewString()
		}
		body, _ := json.Marshal(input)
		return sendTest(s.handler(), "POST", "/v1/playground-key/"+action, string(body), actor, "")
	}
	for _, action := range []string{"create", "restore"} {
		t.Run("allowed/"+action, func(t *testing.T) {
			response := call(action, "owner")
			var body playgroundResponse
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Secret == "" || body.Key.OrganizationID != company || body.Key.UserID != users["owner"].ID {
				t.Fatalf("allowed member response: status=%d; sensitive body omitted", response.Code)
			}
		})
	}
	denied := func(name, actor string, status int, code string) {
		for _, action := range []string{"create", "restore"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				response := call(action, actor)
				var body struct {
					Error  struct{ Code, Message string }
					Secret string
				}
				if response.Code != status || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Error.Code != code || body.Secret != "" || strings.Contains(body.Error.Message, "synthetic") {
					t.Fatalf("workspace denial: status=%d code=%q; sensitive body omitted", response.Code, body.Error.Code)
				}
				if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("managed response can be cached")
				}
			})
		}
	}
	denied("non-member", "outsider", 403, "forbidden")
	if err := workspaces.SetOrganizationMember(ctx, users["manager"].ID, company, users["owner"].ID, org.Developer, nil, false, true); err != nil {
		t.Fatal(err)
	}
	denied("removed-member", "owner", 403, "forbidden")
	s.system = unavailablePlaygroundWorkspaces{workspaces}
	denied("storage-unavailable", "manager", 503, "service_unavailable")
	for _, query := range []string{
		`SELECT count(*) FROM user_api_keys WHERE organization_id=$1`,
		`SELECT count(*) FROM playground_key_slots WHERE workspace=$1`,
		`SELECT count(*) FROM playground_key_requests WHERE workspace=$1`,
	} {
		var count int
		if err := db.QueryRow(query, company).Scan(&count); err != nil || count != 1 {
			t.Fatalf("denied request changed key/vault/request state: count=%d err=%v", count, err)
		}
	}
	var revoked bool
	if err := db.QueryRow(`SELECT revoked_at IS NOT NULL FROM user_api_keys WHERE organization_id=$1`, company).Scan(&revoked); err != nil || !revoked {
		t.Fatal("membership removal did not revoke the original key", err)
	}
}
