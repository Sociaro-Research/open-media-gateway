package remoteworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

const company = "11111111-1111-4111-8111-111111111111"
const user = "22222222-2222-4222-8222-222222222222"
const key = "33333333-3333-4333-8333-333333333333"
const job = "44444444-4444-4444-8444-444444444444"
const secret = "fixture-customer-secret-for-remote-pilot"

type memoryRepo struct {
	mu      sync.Mutex
	records map[string]Record
	saves   int
}

func (r *memoryRepo) RemoteWorkspace(_ context.Context, owner string) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.records[owner]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func (r *memoryRepo) SaveRemoteWorkspace(_ context.Context, v Record, version int64) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.records[v.Owner]
	if current.Version != version {
		return Record{}, ErrConflict
	}
	v.Version = version + 1
	r.records[v.Owner] = v
	r.saves++
	return v, nil
}
func (r *memoryRepo) DisconnectRemoteWorkspace(_ context.Context, owner string, version int64) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.records[owner]
	if v.Version != version {
		return v, ErrConflict
	}
	v = Record{Owner: owner, Version: version + 1}
	r.records[owner] = v
	return v, nil
}
func TestConnectionVerificationIsolationAndRetry(t *testing.T) {
	repo := &memoryRepo{records: map[string]Record{}}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("f", 32))
	scope := "generation"
	var submissions = map[string]bool{}
	var ids []string
	var paths []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("wrong remote credential")
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie leaked")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/key":
			fmt.Fprintf(w, `{"key":{"id":%q,"user_id":%q,"organization_id":%q,"resource_mode":"shared","scopes":[%q]},"organization":{"id":%q,"name":"Fixture company"}}`, key, user, company, scope, company)
		case "/v1/generations":
			id := r.Header.Get("Idempotency-Key")
			ids = append(ids, id)
			submissions[id] = true
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"id":%q,"status_url":"/v1/generations/%s"}`, job, job)
		default:
			t.Errorf("unexpected remote operation %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	m := New(repo, cipher, server.Client())
	ctx := context.Background()
	scope = "system:manage"
	if _, e := m.Connect(ctx, "alice", server.URL, secret, company, 0); !errors.Is(e, ErrCredential) {
		t.Fatal(e)
	}
	if repo.saves != 0 {
		t.Fatal("failed check saved a credential")
	}
	scope = "generation"
	saved, e := m.Connect(ctx, "alice", server.URL, secret, company, 0)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(saved.Ciphertext), secret) {
		t.Fatal("plaintext persisted")
	}
	view, _ := json.Marshal(saved)
	if strings.Contains(string(view), secret) || strings.Contains(string(view), "Ciphertext") {
		t.Fatal("secret exposed")
	}
	before := len(paths)
	if _, _, _, e = m.Proxy(ctx, "bob", "GET", "/v1/models", "", nil, "", saved.SnapshotID()); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if len(paths) != before {
		t.Fatal("foreign owner caused a remote request")
	}
	for i := 0; i < 2; i++ {
		res, _, _, e := m.Proxy(ctx, "alice", "POST", "/v1/generations", "", []byte(`{"model":"mock/image","prompt":"fixture"}`), "same-operation", saved.SnapshotID())
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
	}
	if len(submissions) != 1 || len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("duplicate paid operation: %v", ids)
	}
	before = len(paths)
	for _, path := range []string{"/v1/system/settings", "/v1/organizations/" + company, "/v1/generations/../system/settings", "/v1/generations/%2f..", "/v1/generations/" + job + "/unknown"} {
		if _, _, _, e = m.Proxy(ctx, "alice", "GET", path, "", nil, "", saved.SnapshotID()); !errors.Is(e, ErrRoute) {
			t.Fatalf("route accepted: %s %v", path, e)
		}
	}
	if len(paths) != before {
		t.Fatal("forbidden operation made remote request")
	}
	if _, _, _, e = m.Proxy(ctx, "alice", "GET", "/v1/generations", "organization_id="+company, nil, "", saved.SnapshotID()); !errors.Is(e, ErrRoute) {
		t.Fatal("tenant override accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = m.Connect(canceled, "alice", server.URL, secret, company, saved.Version); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if repo.saves != 1 {
		t.Fatal("interrupted connect replaced existing state")
	}
}
func TestNoCredentialRedirectAndSafeProjection(t *testing.T) {
	var received bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		if r.Header.Get("Authorization") != "" {
			t.Error("customer token leaked")
		}
		fmt.Fprint(w, "image")
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	repo := &memoryRepo{records: map[string]Record{}}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	m := New(repo, cipher, source.Client())
	if _, e := m.Connect(context.Background(), "alice", source.URL, secret, company, 0); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if received {
		t.Fatal("authenticated redirect followed")
	}
	// Explicit download uses a separate request without the bearer credential.
	res, e := m.Download(context.Background(), target.URL, "GET")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if !received {
		t.Fatal("file not fetched")
	}
	value := map[string]any{"status_url": "/v1/generations/" + job, "content_url": "https://attacker.invalid/v1/generations/" + job, "error": map[string]any{"message": secret}}
	Rewrite(value, source.URL, secret, "reviewed-context")
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), secret) || value["content_url"] != "" || !strings.HasPrefix(value["status_url"].(string), Prefix) {
		t.Fatalf("unsafe projection: %s", raw)
	}
	for _, raw := range []string{"http://service.invalid", "https://service.invalid/path", "https://user:password@service.invalid", "https://service.invalid?token=x", "https://service.invalid#x"} {
		if _, e = Origin(raw); e == nil {
			t.Fatal("unsafe origin accepted", raw)
		}
	}
	secure := New(repo, cipher, httpclient.NewPublic(0))
	if _, e = secure.Connect(context.Background(), "alice", source.URL, secret, company, 0); !errors.Is(e, ErrUnavailable) {
		t.Fatal("private-network destination accepted")
	}
}

func TestProductionTransportRejectsPrivateTargetsAndMethods(t *testing.T) {
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	manager := New(&memoryRepo{records: map[string]Record{}}, cipher, httpclient.NewPublic(time.Second))
	for _, origin := range []string{"https://127.0.0.1", "https://10.0.0.1", "https://169.254.169.254", "https://192.168.1.2", "https://[::1]", "https://[::ffff:127.0.0.1]", "https://[fd00::1]", "https://localhost"} {
		if _, err := manager.Connect(context.Background(), "alice", origin, secret, company, 0); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("private target accepted: %s %v", origin, err)
		}
		if _, err := manager.Download(context.Background(), origin+"/content", "GET"); err == nil {
			t.Fatalf("private download accepted: %s", origin)
		}
	}
	for _, method := range []string{"PUT", "PATCH", "DELETE", "CONNECT", "TRACE", "OPTIONS"} {
		for _, path := range []string{"/v1/key", "/v1/models", "/v1/generations", "/v1/generations/" + job + "/content"} {
			if Allowed(method, path) {
				t.Fatalf("unsupported method accepted: %s %s", method, path)
			}
		}
	}
}
