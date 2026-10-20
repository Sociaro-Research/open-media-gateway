package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/google/uuid"
)

func TestEmailInvitationContinuation(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewAuthRepository(db)
	organizations := NewSystemRepository(db)
	owner, err := repo.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Invitation owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	company, err := organizations.CreateOrganization(ctx, owner.ID, "Invitation continuation", owner.ID, uuid.NewString()+".example.test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM organization_invites WHERE organization_id=$1`, `DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, company.ID); e != nil {
				t.Error(e)
			}
		}
		db.Exec(`DELETE FROM users WHERE id=$1`, owner.ID)
	}()
	for _, state := range []string{"active", "revoked", "expired", "used", "replaced", "removed-on-resend"} {
		t.Run(state, func(t *testing.T) {
			invitation, err := organizations.CreateOrganizationInvite(ctx, owner.ID, company.ID)
			if err != nil {
				t.Fatal(err)
			}
			email := uuid.NewString() + "@example.test"
			defer db.Exec(`DELETE FROM users WHERE LOWER(email)=$1`, email)
			mailbox := &authMailbox{}
			cfg := auth.Config{SessionSecret: strings.Repeat("i", 32), PublicURL: "https://example.test", EmailSender: mailbox}
			manager, err := auth.New(ctx, repo, cfg)
			if err != nil {
				t.Fatal(err)
			}
			peer := uuid.NewString() + ":1234"
			call := func(action string, data map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
				body, _ := json.Marshal(data)
				req := httptest.NewRequest("POST", "https://example.test/auth/email/"+action, bytes.NewReader(body))
				req.SetPathValue("action", action)
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = peer
				for _, cookie := range cookies {
					req.AddCookie(cookie)
				}
				response := httptest.NewRecorder()
				manager.Email(response, req)
				return response
			}
			expect := func(w *httptest.ResponseRecorder, status int) {
				t.Helper()
				if w.Code != status {
					t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body)
				}
			}
			invitationIn := invitation.Token
			for _, bad := range []string{"short", strings.Repeat("!", 43)} {
				expect(call("register", map[string]string{"email": email, "invitation_token": bad}), 400)
			}
			registered := call("register", map[string]string{"email": email, "invitation_token": invitationIn})
			expect(registered, 202)
			if strings.Contains(registered.Body.String(), invitationIn) || strings.Contains(mailbox.links[0], invitationIn) {
				t.Fatal("invitation disclosed before verification")
			}
			emailToken := mailbox.token()
			hash := sha256.Sum256([]byte(emailToken))
			var ciphertext []byte
			if err = db.QueryRow(`SELECT invitation_ciphertext FROM email_auth_challenges WHERE token_hash=$1`, hash[:]).Scan(&ciphertext); err != nil || len(ciphertext) == 0 || bytes.Contains(ciphertext, []byte(invitationIn)) {
				t.Fatal("invitation not encrypted", err)
			}
			if state == "replaced" || state == "removed-on-resend" {
				if state == "replaced" {
					next, e := organizations.CreateOrganizationInvite(ctx, owner.ID, company.ID)
					if e != nil {
						t.Fatal(e)
					}
					invitationIn = next.Token
				} else {
					invitationIn = ""
				}
				expect(call("register", map[string]string{"email": email, "invitation_token": invitationIn}), 202)
				expect(call("verify", map[string]string{"token": emailToken}), 400)
				emailToken = mailbox.token()
			}
			switch state {
			case "revoked":
				err = organizations.RevokeOrganizationInvite(ctx, owner.ID, company.ID, invitation.ID)
			case "expired":
				_, err = db.Exec(`UPDATE organization_invites SET expires_at=NOW()-interval '1 second' WHERE id=$1`, invitation.ID)
			case "used":
				other, e := repo.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Other recipient", "")
				if e != nil {
					t.Fatal(e)
				}
				defer db.Exec(`DELETE FROM organization_members WHERE user_id=$1`, other.ID)
				// Invites retain accepted_by, so remove that reference before the synthetic user.
				defer func() {
					db.Exec(`UPDATE organization_invites SET accepted_by=NULL WHERE accepted_by=$1`, other.ID)
					db.Exec(`DELETE FROM organization_members WHERE user_id=$1`, other.ID)
					db.Exec(`DELETE FROM users WHERE id=$1`, other.ID)
				}()
				_, err = organizations.OrganizationInvitation(ctx, other.ID, invitation.Token, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a server restart and a different browser: only the emailed token survives.
			manager, err = auth.New(ctx, repo, cfg)
			if err != nil {
				t.Fatal(err)
			}
			absent := call("registration", map[string]string{})
			expect(absent, 200)
			if strings.Contains(absent.Body.String(), "invitation_token") {
				t.Fatal("unverified context leak")
			}
			verified := call("verify", map[string]string{"token": emailToken})
			expect(verified, 200)
			var verifiedBody struct {
				Invitation string `json:"invitation_token"`
			}
			json.Unmarshal(verified.Body.Bytes(), &verifiedBody)
			if verifiedBody.Invitation != invitationIn {
				t.Fatalf("verification lost invitation: %q", verifiedBody.Invitation)
			}
			grant := verified.Result().Cookies()[0]
			manager, err = auth.New(ctx, repo, cfg)
			if err != nil {
				t.Fatal(err)
			}
			resumed := call("registration", map[string]string{}, grant)
			expect(resumed, 200)
			var resumedBody struct {
				Invitation string `json:"invitation_token"`
			}
			json.Unmarshal(resumed.Body.Bytes(), &resumedBody)
			if resumedBody.Invitation != invitationIn {
				t.Fatal("resumed grant lost invitation")
			}
			// A request body cannot replace the server-bound continuation during completion.
			expect(call("complete", map[string]string{"password": "An owner secret passphrase 123!", "invitation_token": invitation.Token}, grant), 400)
			expect(call("complete", map[string]string{"password": "short"}, grant), 400)
			complete := call("complete", map[string]string{"password": "An owner secret passphrase 123!"}, grant)
			expect(complete, 200)
			var completedBody struct {
				Invitation string `json:"invitation_token"`
			}
			json.Unmarshal(complete.Body.Bytes(), &completedBody)
			if completedBody.Invitation != invitationIn {
				t.Fatal("completion lost invitation")
			}
			account, e := repo.EmailAccount(ctx, email)
			if e != nil {
				t.Fatal(e)
			}
			var memberships int
			if e = db.QueryRow(`SELECT COUNT(*) FROM organization_members WHERE user_id=$1 AND removed_at IS NULL`, account.User.ID).Scan(&memberships); e != nil || memberships != 0 {
				t.Fatal("registration accepted invitation automatically", e)
			}
			if invitationIn != "" {
				preview, e := organizations.OrganizationInvitation(ctx, account.User.ID, invitationIn, false)
				if state == "revoked" || state == "expired" || state == "used" {
					if !errors.Is(e, org.ErrInvitationUnavailable) {
						t.Fatalf("unavailable invite restored access: %v", e)
					}
				} else {
					if e != nil || preview.AlreadyMember {
						t.Fatal("pending invitation unavailable", e)
					}
					if _, e = organizations.OrganizationInvitation(ctx, account.User.ID, invitationIn, true); e != nil {
						t.Fatal(e)
					}
					if e = db.QueryRow(`SELECT COUNT(*) FROM organization_members WHERE user_id=$1 AND removed_at IS NULL`, account.User.ID).Scan(&memberships); e != nil || memberships != 1 {
						t.Fatal("explicit acceptance failed", e)
					}
					// Remove synthetic memberships before the account cleanup.
					defer db.Exec(`DELETE FROM organization_members WHERE user_id=$1`, account.User.ID)
					defer db.Exec(`UPDATE organization_invites SET accepted_by=NULL WHERE accepted_by=$1`, account.User.ID)
				}
			}
			// Password recovery carries the same context through its emailed token.
			recovery := call("forgot", map[string]string{"email": email, "invitation_token": invitation.Token})
			expect(recovery, 202)
			recovered := call("reset", map[string]string{"token": mailbox.token(), "password": "Another owner passphrase 456!"})
			expect(recovered, 200)
			if !strings.Contains(recovered.Body.String(), invitation.Token) {
				t.Fatal("recovery lost invitation")
			}
			// Expired grants do not disclose or restore invitation context.
			expect(call("complete", map[string]string{"password": "An owner secret passphrase 123!"}, grant), 400)
			db.Exec(`DELETE FROM email_auth_challenges WHERE email=$1`, email)
		})
	}
}
