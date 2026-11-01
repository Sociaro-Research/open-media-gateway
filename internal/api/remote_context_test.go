package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func sendRemoteContext(h http.Handler, method, path, body, actor, binding, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
	request.Header.Set("Content-Type", "application/json")
	if binding != "" {
		request.Header.Set("X-Workspace-Context", binding)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	result := httptest.NewRecorder()
	h.ServeHTTP(result, request)
	return result
}
func TestRemoteReviewedContextRejectsReplacementBeforeEgress(t *testing.T) {
	var aCalls, bCalls atomic.Int32
	upstream := func(counter *atomic.Int32) *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[]}`)
		}))
	}
	a, b := upstream(&aCalls), upstream(&bCalls)
	defer a.Close()
	defer b.Close()
	cipher, _ := providerconfig.NewCipher(strings.Repeat("s", 32))
	encrypted, _ := cipher.Encrypt([]byte("synthetic-customer-token"))
	original := remote.Record{Owner: "alice", Version: 1, Active: true, Origin: a.URL, OrganizationID: uuid.NewString(), UserID: uuid.NewString(), KeyID: uuid.NewString(), Ciphertext: encrypted}
	repo := &remoteFixture{records: map[string]remote.Record{"alice": original}}
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.remoteWorkspaceManager = remote.New(repo, cipher, a.Client())
	h := s.handler()
	if result := sendTest(h, "GET", "/v1/system/remote-workspace", "", "alice", ""); result.Code != 200 || !strings.Contains(result.Body.String(), original.SnapshotID()) {
		t.Fatal("reviewed context not exposed")
	}
	for _, field := range []string{"version", "company", "host", "account", "credential"} {
		t.Run(field, func(t *testing.T) {
			replacement := original
			switch field {
			case "version":
				replacement.Version++
			case "company":
				replacement.OrganizationID = uuid.NewString()
			case "host":
				replacement.Origin = b.URL
			case "account":
				replacement.UserID = uuid.NewString()
			case "credential":
				replacement.KeyID = uuid.NewString()
			}
			repo.records["alice"] = replacement
			for _, path := range []string{"/v1/models", "/v1/key", "/v1/generations/estimate", "/v1/generations"} {
				method := "GET"
				if strings.HasSuffix(path, "estimate") || path == "/v1/generations" {
					method = "POST"
				}
				result := sendRemoteContext(h, method, remote.Prefix+path, `{"prompt":"private input reviewed for A"}`, "alice", original.SnapshotID(), "frozen-A-operation")
				if result.Code != 409 {
					t.Fatalf("stale %s %s accepted: %d", method, path, result.Code)
				}
			}
		})
	}
	if aCalls.Load() != 0 || bCalls.Load() != 0 {
		t.Fatal("stale prompt or read reached a remote account")
	}
	repo.records["alice"] = original
	if result := sendRemoteContext(h, "GET", remote.Prefix+"/v1/models", "", "alice", "", ""); result.Code != 428 {
		t.Fatal("missing context accepted", result.Code)
	}
	if result := sendRemoteContext(h, "GET", remote.Prefix+"/v1/models?workspace_context="+original.SnapshotID(), "", "alice", original.SnapshotID(), ""); result.Code != 428 {
		t.Fatal("conflicting context channels accepted", result.Code)
	}
	if result := sendRemoteContext(h, "GET", remote.Prefix+"/v1/models?workspace_context="+original.SnapshotID(), "", "alice", "", ""); result.Code != 200 {
		t.Fatal("bound link rejected", result.Code)
	}
	if aCalls.Load() != 1 || bCalls.Load() != 0 {
		t.Fatal("query binding not stripped or wrong destination")
	}
}
func TestRemoteRenewalRetainsOneRealTaskAdmission(t *testing.T) {
	db := workspaceDatabase(t)
	ctx := context.Background()
	person, e := postgres.NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Task owner", "")
	if e != nil {
		t.Fatal(e)
	}
	tasks := postgres.NewTaskRepository(db)
	taskCtx := identity.WithUser(ctx, person)
	var remoteKeys []string
	var admitted []string
	server := func() *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remoteKeys = append(remoteKeys, r.Header.Get("Idempotency-Key"))
			now := time.Now().UTC()
			task := core.Task{ID: uuid.NewString(), Status: core.TaskQueued, Provider: "mock", Model: "mock-image-v1", MediaType: "image", Prompt: "Frozen same operation", CreatedAt: now, UpdatedAt: now}
			stored, _, err := tasks.CreateOrGet(taskCtx, task, r.Header.Get("Idempotency-Key"), "frozen-request-hash")
			if err != nil {
				t.Error(err)
				http.Error(w, "fixture", 500)
				return
			}
			admitted = append(admitted, stored.ID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": stored.ID})
		}))
	}
	a, b := server(), server()
	defer a.Close()
	defer b.Close()
	cipher, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
	encrypted, _ := cipher.Encrypt([]byte("synthetic-generation-key"))
	original := remote.Record{Owner: "alice", Version: 1, Active: true, Origin: a.URL, OrganizationID: uuid.NewString(), UserID: uuid.NewString(), KeyID: uuid.NewString(), Ciphertext: encrypted}
	repo := &remoteFixture{records: map[string]remote.Record{"alice": original}}
	manager := remote.New(repo, cipher, a.Client())
	submit := func(record remote.Record) {
		t.Helper()
		response, _, _, err := manager.Proxy(ctx, "alice", "POST", "/v1/generations", "", []byte(`{"prompt":"Frozen same operation"}`), "same-logical-operation", record.SnapshotID())
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	submit(original)
	renewed := original
	renewed.Version++
	renewed.KeyID = uuid.NewString()
	repo.records["alice"] = renewed
	if _, _, _, err := manager.Proxy(ctx, "alice", "POST", "/v1/generations", "", nil, "same-logical-operation", original.SnapshotID()); !errors.Is(err, remote.ErrConflict) {
		t.Fatal("old context dispatched through renewed key", err)
	}
	submit(renewed)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE owner_id=$1`, person.ID).Scan(&n); err != nil || n != 1 || admitted[0] != admitted[1] || remoteKeys[0] != remoteKeys[1] {
		t.Fatalf("renewal duplicated admission: rows=%d error=%v", n, err)
	}
	if renewed.OperationScopeID() != original.OperationScopeID() {
		t.Fatal("renewal changed logical account scope")
	}
	anotherCompany := renewed
	anotherCompany.Version++
	anotherCompany.OrganizationID = uuid.NewString()
	repo.records["alice"] = anotherCompany
	submit(anotherCompany)
	anotherHost := anotherCompany
	anotherHost.Version++
	anotherHost.Origin = b.URL
	repo.records["alice"] = anotherHost
	submit(anotherHost)
	if remoteKeys[1] == remoteKeys[2] || remoteKeys[2] == remoteKeys[3] {
		t.Fatal("operation namespace conflates companies/hosts")
	}
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE owner_id=$1`, person.ID).Scan(&n); err != nil || n != 3 {
		t.Fatal("different companies/hosts shared a task", n, err)
	}
}
