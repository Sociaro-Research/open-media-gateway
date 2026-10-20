package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

type browserAuthStub struct{}

func (browserAuthStub) Authenticate(r *http.Request) (identity.User, error) {
	cookie, err := r.Cookie("test_session")
	if err != nil || (cookie.Value != "alice" && cookie.Value != "bob") {
		return identity.User{}, errors.New("no session")
	}
	return identity.User{ID: cookie.Value, Name: cookie.Value, Email: cookie.Value + "@example.test"}, nil
}
func (browserAuthStub) Login(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "test_session", Value: "alice", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", 302)
}
func (browserAuthStub) Callback(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", 302)
}
func (browserAuthStub) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "test_session", Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(204)
}

type storedTestKey struct {
	owner string
	key   apikey.Key
	hash  []byte
}
type memoryKeys struct {
	mu      sync.Mutex
	records []storedTestKey
}

func (m *memoryKeys) Create(_ context.Context, owner string, key apikey.Key, hash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, storedTestKey{owner, key, slices.Clone(hash)})
	return nil
}
func (m *memoryKeys) List(_ context.Context, owner string) ([]apikey.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := []apikey.Key{}
	for _, r := range m.records {
		if r.owner == owner {
			keys = append(keys, r.key)
		}
	}
	return keys, nil
}
func (m *memoryKeys) Usage(ctx context.Context, owner string, month time.Time) ([]apikey.MonthlyUsage, error) {
	keys, _ := m.List(ctx, owner)
	result := []apikey.MonthlyUsage{}
	for _, key := range keys {
		result = append(result, apikey.MonthlyUsage{KeyID: key.ID, Month: month.UTC().Format("2006-01")})
	}
	return result, nil
}

func (m *memoryKeys) Revoke(_ context.Context, owner, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.records {
		if r.owner == owner && r.key.ID == id {
			now := time.Now()
			m.records[i].key.RevokedAt = &now
			return nil
		}
	}
	return apikey.ErrNotFound
}
func (m *memoryKeys) Authenticate(_ context.Context, hash []byte) (identity.User, apikey.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.records {
		if slices.Equal(hash, r.hash) && r.key.RevokedAt == nil && (r.key.ExpiresAt == nil || r.key.ExpiresAt.After(time.Now())) {
			return identity.User{ID: r.owner, Name: r.owner}, r.key, nil
		}
	}
	return identity.User{}, apikey.Key{}, apikey.ErrNotFound
}

type accountService struct {
	serviceStub
	mu    sync.Mutex
	tasks []core.Task
}

