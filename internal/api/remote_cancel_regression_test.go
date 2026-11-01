package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

type cancelConfirmationFixture struct {
	remoteFixture
	attempt     remote.Attempt
	cleared     bool
	beforeClear func()
}

func (f *cancelConfirmationFixture) AuthorizationAttempt(context.Context, string) (remote.Attempt, error) {
	return f.attempt, nil
}
func (f *cancelConfirmationFixture) SaveAuthorizationAttempt(_ context.Context, v remote.Attempt) (remote.Attempt, error) {
	f.attempt = v
	return v, nil
}
func (f *cancelConfirmationFixture) ClearAuthorizationAttempt(context.Context, string, string) error {
	if f.beforeClear != nil {
		f.beforeClear()
	}
	f.cleared = true
	return nil
}
func TestCancellationKeepsProofUnlessHostConfirms(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		confirmed bool
	}{{"ambiguous expired host", 410, `{"error":{"code":"expired"}}`, false}, {"missing confirmation", 200, `{}`, false}, {"false confirmation", 200, `{"cancelled":false}`, false}, {"invalid response", 200, `not JSON`, false}, {"confirmed", 200, `{"cancelled":true}`, true}} {
		t.Run(test.name, func(t *testing.T) {
			host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.status); fmt.Fprint(w, test.body) }))
			defer host.Close()
			cipher, _ := providerconfig.NewCipher(strings.Repeat("t", 32))
			encrypted, _ := cipher.Encrypt([]byte("retained-synthetic-device-proof"))
			repo := &cancelConfirmationFixture{attempt: remote.Attempt{ID: "attempt", Owner: "alice", Origin: host.URL, Cipher: encrypted}}
			manager := remote.New(repo, cipher, host.Client())
			err := manager.CancelAuthorization(context.Background(), "alice", "attempt")
			if (err == nil) != test.confirmed || repo.cleared != test.confirmed {
				t.Fatalf("false cancellation success/discard: err=%v cleared=%v", err, repo.cleared)
			}
		})
	}
}
func TestExpiredCancellationRevokesIssuedKeyBeforeClearingProof(t *testing.T) {
	db := workspaceDatabase(t)
	ctx := context.Background()
	owner, e := postgres.NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`); e != nil {
		t.Fatal(e)
	}
	repo := postgres.NewSystemRepository(db)
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	authority := wa.New(repo, cipher)
	device, _ := wa.DeviceCode()
	request, e := authority.Start(ctx, device, wa.Challenge{Label: "Synthetic installation", Origin: "http://localhost:8088"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.Approve(ctx, owner.ID, request.Code, wa.Approval{Name: "Expired request company", Website: "expired.fixture.test", Consent: true}); e != nil {
		t.Fatal(e)
	}
	issued, token, e := authority.Poll(ctx, device)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE workspace_authorizations SET expires_at=NOW()-INTERVAL '1 second' WHERE device_hash=$1`, wa.Digest(device)); e != nil {
		t.Fatal(e)
	}
	if _, _, e = authority.Poll(ctx, device); !errors.Is(e, wa.ErrUnavailable) {
		t.Fatal("expiry allowed new redemption", e)
	}
	if _, e = repo.Approve(ctx, owner.ID, request.Code, wa.Approval{Company: issued.Company, Consent: true}); !errors.Is(e, wa.ErrUnavailable) {
		t.Fatal("expiry allowed consent", e)
	}
	// Start cleanup may erase expired delivery ciphertext, but must retain the
	// proof->issued-key relation needed by later cancellation.
	another, _ := wa.DeviceCode()
	if _, e = authority.Start(ctx, another, wa.Challenge{Label: "Other request", Origin: "http://localhost:8088"}); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE system_settings SET customer_workspaces=FALSE`); e != nil {
		t.Fatal(e)
	}
	keys := postgres.NewAPIKeyRepository(db)
	if _, _, e = keys.Authenticate(ctx, apikey.Hash(token)); e != nil {
		t.Fatal("fixture key was not active before cancellation", e)
	}
	hostServer := &Server{workspaceAuthorization: authority}
	host := httptest.NewTLSServer(http.HandlerFunc(hostServer.cancelWorkspaceAuthorization))
	defer host.Close()
	encrypted, _ := cipher.Encrypt([]byte(device))
	local := &cancelConfirmationFixture{attempt: remote.Attempt{ID: "local-attempt", Owner: "alice", Origin: host.URL, Cipher: encrypted, Expires: time.Now().Add(-time.Minute)}}
	local.beforeClear = func() {
		t.Helper()
		if _, _, err := keys.Authenticate(ctx, apikey.Hash(token)); !errors.Is(err, apikey.ErrNotFound) {
			t.Fatal("local proof cleared while grant still authenticates", err)
		}
	}
	client := remote.New(local, cipher, host.Client())
	if e = client.CancelAuthorization(ctx, "alice", "local-attempt"); e != nil || !local.cleared {
		t.Fatal("expired cancellation not confirmed", e)
	}
	if e = client.CancelAuthorization(ctx, "alice", "local-attempt"); e != nil {
		t.Fatal("repeated cancellation failed", e)
	}
	var state string
	var ciphertext []byte
	if e = db.QueryRow(`SELECT status,token_ciphertext FROM workspace_authorizations WHERE device_hash=$1`, wa.Digest(device)).Scan(&state, &ciphertext); e != nil || state != "cancelled" || len(ciphertext) != 0 {
		t.Fatal("issued proof not tombstoned", state, e)
	}
}
func TestExpiredLocalAttemptCannotLoseRevocationProofOnNewStart(t *testing.T) {
	db := workspaceDatabase(t)
	ctx := context.Background()
	owner, e := postgres.NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Local admin", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, owner.ID); e != nil {
		t.Fatal(e)
	}
	calls := 0
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) }))
	defer host.Close()
	cipher, _ := providerconfig.NewCipher(strings.Repeat("p", 32))
	proof, _ := wa.DeviceCode()
	encrypted, _ := cipher.Encrypt([]byte(proof))
	repo := postgres.NewSystemRepository(db)
	original := remote.Attempt{Owner: owner.ID, ID: uuid.NewString(), Origin: host.URL, Name: "Original", ClientOrigin: "http://localhost:8088", Cipher: encrypted, Version: 0, Expires: time.Now().Add(-time.Minute)}
	if _, e = repo.SaveAuthorizationAttempt(ctx, original); e != nil {
		t.Fatal(e)
	}
	candidate := original
	candidate.ID = uuid.NewString()
	candidate.Cipher = []byte("must-not-replace")
	candidate.Expires = time.Now().Add(time.Minute)
	recovered, e := repo.SaveAuthorizationAttempt(ctx, candidate)
	if e != nil || recovered.ID != original.ID || string(recovered.Cipher) != string(original.Cipher) {
		t.Fatal("expiry discarded old proof", e)
	}
	candidate.Name = "Different request"
	if _, e = repo.SaveAuthorizationAttempt(ctx, candidate); !errors.Is(e, remote.ErrConflict) {
		t.Fatal("new metadata discarded old proof", e)
	}
	manager := remote.New(repo, cipher, host.Client())
	if _, e = manager.BeginAuthorization(ctx, owner.ID, host.URL, original.Name, original.ClientOrigin, 0); !errors.Is(e, wa.ErrUnavailable) || calls != 0 {
		t.Fatal("expired request restarted or proof lost", e, calls)
	}
	kept, e := repo.AuthorizationAttempt(ctx, owner.ID)
	if e != nil || kept.ID != original.ID {
		t.Fatal("pending proof missing", e)
	}
}
