package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

func TestCompanyKeySupportRealMiddlewareAndPostgres(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	people := NewAuthRepository(db)
	sys := NewSystemRepository(db)
	keys := NewAPIKeyRepository(db)
	users := []identity.User{}
	for _, name := range []string{"Admin", "Recipient", "Manager A", "Manager B"} {
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@private.fixture.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		users = append(users, u)
	}
	admin, recipient, a, b := users[0], users[1], users[2], users[3]
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, admin.ID)
	companies := []org.Organization{}
	for _, name := range []string{"A", "B"} {
		c, e := sys.CreateOrganization(ctx, admin.ID, "Support "+name, recipient.ID, uuid.NewString()+".fixture.test")
		if e != nil {
			t.Fatal(e)
		}
		companies = append(companies, c)
	}
	for i, u := range []identity.User{a, b} {
		if e := sys.SetUserRole(ctx, admin.ID, u.ID, "client_manager"); e != nil {
			t.Fatal(e)
		}
		links := []identity.Contact{{Label: "Published " + u.Name, URL: "https://support.fixture.test/" + u.ID}}
		if _, e := people.UpdateProfile(ctx, u.ID, auth.ProfileUpdate{Contacts: &links}); e != nil {
			t.Fatal(e)
		}
		if e := sys.AssignClientManager(ctx, admin.ID, companies[i].ID, u.ID); e != nil {
			t.Fatal(e)
		}
	}
	makeKey := func(company string) (apikey.Key, string) {
		t.Helper()
		k, s, e := apikey.Generate(apikey.CreateInput{UserID: recipient.ID, OrganizationID: company, Name: "Synthetic support", ResourceMode: "shared", Scopes: []string{apikey.Generation}}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		if e = keys.Create(identity.WithUser(ctx, recipient), recipient.ID, k, apikey.Hash(s)); e != nil {
			t.Fatal(e)
		}
		return k, s
	}
	ka, sa := makeKey(companies[0].ID)
	_, sb := makeKey(companies[1].ID)
	_, personal := makeKey("")
	token := uuid.NewString() + uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	if e := people.CreateSession(ctx, hash[:], recipient.ID, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	login, e := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("t", 32), PublicURL: "https://support.fixture.test"})
	if e != nil {
		t.Fatal(e)
	}
	h := api.NewGateway(api.Dependencies{Auth: login, System: sys, Keys: keys, APIKey: "synthetic-service"})
	send := func(method, path, key string, session bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://support.fixture.test"+path, nil)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if session {
			r.AddCookie(&http.Cookie{Name: "media_gateway_session", Value: token})
		}
		if method != "GET" {
			r.Header.Set("Origin", "https://support.fixture.test")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if e := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := map[string]int{}
	for _, table := range []string{"organizations", "organization_members", "user_api_keys", "organization_credits", "tasks"} {
		before[table] = count(table)
	}
	for _, tc := range []struct{ name, key, want string }{{"A", sa, a.ID}, {"B", sb, b.ID}} {
		t.Run("bound-company-"+tc.name, func(t *testing.T) {
			w := send("GET", "/v1/key/support", tc.key, false)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			var out org.Support
			if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
				t.Fatal(e)
			}
			if out.ClientManager == nil || out.ClientManager.ID != tc.want || len(out.ClientManager.Contacts) != 1 {
				t.Fatal(w.Body)
			}
			for _, private := range []string{recipient.Email, a.Email, b.Email, `"email"`, `"theme"`, `"is_system_admin"`} {
				if strings.Contains(w.Body.String(), private) {
					t.Fatal("private data leak", private)
				}
			}
			if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("cacheable")
			}
		})
	}
	for _, tc := range []struct {
		name, method, path, key string
		session                 bool
		want                    int
	}{
		{"query", "GET", "/v1/key/support?organization_id=" + companies[1].ID, sa, false, 400}, {"path", "GET", "/v1/key/support/" + companies[1].ID, sa, false, 404}, {"session", "GET", "/v1/key/support", "", true, 401}, {"personal", "GET", "/v1/key/support", personal, false, 403}, {"service", "GET", "/v1/key/support", "synthetic-service", false, 403}, {"old-key", "GET", "/v1/organizations/" + companies[0].ID + "/support", sa, true, 403}, {"browser", "GET", "/v1/organizations/" + companies[0].ID + "/support", "", true, 200}, {"profile", "PATCH", "/v1/me", sa, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := send(tc.method, tc.path, tc.key, tc.session)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
	t.Run("stored-insufficient-scope", func(t *testing.T) {
		exec(`UPDATE user_api_keys SET scopes='["keys:manage"]'::jsonb WHERE id=$1`, ka.ID)
		if w := send("GET", "/v1/key/support", sa, true); w.Code != 403 {
			t.Fatal(w.Code, w.Body)
		}
		exec(`UPDATE user_api_keys SET scopes='["generation"]'::jsonb WHERE id=$1`, ka.ID)
	})
	t.Run("expiry-and-revocation", func(t *testing.T) {
		exec(`UPDATE user_api_keys SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, ka.ID)
		if w := send("GET", "/v1/key/support", sa, true); w.Code != 401 {
			t.Fatal(w.Code, w.Body)
		}
		exec(`UPDATE user_api_keys SET expires_at=NULL,revoked_at=NOW() WHERE id=$1`, ka.ID)
		if w := send("GET", "/v1/key/support", sa, true); w.Code != 401 {
			t.Fatal(w.Code, w.Body)
		}
		exec(`UPDATE user_api_keys SET revoked_at=NULL WHERE id=$1`, ka.ID)
	})
	t.Run("live-membership", func(t *testing.T) {
		exec(`UPDATE organization_members SET removed_at=NOW() WHERE organization_id=$1 AND user_id=$2`, companies[0].ID, recipient.ID)
		if w := send("GET", "/v1/key/support", sa, true); w.Code != 401 {
			t.Fatal(w.Code, w.Body)
		}
		if w := send("GET", "/v1/key/support", sb, false); w.Code != 200 {
			t.Fatal("other bound company lost", w.Code, w.Body)
		}
		exec(`UPDATE organization_members SET removed_at=NULL WHERE organization_id=$1 AND user_id=$2`, companies[0].ID, recipient.ID)
	})
	t.Run("unpublished-and-unassigned", func(t *testing.T) {
		empty := []identity.Contact{}
		if _, e := people.UpdateProfile(ctx, a.ID, auth.ProfileUpdate{Contacts: &empty}); e != nil {
			t.Fatal(e)
		}
		w := send("GET", "/v1/key/support", sa, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"contacts":[]`) {
			t.Fatal(w.Code, w.Body)
		}
		if e := sys.AssignClientManager(ctx, admin.ID, companies[0].ID, ""); e != nil {
			t.Fatal(e)
		}
		w = send("GET", "/v1/key/support", sa, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"client_manager":null`) {
			t.Fatal(w.Code, w.Body)
		}
	})
	for table, n := range before {
		if count(table) != n {
			t.Fatal("read endpoint changed domain row count", table)
		}
	}
}
