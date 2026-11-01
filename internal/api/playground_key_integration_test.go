package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/playgroundkey"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
)

type playgroundBrowserAuth struct {
	browserAuthStub
	users map[string]identity.User
}

func (a playgroundBrowserAuth) Authenticate(r *http.Request) (identity.User, error) {
	c, e := r.Cookie("test_session")
	if e != nil {
		return identity.User{}, e
	}
	u, ok := a.users[c.Value]
	if !ok {
		return identity.User{}, apikey.ErrNotFound
	}
	return u, nil
}

type playgroundResponse struct {
	Key    apikey.Key `json:"key"`
	Secret string     `json:"secret"`
}

func TestManagedPlaygroundKeyAccountVault(t *testing.T) {
	db := workspaceDatabase(t)
	ctx := context.Background()
	people := postgres.NewAuthRepository(db)
	alice, e := people.UpsertGoogleUser(ctx, uuid.NewString(), "managed-alice@example.test", "Alice", "")
	if e != nil {
		t.Fatal(e)
	}
	bob, e := people.UpsertGoogleUser(ctx, uuid.NewString(), "managed-bob@example.test", "Bob", "")
	if e != nil {
		t.Fatal(e)
	}
	repo := postgres.NewAPIKeyRepository(db)
	manager, e := playgroundkey.New(repo, repo, strings.Repeat("synthetic-master-", 3))
	if e != nil {
		t.Fatal(e)
	}
	s, _ := newPersonalTestServer()
	s.keys = repo
	s.playgroundKeys = manager
	s.auth = playgroundBrowserAuth{users: map[string]identity.User{"alice": alice, "bob": bob}}
	h := s.handler()
	call := func(action, session, org, request, bearer string) *httptest.ResponseRecorder {
		input := map[string]string{"organization_id": org}
		if action == "create" {
			input["request_id"] = request
		}
		body, _ := json.Marshal(input)
		return sendTest(h, "POST", "/v1/playground-key/"+action, string(body), session, bearer)
	}
	decode := func(w *httptest.ResponseRecorder) playgroundResponse {
		t.Helper()
		if w.Code != 200 {
			t.Fatalf("managed endpoint status %d", w.Code)
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("secret response cacheable")
		}
		var out playgroundResponse
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Secret == "" || out.Key.Purpose != playgroundkey.Purpose {
			t.Fatal("managed response missing metadata")
		}
		return out
	}
	if w := call("restore", "alice", "", "", ""); w.Code != 404 {
		t.Fatal("visit created a key", w.Code)
	}
	aliasRequest := uuid.NewString()
	request := uuid.NewString()
	first := decode(call("create", "alice", "", request, ""))
	if first.Key.Name != playgroundkey.Name || len(first.Key.Scopes) != 1 || first.Key.Scopes[0] != apikey.Generation {
		t.Fatal("wrong key purpose or permissions")
	}
	if first.Key.Prefix == "" || first.Key.CreatedAt.IsZero() || first.Key.CreatedBy != alice.ID {
		t.Fatal("incomplete actual key metadata")
	}
	if _, e = db.Exec(`UPDATE user_api_keys SET name='Renamed Playground fixture',expires_at=clock_timestamp()+interval '1 hour' WHERE id=$1`, first.Key.ID); e != nil {
		t.Fatal(e)
	}
	second := decode(call("restore", "alice", "", "", ""))
	if second.Key.Name != "Renamed Playground fixture" || second.Key.ExpiresAt == nil || second.Key.Purpose != playgroundkey.Purpose {
		t.Fatal("restored metadata did not preserve actual name, expiry and immutable purpose")
	}
	if second.Secret != first.Secret || second.Key.ID != first.Key.ID {
		t.Fatal("second browser did not restore same key")
	}
	if w := call("restore", "bob", "", "", ""); w.Code != 404 {
		t.Fatal("foreign account restored a key", w.Code)
	}
	for _, bearer := range []string{first.Secret, "invalid", "service-secret"} {
		if w := call("restore", "alice", "", "", bearer); w.Code == 200 {
			t.Fatal("bearer permitted vault access")
		}
	}
	if w := call("restore", "", "", "", ""); w.Code != 401 {
		t.Fatal("anonymous restore", w.Code)
	}
	if w := sendTest(h, "GET", "/v1/providers", "", "alice", ""); w.Code != 401 {
		t.Fatal("session-only generation authorized")
	}
	if w := sendTest(h, "GET", "/v1/providers", "", "", first.Secret); w.Code != 200 {
		t.Fatal("ordinary bearer failed", w.Code)
	}
	generated := sendTest(h, "POST", "/v1/generations", `{"prompt":"Synthetic managed key check","model":"mock/mock-video-v1"}`, "", first.Secret)
	var task core.Task
	json.Unmarshal(generated.Body.Bytes(), &task)
	if generated.Code != 202 || task.APIKeyID == nil || *task.APIKeyID != first.Key.ID {
		t.Fatal("managed key did not use normal attributed generation", generated.Code)
	}
	if w := sendTest(h, "GET", "/v1/api-keys", "", "", first.Secret); w.Code != 403 {
		t.Fatal("managed generation key exceeded scopes", w.Code)
	}
	for _, action := range []string{"create", "restore"} {
		r := httptest.NewRequest("POST", "/v1/playground-key/"+action, strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
		r.Header.Set("Origin", "https://attacker.invalid")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-origin vault operation accepted", action, w.Code)
		}
	}
	var cipher, hash []byte
	if e = db.QueryRow(`SELECT v.ciphertext,k.token_hash FROM playground_key_slots v JOIN user_api_keys k ON k.id=v.key_id WHERE v.user_id=$1`, alice.ID).Scan(&cipher, &hash); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(cipher), first.Secret) || string(hash) != string(apikey.Hash(first.Secret)) {
		t.Fatal("plaintext persisted or hash authentication changed")
	}
	otherDomain, _ := providerconfig.NewCipher(strings.Repeat("synthetic-master-", 3))
	if _, e = otherDomain.Decrypt(cipher); e == nil {
		t.Fatal("vault was not encryption-domain separated")
	}
	if _, e = playgroundkey.New(repo, repo, ""); e == nil {
		t.Fatal("missing operator encryption master accepted")
	}
	// Normal key issuance cannot request managed purpose and creates no vault row.
	if w := sendTest(h, "POST", "/v1/api-keys", `{"name":"Manual","purpose":"playground_templates"}`, "alice", ""); w.Code != 400 {
		t.Fatal("caller chose managed purpose")
	}
	if w := sendTest(h, "POST", "/v1/api-keys", `{"name":"Manual"}`, "alice", ""); w.Code != 201 {
		t.Fatal("ordinary create failed", w.Code)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM playground_key_slots`).Scan(&count); e != nil || count != 1 {
		t.Fatal("ordinary key vaulted", e)
	}
	// Concurrent tabs with different idempotency IDs converge on one active key.
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := call("create", "alice", "", uuid.NewString(), "")
			codes <- w.Code
			var got playgroundResponse
			json.Unmarshal(w.Body.Bytes(), &got)
			ids <- got.Key.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatal("concurrent create", code)
		}
	}
	for id := range ids {
		if id != first.Key.ID {
			t.Fatal("duplicate managed credential")
		}
	}
	decode(call("create", "alice", "", aliasRequest, ""))
	// Encryption domain, record binding, nonce authentication and master mismatch.
	wrong, _ := playgroundkey.New(repo, repo, strings.Repeat("different-master-", 3))
	if _, _, e = wrong.Restore(ctx, alice.ID, ""); e == nil {
		t.Fatal("wrong master decrypted vault")
	}
	if _, e = db.Exec(`UPDATE playground_key_slots SET ciphertext=set_byte(ciphertext,20,get_byte(ciphertext,20)#1) WHERE user_id=$1`, alice.ID); e != nil {
		t.Fatal(e)
	}
	if w := call("restore", "alice", "", "", ""); w.Code != 503 {
		t.Fatal("tamper accepted", w.Code)
	}
	db.Exec(`UPDATE playground_key_slots SET ciphertext=$2 WHERE user_id=$1`, alice.ID, cipher)
	// Copying an authenticated ciphertext to another owner's slot cannot decrypt it.
	b := decode(call("create", "bob", "", uuid.NewString(), ""))
	_ = b
	db.Exec(`UPDATE playground_key_slots SET ciphertext=$2 WHERE user_id=$1`, bob.ID, cipher)
	if w := call("restore", "bob", "", "", ""); w.Code != 503 {
		t.Fatal("cross-owner ciphertext transplant accepted", w.Code)
	}
	if _, e = db.Exec(`UPDATE user_api_keys SET purpose='' WHERE id=$1`, first.Key.ID); e == nil {
		t.Fatal("managed provenance editable")
	}
	// Revocation and expired replay cannot silently mint; a new explicit request can.
	if e = repo.Revoke(ctx, alice.ID, first.Key.ID); e != nil {
		t.Fatal(e)
	}
	if w := call("restore", "alice", "", "", ""); w.Code != 410 {
		t.Fatal("revoked restore", w.Code)
	}
	if w := call("create", "alice", "", request, ""); w.Code != 410 {
		t.Fatal("revoked replay recreated", w.Code)
	}
	if w := call("create", "alice", uuid.NewString(), request, ""); w.Code != 409 && w.Code != 403 {
		t.Fatal("request ID crossed workspaces", w.Code)
	}
	replacement := decode(call("create", "alice", "", uuid.NewString(), ""))
	if w := call("create", "alice", "", aliasRequest, ""); w.Code != 409 {
		t.Fatal("active-slot request replay switched replacement", w.Code)
	}
	if replacement.Key.ID == first.Key.ID {
		t.Fatal("explicit replacement reused revoked key")
	}
	if w := call("create", "alice", "", request, ""); w.Code != 409 {
		t.Fatal("stale create replay switched replacement", w.Code)
	}
	db.Exec(`UPDATE user_api_keys SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, replacement.Key.ID)
	if w := call("restore", "alice", "", "", ""); w.Code != 410 {
		t.Fatal("expired restore", w.Code)
	}
	// Organization vault stays with its consuming member, never the manager viewing keys.
	org := uuid.NewString()
	if _, e = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Managed workspace')`, org); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager'),($1,$3,'admin')`, org, alice.ID, bob.ID); e != nil {
		t.Fatal(e)
	}
	orgKey := decode(call("create", "alice", strings.ToUpper(org), uuid.NewString(), ""))
	for _, alias := range []string{org, "urn:uuid:" + org, strings.ReplaceAll(org, "-", "")} {
		same := decode(call("create", "alice", alias, uuid.NewString(), ""))
		if same.Key.ID != orgKey.Key.ID {
			t.Fatal("UUID spelling created duplicate workspace key")
		}
	}
	if w := call("create", "alice", org, aliasRequest, ""); w.Code != 409 {
		t.Fatal("same request ID crossed real workspaces", w.Code)
	}
	if orgKey.Key.ID == replacement.Key.ID {
		t.Fatal("cross-workspace key reused")
	}
	listed, e := repo.OrganizationKeys(ctx, bob.ID, org)
	if e != nil || len(listed) != 1 || listed[0].Purpose != playgroundkey.Purpose {
		t.Fatal("manager purpose metadata", e)
	}
	if w := call("restore", "bob", org, "", ""); w.Code != 404 {
		t.Fatal("manager restored member secret", w.Code)
	}
	db.Exec(`UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, org, alice.ID)
	if w := call("restore", "alice", org, "", ""); w.Code != 200 {
		t.Fatal("valid member-owned key stopped restoring", w.Code)
	}
	if e = repo.RevokeForActor(ctx, bob.ID, orgKey.Key.ID); e != nil {
		t.Fatal(e)
	}
	if w := call("create", "alice", org, uuid.NewString(), ""); w.Code != 403 {
		t.Fatal("member minted replacement", w.Code)
	}
	db.Exec(`UPDATE organization_members SET removed_at=clock_timestamp() WHERE organization_id=$1 AND user_id=$2`, org, alice.ID)
	if w := call("restore", "alice", org, "", ""); w.Code == 200 {
		t.Fatal("removed member restored secret")
	}
}