func (s *accountService) CreateMediaRequest(ctx context.Context, input core.CreateMediaInput, _ string) (core.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(ctx), APIKeyID: identity.APIKeyID(ctx), Status: core.TaskQueued, Prompt: input.Prompt, Model: input.Model, Provider: input.Provider, MediaType: input.MediaType, Options: input.Options, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	cost := core.FreeCost(time.Now().UTC())
	task.Cost = &cost
	s.tasks = append(s.tasks, task)
	return task, nil
}
func (s *accountService) ListTasks(ctx context.Context, _ int) ([]core.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := []core.Task{}
	for _, t := range s.tasks {
		if t.OwnerID == identity.OwnerID(ctx) {
			tasks = append(tasks, t)
		}
	}
	return tasks, nil
}
func (s *accountService) GetTask(ctx context.Context, id string) (core.Task, error) {
	tasks, _ := s.ListTasks(ctx, 100)
	for _, task := range tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return core.Task{}, core.ErrTaskNotFound
}
func newPersonalTestServer() (*Server, *memoryKeys) {
	keys := &memoryKeys{}
	return &Server{service: &accountService{}, health: healthStub{}, apiKey: "service-secret", auth: browserAuthStub{}, keys: keys, providerAdmin: &providerAdminStub{provider: providers.AdminProvider{ID: "mock", Name: "Mock"}}}, keys
}
func sendTest(h http.Handler, method, path, body, session, secret string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if session != "" {
		r.AddCookie(&http.Cookie{Name: "test_session", Value: session})
	}
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func issueTestKey(t *testing.T, h http.Handler, owner, body string) (apikey.Key, string) {
	t.Helper()
	w := sendTest(h, "POST", "/v1/api-keys", body, owner, "")
	if w.Code != 201 {
		t.Fatalf("create key: %d %s", w.Code, w.Body)
	}
	var v struct {
		apikey.Key
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Secret) != 46 || !strings.HasPrefix(v.Secret, "gw_") {
		t.Fatal("invalid secret format")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("key response must not be cached")
	}
	return v.Key, v.Secret
}

func TestPersonalKeyAccountParityAndRevocation(t *testing.T) {
	s, store := newPersonalTestServer()
	h := s.handler()
	first, secret := issueTestKey(t, h, "alice", `{"name":"Bot"}`)
	_, second := issueTestKey(t, h, "alice", `{"name":"Second application"}`)
	_, bob := issueTestKey(t, h, "bob", `{"name":"Bob bot"}`)
	list := sendTest(h, "GET", "/v1/api-keys", "", "alice", "")
	if strings.Contains(list.Body.String(), secret) || strings.Contains(list.Body.String(), "token_hash") || strings.Contains(list.Body.String(), "Bob bot") {
		t.Fatal("list leaked key or account data")
	}
	if string(store.records[0].hash) == secret || len(store.records[0].hash) != 32 {
		t.Fatal("secret must only be persisted as hash")
	}
	create := sendTest(h, "POST", "/v1/generations", `{"prompt":"hello","model":"mock/mock-video-v1"}`, "", secret)
	if create.Code != 202 {
		t.Fatalf("create: %s", create.Body)
	}
	var task core.Task
	_ = json.Unmarshal(create.Body.Bytes(), &task)
	if task.APIKeyID == nil || *task.APIKeyID != first.ID {
		t.Fatal("missing originating key attribution")
	}
	for _, credential := range []struct{ session, secret string }{{"", secret}, {"", second}} {
		w := sendTest(h, "GET", "/v1/generations/"+task.ID, "", credential.session, credential.secret)
		if w.Code != 200 {
			t.Fatalf("same account cannot read task: %d", w.Code)
		}
	}
	for _, credential := range []string{bob, "service-secret"} {
		if w := sendTest(h, "GET", "/v1/generations/"+task.ID, "", "", credential); w.Code != 404 {
			t.Fatalf("cross-account read: %d", w.Code)
		}
	}
	if w := sendTest(h, "DELETE", "/v1/api-keys/"+first.ID, "", "bob", ""); w.Code != 404 {
		t.Fatal("cross-account revoke")
	}
	for i := 0; i < 2; i++ {
		if w := sendTest(h, "DELETE", "/v1/api-keys/"+first.ID, "", "alice", ""); w.Code != 204 {
			t.Fatal("revoke not idempotent")
		}
	}
	if w := sendTest(h, "GET", "/v1/generations", "", "alice", secret); w.Code != 401 {
		t.Fatal("revoked key silently fell back to session")
	}
	if w := sendTest(h, "GET", "/v1/generations/"+task.ID, "", "", second); w.Code != 200 {
		t.Fatal("revocation affected another key or task")
	}
}

func TestKeyPermissionsAndNoEscalation(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := s.handler()
	_, basic := issueTestKey(t, h, "alice", `{"name":"Basic"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"Key manager","scopes":["generation","keys:manage"]}`)
	_, provider := issueTestKey(t, h, "alice", `{"name":"Provider manager","scopes":["generation","providers:manage"]}`)
	for _, path := range []string{"/v1/api-keys", "/v1/admin/providers"} {
		if w := sendTest(h, "GET", path, "", "", basic); w.Code != 403 {
			t.Fatalf("scope not enforced at %s: %d", path, w.Code)
		}
	}
	if w := sendTest(h, "GET", "/v1/admin/providers", "", "", provider); w.Code != 200 {
		t.Fatal("provider scope rejected")
	}
	if w := sendTest(h, "POST", "/v1/api-keys", `{"name":"Escalation","scopes":["generation","providers:manage"]}`, "", manager); w.Code != 403 {
		t.Fatal("scope escalation allowed")
	}
	if w := sendTest(h, "POST", "/v1/api-keys", `{"name":"Child"}`, "", manager); w.Code != 201 {
		t.Fatal("scoped key creation failed")
	}
	if w := sendTest(h, "GET", "/v1/api-keys", "", "", "service-secret"); w.Code != 403 {
		t.Fatal("system can access personal keys")
	}
	if w := sendTest(h, "GET", "/v1/me", "", "", basic); w.Code != 200 || !strings.Contains(w.Body.String(), "alice") {
		t.Fatal("personal profile unavailable")
	}
}

func TestAPIKeyValidationAndCrossOriginProtection(t *testing.T) {
	s, store := newPersonalTestServer()
	h := s.handler()
	for _, body := range []string{`{}`, `null`, `{"name":" "}`, `{"name":"x","scopes":["admin"]}`, `{"name":"x","scopes":["keys:manage"]}`, `{"name":"x","expires_at":"2000-01-01T00:00:00Z"}`, `{"name":"x","secret":"injected"}`, `{"name":"x"} garbage`, `{"name":"x"} {}`, `{"name":"` + strings.Repeat("a", 5000) + `"}`} {
		if w := sendTest(h, "POST", "/v1/api-keys", body, "alice", ""); w.Code != 400 {
			t.Fatalf("invalid body accepted: %d", w.Code)
		}
	}
	if len(store.records) != 0 {
		t.Fatal("invalid key persisted")
	}
	req := httptest.NewRequest("POST", "http://gateway.test/v1/api-keys", strings.NewReader(`{"name":"Attack"}`))
	req.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
	req.Header.Set("Origin", "https://attacker.test")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("cross-origin key creation accepted")
	}
	req = httptest.NewRequest("POST", "http://gateway.test/v1/api-keys", strings.NewReader(`{"name":"Allowed"}`))
	req.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
	req.Header.Set("Origin", "http://gateway.test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatal("same-origin key creation rejected")
	}
	for _, secret := range []string{"invalid", "gw_" + strings.Repeat("x", 43)} {
		if w := sendTest(h, "GET", "/v1/generations", "", "alice", secret); w.Code != 401 {
			t.Fatal("invalid explicit credential fell back to session")
		}
	}
	empty := New(serviceStub{}, healthStub{}, "")
	if w := sendTest(empty, "GET", "/v1/generations", "", "", ""); w.Code != 401 {
		t.Fatal("empty configured key grants access")
	}
}

