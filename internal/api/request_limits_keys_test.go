package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/queue"
	"github.com/google/uuid"
)

type companyKeyCapabilities interface {
	OrganizationKeys(context.Context, string, string) ([]apikey.Key, error)
	RevokeForActor(context.Context, string, string) error
	OrganizationUsage(context.Context, string, string, time.Time) ([]apikey.MonthlyUsage, error)
}

type scopedKeysError struct {
	apikey.Repository
	err error
}

func (k scopedKeysError) OrganizationKeys(context.Context, string, string) ([]apikey.Key, error) {
	return nil, k.err
}
func (k scopedKeysError) OrganizationUsage(context.Context, string, string, time.Time) ([]apikey.MonthlyUsage, error) {
	return nil, k.err
}
func (k scopedKeysError) RevokeForActor(context.Context, string, string) error { return k.err }

func TestLimitedKeysCapabilities(t *testing.T) {
	ctx := context.Background()
	f := newLimitsFixture()
	limiter := &captureLimiter{}
	base := &memoryKeys{}
	missing, ok := WithRequestLimits(base, f, limiter).(companyKeyCapabilities)
	if !ok {
		t.Fatal("decorator hides company management capabilities")
	}
	if _, err := missing.OrganizationKeys(ctx, "actor", "company"); !errors.Is(err, apikey.ErrForbidden) {
		t.Fatal("missing scoped list must fail closed", err)
	}
	if _, err := missing.OrganizationUsage(ctx, "actor", "company", time.Now()); !errors.Is(err, apikey.ErrForbidden) {
		t.Fatal("missing scoped usage must fail closed", err)
	}
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "owner key"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = base.Create(ctx, "owner", key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	if err = missing.RevokeForActor(ctx, "other", key.ID); !errors.Is(err, apikey.ErrNotFound) {
		t.Fatal("owner-only fallback escalated", err)
	}
	if err = missing.RevokeForActor(ctx, "owner", key.ID); err != nil {
		t.Fatal("owner-only revoke fallback", err)
	}
	sentinel := errors.New("scoped store unavailable")
	forward := WithRequestLimits(scopedKeysError{base, sentinel}, f, limiter).(companyKeyCapabilities)
	if _, err = forward.OrganizationKeys(ctx, "actor", "org"); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err = forward.OrganizationUsage(ctx, "actor", "org", time.Now()); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err = forward.RevokeForActor(ctx, "actor", "id"); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

func TestLimitedKeysCompanyPostgres(t *testing.T) {
	db := workspaceDatabase(t)
	ctx := context.Background()
	people := postgres.NewAuthRepository(db)
	sys := postgres.NewSystemRepository(db)
	users := map[string]identity.User{}
	for _, name := range []string{"root", "super", "member", "developer", "outsider"} {
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), name+"@example.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		users[name] = u
	}
	if e := sys.SetAdmin(ctx, "", users["root"].ID, true, true); e != nil {
		t.Fatal(e)
	}
	org, e := sys.CreateOrganization(ctx, users["root"].ID, "Decorated company", users["super"].ID, uuid.NewString()+".example.test")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"member", "developer"} {
		if e = sys.SetOrganizationMember(ctx, users["root"].ID, org.ID, users[name].ID, name, nil, true, false); e != nil {
			t.Fatal(e)
		}
	}
	foreign, e := sys.CreateOrganization(ctx, users["root"].ID, "Other company", users["super"].ID, uuid.NewString()+".example.test")
	if e != nil {
		t.Fatal(e)
	}
	settings, e := sys.Settings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	settings.SharedResources = true
	settings.PersonalProviders = false
	if _, e = sys.SaveSettings(ctx, users["root"].ID, settings); e != nil {
		t.Fatal(e)
	}
	redisURL := os.Getenv("GATEWAY_TEST_REDIS_URL")
	if redisURL == "" {
		t.Fatal("requires disposable Redis")
	}
	limiter, e := queue.Open(ctx, redisURL, "company-keys-"+uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	defer limiter.Close()
	repo := postgres.NewAPIKeyRepository(db)
	decorated := WithRequestLimits(repo, sys, limiter)
	s, _ := newPersonalTestServer()
	s.keys = decorated
	s.system = sys
	s.auth = playgroundBrowserAuth{users: users}
	h := s.handler()
	call := func(method, path, body, actor, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		} else {
			r.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	issue := func(company, recipient string, scopes []string) (apikey.Key, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"name": "Scoped fixture", "organization_id": company, "user_id": users[recipient].ID, "scopes": scopes})
		w := call("POST", "/v1/api-keys", string(body), "super", "")
		var result struct {
			apikey.Key
			Secret string `json:"secret"`
		}
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("issue: %d %s", w.Code, w.Body)
		}
		return result.Key, result.Secret
	}
	memberKey, secret := issue(org.ID, "member", []string{apikey.Generation})
	ownerKey, ownerSecret := issue(org.ID, "super", []string{apikey.Generation, apikey.KeysManage})
	foreignKey, _ := issue(foreign.ID, "super", []string{apikey.Generation})
	list := call("GET", "/v1/api-keys?organization_id="+org.ID, "", "super", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), memberKey.ID) || strings.Contains(list.Body.String(), foreignKey.ID) {
		t.Fatalf("manager scoped list: %d %s", list.Code, list.Body)
	}
	usage := call("GET", "/v1/api-keys/usage?organization_id="+org.ID, "", "super", "")
	if usage.Code != 200 || !strings.Contains(usage.Body.String(), memberKey.ID) || strings.Contains(usage.Body.String(), foreignKey.ID) {
		t.Fatalf("manager usage: %d %s", usage.Code, usage.Body)
	}
	for _, path := range []string{"/v1/api-keys?organization_id=" + org.ID, "/v1/api-keys/usage?organization_id=" + org.ID} {
		if w := call("GET", path, "", "outsider", ""); w.Code == 200 {
			t.Fatal("foreign company read", w.Body)
		}
	}
	if w := call("GET", "/v1/api-keys?organization_id="+foreign.ID, "", "", ownerSecret); w.Code != 403 {
		t.Fatal("bound list crossed company", w.Code, w.Body)
	}
	if w := call("GET", "/v1/api-keys/usage?organization_id="+foreign.ID, "", "", ownerSecret); w.Code != 403 {
		t.Fatal("bound usage crossed company", w.Code, w.Body)
	}
	if w := call("DELETE", "/v1/api-keys/"+foreignKey.ID, "", "", ownerSecret); w.Code != 404 {
		t.Fatal("bound revoke crossed company", w.Code, w.Body)
	}
	if w := call("DELETE", "/v1/api-keys/"+memberKey.ID, "", "", secret); w.Code != 403 {
		t.Fatal("generation key gained manage scope", w.Code, w.Body)
	}
	if w := call("DELETE", "/v1/api-keys/"+ownerKey.ID, "", "developer", ""); w.Code != 403 {
		t.Fatal("developer revoked superior key", w.Code, w.Body)
	}
	if w := call("DELETE", "/v1/api-keys/"+memberKey.ID, "", "outsider", ""); w.Code != 403 {
		t.Fatal("outsider revoked member", w.Code, w.Body)
	}
	if w := call("GET", "/v1/key", "", "", secret); w.Code != 200 {
		t.Fatal("active key rejected by HTTP authentication", w.Code)
	}
	if _, _, e = decorated.Authenticate(ctx, apikey.Hash(secret)); e != nil {
		t.Fatal("fixture key unauthenticated", e)
	}
	if w := call("DELETE", "/v1/api-keys/"+memberKey.ID, "", "super", ""); w.Code != 204 {
		t.Fatal("manager revoke", w.Code, w.Body)
	}
	if w := call("GET", "/v1/key", "", "", secret); w.Code != 401 {
		t.Fatal("revoked key accepted by HTTP authentication", w.Code)
	}
	if _, _, e = decorated.Authenticate(ctx, apikey.Hash(secret)); e == nil {
		t.Fatal("revoked key still authenticates")
	}
	if e = repo.Revoke(ctx, users["member"].ID, ownerKey.ID); !errors.Is(e, apikey.ErrNotFound) {
		t.Fatal("internal owner-only Revoke changed", e)
	}
	if w := call("DELETE", "/v1/api-keys/"+ownerKey.ID, "", "", ownerSecret); w.Code != 204 {
		t.Fatal("owner self revoke", w.Code, w.Body)
	}
	policy, e := sys.LimitSettings(ctx, users["root"].ID, "")
	if e != nil {
		t.Fatal(e)
	}
	one := 1
	policy.System.Values["user_key_creations_minute"] = &one
	if _, e = sys.SaveLimits(ctx, users["root"].ID, "", policy.System); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]string{"name": "quota denied", "organization_id": org.ID})
	if w := call("POST", "/v1/api-keys", string(body), "super", ""); w.Code != 429 {
		t.Fatal("real Redis admission bypassed", w.Code, w.Body)
	}
}
