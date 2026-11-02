package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/google/uuid"
)

func TestSavedAuthorizationCleanupBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, change string
		completed    bool
		failure      bool
	}{
		{"verified expired save", "", true, false},
		{"still pending", "UPDATE remote_workspace_authorizations SET base_version=1", false, false},
		{"later revision", "UPDATE remote_workspaces SET version=2", false, true},
		{"disconnected", "UPDATE remote_workspaces SET active=FALSE", false, true},
		{"legacy key replacement", "UPDATE remote_workspaces SET provisioning='legacy'", false, true},
		{"foreign destination", "UPDATE remote_workspaces SET origin='https://foreign.fixture.test'", false, true},
		{"invalid stored proof", "UPDATE remote_workspaces SET token_hash='\\x01'::bytea", false, true},
		{"unverified save", "UPDATE remote_workspaces SET verified_at=NULL", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := workspaceDatabase(t)
			ctx := context.Background()
			people := postgres.NewAuthRepository(db)
			user, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Owner", "")
			if e != nil {
				t.Fatal(e)
			}
			repo := postgres.NewSystemRepository(db)
			if e = repo.SetAdmin(ctx, "", user.ID, true, true); e != nil {
				t.Fatal(e)
			}
			cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
			proof, _ := cipher.Encrypt([]byte("synthetic retained device proof"))
			id := uuid.NewString()
			attempt := remote.Attempt{Owner: user.ID, ID: id, Origin: "https://console.sociaro.ai", Name: "Installation", ClientOrigin: "http://localhost", Version: 0, Cipher: proof, Expires: time.Now().Add(-time.Hour)}
			if _, e = repo.SaveAuthorizationAttempt(ctx, attempt); e != nil {
				t.Fatal(e)
			}
			now := time.Now()
			saved, e := repo.SaveRemoteWorkspace(ctx, remote.Record{Owner: user.ID, Active: true, Provisioning: "dedicated", Origin: "https://CONSOLE.SOCIARO.AI.:443/", OrganizationID: uuid.NewString(), UserID: uuid.NewString(), KeyID: uuid.NewString(), TokenHash: make([]byte, 32), Ciphertext: proof, VerifiedAt: &now}, 0)
			if e != nil {
				t.Fatal(e)
			}
			if test.change != "" {
				if _, e = db.Exec(test.change); e != nil {
					t.Fatal(e)
				}
			}
			// A wrong request id never consumes proof even when the connection is valid.
			if _, done, e := repo.FinishSavedAuthorization(ctx, user.ID, uuid.NewString()); e == nil || done {
				t.Fatal("wrong attempt consumed completion")
			}
			result, done, e := repo.FinishSavedAuthorization(ctx, user.ID, id)
			if (e != nil) != test.failure || done != test.completed {
				t.Fatalf("done=%v err=%v", done, e)
			}
			if done {
				if result.Version != saved.Version || result.KeyID != saved.KeyID || result.Origin != saved.Origin || string(result.Ciphertext) != string(proof) {
					t.Fatal("cleanup changed credential/namespace")
				}
				if _, e = repo.AuthorizationAttempt(ctx, user.ID); e != remote.ErrNotFound {
					t.Fatal("proof was not cleared after confirmed save", e)
				}
			} else {
				kept, e := repo.AuthorizationAttempt(ctx, user.ID)
				if e != nil || kept.ID != id || string(kept.Cipher) != string(proof) {
					t.Fatal("unconfirmed attempt proof lost", e)
				}
			}
		})
	}
}