func TestExpiredKeyAndConflictingHeaders(t *testing.T) {
	s, store := newPersonalTestServer()
	h := s.handler()
	_, secret := issueTestKey(t, h, "alice", `{"name":"Expiring"}`)
	past := time.Now().Add(-time.Hour)
	store.records[0].key.ExpiresAt = &past
	if w := sendTest(h, "GET", "/v1/providers", "", "", secret); w.Code != 401 {
		t.Fatal("expired key accepted")
	}
	req := httptest.NewRequest("GET", "/v1/providers", nil)
	req.Header.Set("Authorization", "Bearer service-secret")
	req.Header.Add("Authorization", "Bearer service-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("ambiguous credentials accepted")
	}
}

type previewBrowserAuth struct{ browserAuthStub }

func (a previewBrowserAuth) Authenticate(r *http.Request) (identity.User, error) {
	user, err := a.browserAuthStub.Authenticate(r)
	if err == nil {
		user.ID = uuid.NewSHA1(uuid.NameSpaceDNS, []byte(user.Name)).String()
		user.SessionID = user.ID
	}
	return user, err
}

// Opt-in local UI fixture: no production accounts or external provider calls.
func TestConsolePreview(t *testing.T) {
	if os.Getenv("GATEWAY_CONSOLE_PREVIEW") != "1" {
		t.Skip("local browser fixture")
	}
	s, _ := newPersonalTestServer()
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 30<<20)
	if err != nil {
		t.Fatal(err)
	}
	s.storage = store
	s.mediaSigner = newMediaURLSigner("preview-signing-secret", time.Hour)
	s.references = references.New(&referenceMemory{items: map[string]references.Image{}}, store)
	s.auth = previewBrowserAuth{}
	ctx := identity.WithOwnerID(context.Background(), uuid.NewSHA1(uuid.NameSpaceDNS, []byte("alice")).String())
	for _, name := range []string{"First reference.png", "Second reference.png"} {
		data := append(referencePNG(t), []byte(name)...)
		if _, err := s.references.Upload(ctx, name, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal(http.ListenAndServe("127.0.0.1:18764", s.handler()))
}

func TestPublicReferenceAndRouteCoverage(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := s.handler()
	for _, path := range []string{"/openapi.yaml", "/openapi.json", "/site.json"} {
		if w := sendTest(h, "GET", path, "", "", ""); w.Code != 200 {
			t.Fatalf("public documentation %s: %d", path, w.Code)
		}
	}
	w := sendTest(h, "GET", "/openapi.json", "", "", "")
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	// Keep every registered business/session endpoint in the downloadable contract.
	source, err := os.ReadFile("http.go")
	if err != nil {
		t.Fatal(err)
	}
	referenceSource, err := os.ReadFile("references.go")
	if err != nil {
		t.Fatal(err)
	}
	source = append(source, referenceSource...)
	organizationSource, err := os.ReadFile("organizations.go")
	if err != nil {
		t.Fatal(err)
	}
	source = append(source, organizationSource...)
	for _, name := range []string{"openai.go", "payments.go", "audit.go", "provider_assets.go"} {
		adapterSource, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		source = append(source, adapterSource...)
	}
	routePattern := regexp.MustCompile(`mux.Handle(?:Func)?\("(GET|POST|PUT|PATCH|DELETE|HEAD) ([^"]+)"`)
	normalize := func(path string) string { return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "{id}") }
	routes := map[string]bool{}
	for _, match := range routePattern.FindAllStringSubmatch(string(source), -1) {
		path := match[2]
		if !strings.HasPrefix(path, "/v1/") && !strings.HasPrefix(path, "/auth/") && !strings.HasPrefix(path, "/media/") && !strings.HasPrefix(path, "/references/") && path != "/healthz" && path != "/site.json" {
			continue
		}
		routes[strings.ToLower(match[1])+" "+normalize(path)] = true
	}
	operations := map[string]bool{}
	for path, item := range spec.Paths {
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head"} {
			if _, ok := item[method]; ok {
				operations[method+" "+normalize(path)] = true
			}
		}
	}
	for route := range routes {
		if !operations[route] {
			t.Errorf("undocumented route: %s", route)
		}
	}
	for route := range operations {
		if !routes[route] {
			t.Errorf("documented route missing from server: %s", route)
		}
	}
}

