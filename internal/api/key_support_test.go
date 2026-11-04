package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
)

type keySupportFixture struct {
	*organizationFixture
	removed bool
	calls   int
	cards   map[string]org.Support
}

func (f *keySupportFixture) OrganizationAccess(_ context.Context, actor, id string) (string, error) {
	if f.removed || actor != "alice" || (id != fixtureOrg && id != fixtureOtherOrg) {
		return "", org.ErrForbidden
	}
	return org.MemberRole, nil
}
func (f *keySupportFixture) OrganizationSupport(ctx context.Context, actor, id string) (org.Support, error) {
	f.calls++
	if _, err := f.OrganizationAccess(ctx, actor, id); err != nil {
		return org.Support{}, err
	}
	return f.cards[id], nil
}
func supportKey(t *testing.T, keys *memoryKeys, company string, scopes []string, expires *time.Time) (apikey.Key, string) {
	t.Helper()
	k, secret, err := apikey.Generate(apikey.CreateInput{Name: "Synthetic support", OrganizationID: company, ResourceMode: "shared", Scopes: []string{apikey.Generation}, ExpiresAt: expires}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	k.Scopes = scopes // Exercise a stored legacy/invalid scope without weakening issuance.
	if err = keys.Create(context.Background(), "alice", k, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	return k, secret
}
func supportServer() (*Server, *memoryKeys, *keySupportFixture) {
	s, keys := newPersonalTestServer()
	f := &keySupportFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}}, cards: map[string]org.Support{
		fixtureOrg:      {ClientManager: &org.SupportPerson{ID: "manager-a", Name: "Published A", Contacts: []identity.Contact{{Label: "Published", URL: "https://support.example.test/a"}}}},
		fixtureOtherOrg: {ClientManager: &org.SupportPerson{ID: "manager-b", Name: "Published B", Contacts: []identity.Contact{}}},
	}}
	s.system = f
	return s, keys, f
}
func TestCompanyKeySupportAuthenticationAndBinding(t *testing.T) {
	s, keys, f := supportServer()
	h := s.handler()
	_, a := supportKey(t, keys, fixtureOrg, []string{apikey.Generation}, nil)
	_, b := supportKey(t, keys, fixtureOtherOrg, []string{apikey.Generation}, nil)
	_, personal := supportKey(t, keys, "", []string{apikey.Generation}, nil)
	_, scope := supportKey(t, keys, fixtureOrg, []string{apikey.KeysManage}, nil)
	revoked, revokedSecret := supportKey(t, keys, fixtureOrg, []string{apikey.Generation}, nil)
	keys.Revoke(context.Background(), "alice", revoked.ID)
	future := time.Now().Add(time.Hour)
	expired, expiredSecret := supportKey(t, keys, fixtureOrg, []string{apikey.Generation}, &future)
	past := time.Now().Add(-time.Hour)
	for i := range keys.records {
		if keys.records[i].key.ID == expired.ID {
			keys.records[i].key.ExpiresAt = &past
		}
	}
	for _, tc := range []struct {
		name, key, actor, path string
		want                   int
	}{
		{"company-a", a, "", "/v1/key/support", 200}, {"company-b", b, "", "/v1/key/support", 200},
		{"anonymous", "", "", "/v1/key/support", 401}, {"session-only", "", "alice", "/v1/key/support", 401}, {"invalid", "gw_invalid", "alice", "/v1/key/support", 401},
		{"revoked", revokedSecret, "alice", "/v1/key/support", 401}, {"expired", expiredSecret, "alice", "/v1/key/support", 401}, {"personal", personal, "", "/v1/key/support", 403}, {"service", "service-secret", "", "/v1/key/support", 403}, {"scope", scope, "", "/v1/key/support", 403},
		{"query-substitution", a, "", "/v1/key/support?organization_id=" + fixtureOtherOrg, 400}, {"encoded-query", a, "", "/v1/key/support?org=%32%32%32", 400}, {"path-substitution", a, "", "/v1/key/support/" + fixtureOtherOrg, 404},
		{"old-browser-route", a, "alice", "/v1/organizations/" + fixtureOrg + "/support", 403}, {"profile", a, "alice", "/v1/me", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := "GET"
			if tc.name == "profile" {
				method = "PATCH"
			}
			before := f.calls
			w := sendTest(h, method, tc.path, "", tc.actor, tc.key)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d: %s", w.Code, tc.want, w.Body)
			}
			if tc.want != 200 && f.calls != before {
				t.Fatal("denied request reached support repository")
			}
			if tc.want == 200 {
				var card org.Support
				if err := json.Unmarshal(w.Body.Bytes(), &card); err != nil {
					t.Fatal(err)
				}
				want := "manager-a"
				if tc.key == b {
					want = "manager-b"
				}
				if card.ClientManager == nil || card.ClientManager.ID != want {
					t.Fatal(w.Body)
				}
				if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("cacheable support")
				}
				for _, field := range []string{`"email"`, `"theme"`, `"is_system_admin"`, `"scopes"`} {
					if strings.Contains(w.Body.String(), field) {
						t.Fatal("private field", field)
					}
				}
			}
		})
	}
	f.removed = true
	if w := sendTest(h, "GET", "/v1/key/support", "", "", a); w.Code != 403 {
		t.Fatal("removed membership", w.Code, w.Body)
	}
	f.removed = false
	f.cards[fixtureOrg] = org.Support{}
	if w := sendTest(h, "GET", "/v1/key/support", "", "", a); w.Code != 200 || !strings.Contains(w.Body.String(), `"client_manager":null`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := sendTest(h, "GET", "/v1/organizations/"+fixtureOtherOrg+"/support", "", "alice", ""); w.Code != 200 {
		t.Fatal("browser regression", w.Code, w.Body)
	}
}
func TestCompanyKeySupportThroughExistingProxy(t *testing.T) {
	central, keys, f := supportServer()
	_, secret := supportKey(t, keys, fixtureOrg, []string{apikey.Generation}, nil)
	centralHandler := central.handler()
	const local = "https://local.example.test"
	calls := 0
	client := &http.Client{Transport: companyRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "console.sociaro.ai" || r.URL.Path != "/v1/key/support" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Company-Context") != "" {
			t.Fatal("unexpected forwarded request")
		}
		w := httptest.NewRecorder()
		centralHandler.ServeHTTP(w, r)
		return w.Result(), nil
	})}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("s", 32))
	encrypted, _ := cipher.Encrypt([]byte(secret))
	hash := sha256.Sum256([]byte(secret))
	record := remote.Record{Access: wa.CompanyAccess, Owner: "alice", Version: 1, Active: true, Origin: installation.DefaultCentralOrigin, OrganizationID: fixtureOrg, TokenHash: hash[:], Ciphertext: encrypted}
	repo := &remoteFixture{records: map[string]remote.Record{"alice": record}}
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true, "bob": true}}
	s.remoteWorkspaceManager = remote.New(repo, cipher, client)
	h, err := WithPublicAPI(s.handler(), local, local, "")
	if err != nil {
		t.Fatal(err)
	}
	send := func(actor, contextID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", local+"/auth/company-api/v1/key/support", nil)
		r.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
		r.Header.Set("Authorization", "Bearer "+secret)
		r.Header.Set("X-Company-Context", contextID)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := send("alice", record.SnapshotID()); w.Code != 200 || !strings.Contains(w.Body.String(), "Published A") {
		t.Fatal(w.Code, w.Body)
	}
	before := calls
	for _, tc := range []struct{ actor, contextID string }{{"bob", record.SnapshotID()}, {"alice", "stale"}} {
		if w := send(tc.actor, tc.contextID); w.Code < 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if calls != before {
		t.Fatal("denied local viewer forwarded")
	}
	f.removed = true
	if w := send("alice", record.SnapshotID()); w.Code != 403 {
		t.Fatal("central denied membership hidden", w.Code, w.Body)
	}
	record.Active = false
	repo.records["alice"] = record
	before = calls
	if w := send("alice", "stale"); w.Code < 400 || calls != before {
		t.Fatal("disconnected local fallback", w.Code, w.Body)
	}
}