func TestMonthlyUsageAccessAndMonthValidation(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := s.handler()
	_, generation := issueTestKey(t, h, "alice", `{"name":"Application"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"Admin","scopes":["generation","keys:manage"]}`)
	for _, test := range []struct {
		query, session, secret string
		status                 int
	}{
		{"", "", "", 401}, {"", "", generation, 403}, {"", "", "service-secret", 403},
		{"", "alice", "", 200}, {"?month=2026-09", "", manager, 200},
		{"?month=2026-13", "alice", "", 400}, {"?month=1999-12", "alice", "", 400},
		{"?month=2026-09-01", "alice", "", 400},
		{"?month=2026-03&timezone=Asia/Nicosia", "alice", "", 200},
		{"?month=2026-03&timezone=Invalid/Zone", "alice", "", 400},
	} {
		w := sendTest(h, "GET", "/v1/api-keys/usage"+test.query, "", test.session, test.secret)
		if w.Code != test.status {
			t.Fatalf("month %q: want %d, got %d", test.query, test.status, w.Code)
		}
	}
}

type usageMonthRecorder struct {
	*memoryKeys
	month time.Time
}

func (s *usageMonthRecorder) Usage(_ context.Context, _ string, month time.Time) ([]apikey.MonthlyUsage, error) {
	s.month = month
	return []apikey.MonthlyUsage{}, nil
}

func TestKeyUsagePreservesRequestedCalendarTimezone(t *testing.T) {
	s, keys := newPersonalTestServer()
	recorder := &usageMonthRecorder{memoryKeys: keys}
	s.keys = recorder
	w := sendTest(s.handler(), "GET", "/v1/api-keys/usage?month=2026-03&timezone=Asia/Nicosia", "", "alice", "")
	if w.Code != 200 || recorder.month.Location().String() != "Asia/Nicosia" || recorder.month.UTC().Format(time.RFC3339) != "2026-02-28T22:00:00Z" {
		t.Fatalf("month timezone lost: %d %v", w.Code, recorder.month)
	}
}

func (s *accountService) Estimate(ctx context.Context, input core.CreateMediaInput) (core.CostEstimate, error) {
	if input.Prompt == "" {
		return core.CostEstimate{}, core.ErrPromptRequired
	}
	return core.CostEstimate{Cost: core.FreeCost(time.Now().UTC()), CanGenerate: true}, nil
}
func (s *accountService) ListProviders(ctx context.Context) []core.ProviderDescriptor {
	models, _ := mock.New(0, "").ListModels(ctx)
	return []core.ProviderDescriptor{{ID: "mock", Name: "Test provider", DefaultModel: "mock-video-v1", CredentialsConfigured: true, Models: models}}
}

func TestRemovedMonthlyBudgetContract(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := s.handler()
	if w := sendTest(h, "POST", "/v1/api-keys", `{"name":"Bot","monthly_limit_usd":1}`, "alice", ""); w.Code != 400 {
		t.Fatalf("obsolete budget accepted: %d", w.Code)
	}
	key, secret := issueTestKey(t, h, "alice", `{"name":"Bot"}`)
	if w := sendTest(h, "PUT", "/v1/api-keys/"+key.ID+"/budget", `{"monthly_limit_usd":1}`, "alice", ""); w.Code != 404 {
		t.Fatalf("obsolete route: %d", w.Code)
	}

	for _, test := range []struct {
		body, secret string
		code         int
	}{
		{`{"prompt":"test","model":"mock/mock-video-v1"}`, secret, 200},
		{`{"prompt":"test"}`, "", 401}, {`{"prompt":""}`, secret, 400},
	} {
		if w := sendTest(h, "POST", "/v1/generations/estimate", test.body, "", test.secret); w.Code != test.code {
			t.Errorf("estimate %d: %s", w.Code, w.Body.String())
		}
	}
}

func TestGenerationRequiresAnActualKeyEvenWithSession(t *testing.T) {
	server, _ := newPersonalTestServer()
	h := server.handler()
	for _, path := range []string{"/v1/providers", "/v1/generations/estimate", "/v1/generations", "/v1/videos", "/v1/generations", "/v1/generations/unknown", "/v1/generations/unknown/media-access"} {
		method := "GET"
		if path == "/v1/generations" || path == "/v1/generations/estimate" || path == "/v1/videos" {
			method = "POST"
		}
		w := sendTest(h, method, path, `{"prompt":"test","model":"mock/mock-video-v1"}`, "alice", "")
		if w.Code != 401 {
			t.Fatalf("session accepted by %s: %d", path, w.Code)
		}
	}
	if w := sendTest(h, "GET", "/v1/api-keys", "", "alice", ""); w.Code != 200 {
		t.Fatal("session cannot manage keys")
	}
	key, secret := issueTestKey(t, h, "alice", `{"name":"Generation","scopes":["generation"]}`)
	w := sendTest(h, "POST", "/v1/generations", `{"prompt":"test","model":"mock/mock-video-v1"}`, "alice", secret)
	var task core.Task
	json.Unmarshal(w.Body.Bytes(), &task)
	if w.Code != 202 || task.APIKeyID == nil || *task.APIKeyID != key.ID {
		t.Fatalf("key was not attributed: %d %s", w.Code, w.Body.String())
	}
}

// Return deliberately mixed records to ensure the endpoint filters by the calling key.
type currentKeyUsageStub struct {
	*memoryKeys
	usage []apikey.MonthlyUsage
}

func (s *currentKeyUsageStub) Usage(context.Context, string, time.Time) ([]apikey.MonthlyUsage, error) {
	return s.usage, nil
}

func TestCurrentAPIKeyBudgetIsolation(t *testing.T) {
	s, keys := newPersonalTestServer()
	h := s.handler()
	key, secret := issueTestKey(t, h, "alice", `{"name":"Generation","scopes":["generation"]}`)
	other, _ := issueTestKey(t, h, "alice", `{"name":"Other"}`)
	foreign, _ := issueTestKey(t, h, "bob", `{"name":"Foreign"}`)
	s.keys = &currentKeyUsageStub{memoryKeys: keys, usage: []apikey.MonthlyUsage{
		{KeyID: other.ID}, {KeyID: foreign.ID},
		{KeyID: key.ID, Month: time.Now().UTC().Format("2006-01"), ActualUSD: 9500000, ConsumedUSD: 9500000, UnconfirmedTasks: 1},
	}}
	h = s.handler()
	for _, session := range []string{"", "alice"} {
		if w := sendTest(h, "GET", "/v1/key", "", session, ""); w.Code != 401 {
			t.Fatalf("session-only access: %d", w.Code)
		}
	}
	w := sendTest(h, "GET", "/v1/key", "", "bob", secret)
	var result struct {
		Key   apikey.Key          `json:"key"`
		Usage apikey.MonthlyUsage `json:"usage"`
	}
	if w.Code != 200 {
		t.Fatalf("current key: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Key.ID != key.ID || result.Usage.KeyID != key.ID || result.Usage.ConsumedUSD != 9500000 || result.Usage.ActualUSD != 0 {
		t.Fatalf("wrong current key budget: %+v", result)
	}
	for _, hidden := range []string{other.ID, foreign.ID, secret} {
		if strings.Contains(w.Body.String(), hidden) {
			t.Fatal("unrelated credential leaked")
		}
	}
	if w := sendTest(h, "GET", "/v1/api-keys/usage", "", "", secret); w.Code != 403 {
		t.Fatal("generation key gained account-wide access")
	}
	if w := sendTest(h, "GET", "/v1/key", "", "", "service-secret"); w.Code != 200 || !strings.Contains(w.Body.String(), `"usage":null`) {
		t.Fatal("service key usage must be unavailable")
	}
	if err := keys.Revoke(context.Background(), "alice", key.ID); err != nil {
		t.Fatal(err)
	}
	if w := sendTest(h, "GET", "/v1/key", "", "alice", secret); w.Code != 401 {
		t.Fatal("revoked key accepted")
	}
}
