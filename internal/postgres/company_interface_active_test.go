package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/pressly/goose/v3"
)

func TestCompanyInterfaceLocalOperationsMigrationPreservesPopulatedCustody(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	user, _ := ciUser(t, db)
	in := ciStart(t)
	epoch, e := r.BeginLocal(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	tokens := ciIssue(t, r, user, in)
	if e = r.ActivateLocal(context.Background(), in, epoch, tokens); e != nil {
		t.Fatal(e)
	}
	active, e := r.LocalActive(context.Background(), in.LocalSession)
	if e != nil {
		t.Fatal(e)
	}
	first, e := r.BeginLocalRotation(context.Background(), active)
	if e != nil {
		t.Fatal(e)
	}
	result, e := r.Refresh(ciHost(), first.Request)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.CommitLocalRotation(context.Background(), first, result); e != nil {
		t.Fatal(e)
	}
	active, e = r.LocalActive(context.Background(), in.LocalSession)
	if e != nil {
		t.Fatal(e)
	}
	second, e := r.BeginLocalRotation(context.Background(), active)
	if e != nil {
		t.Fatal(e)
	}
	logout, e := r.BeginLocalLogout(context.Background(), in.LocalSession, epoch)
	if e != nil {
		t.Fatal(e)
	}
	if logout.Request != second.Request {
		t.Fatal("logout lost uncertain rotation proof")
	}
	projection := newResourceLegacyProjection(t, db, "company_interface_local_operations", "company_interface_local_control", "company_interface_local_contexts", "company_interface_grants", "company_interface_sessions")
	before := projection.snapshot(t, db, "before90rollback")
	if e = goose.DownTo(db, "migrations", 89); e != nil {
		t.Fatal(e)
	}
	if before != projection.snapshot(t, db, "after90rollback") {
		t.Fatal("rollback changed custody")
	}
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	if before != projection.snapshot(t, db, "after90reupgrade") {
		t.Fatal("reupgrade changed custody")
	}
	recovered, e := r.BeginLocalLogout(context.Background(), in.LocalSession, epoch)
	if e != nil || recovered != logout {
		t.Fatal("pending logout not decryptable", e)
	}
	for _, query := range []string{`UPDATE company_interface_local_operations SET central_origin='https://other.example' WHERE state='pending'`, `UPDATE company_interface_local_operations SET ciphertext='bad'::bytea WHERE state='pending'`, `UPDATE company_interface_local_operations SET state='pending',settled_at=NULL,ciphertext='bad'::bytea WHERE state='settled'`} {
		if _, e = db.Exec(query); e == nil {
			t.Fatal("custody guard accepted mutation")
		}
	}
}
func TestCompanyInterfaceActiveCleanupProofExpiryAndBinding(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "bound", true: "expired"}[expired], func(t *testing.T) {
			db := auditDatabase(t, true)
			r := ciRepository(t, db)
			u, _ := ciUser(t, db)
			in := ciStart(t)
			tokens := ciIssue(t, r, u, in)
			request := ciRefresh(in, tokens)
			next, e := r.Refresh(ciHost(), request)
			if e != nil {
				t.Fatal(e)
			}
			for _, field := range []string{"grant", "proof", "operation", "family", "version", "origin", "browser"} {
				bad := request
				switch field {
				case "grant":
					bad.GrantCredential = ciSecret(t)
				case "proof":
					bad.RefreshToken = ciSecret(t)
				case "operation":
					bad.OperationID = ciStart(t).AttemptID
				case "family":
					bad.SessionID = ciStart(t).AttemptID
				case "version":
					bad.Version++
				case "origin":
					bad.LocalOrigin = "https://other.example"
				case "browser":
					bad.LocalSession = ciSecret(t)
				}
				if _, e = r.RevokeFamilyProof(ciHost(), bad); !errors.Is(e, ci.ErrDenied) {
					t.Fatal("cleanup substitution", field, e)
				}
			}
			if expired {
				ciExec(t, db, `UPDATE company_interface_rotations SET recovery_expires_at=clock_timestamp()-INTERVAL '1 second' WHERE session_id=$1`, tokens.SessionID)
				if _, e = r.RevokeFamilyProof(ciHost(), request); !errors.Is(e, ci.ErrDenied) {
					t.Fatal("expired old proof accepted", e)
				}
				return
			}
			// A reset or grant revocation does not require renewed user authority to
			// acknowledge exact terminal cleanup; neither path issues credentials.
			ciExec(t, db, `DELETE FROM user_sessions WHERE user_id=$1`, u.ID)
			result, e := r.RevokeFamilyProof(ciHost(), request)
			if e != nil || !result.Revoked || result.SessionID != next.SessionID {
				t.Fatal("reset cleanup", e)
			}
			if _, e = r.Refresh(ciHost(), request); e == nil {
				t.Fatal("cleanup restored user authority")
			}
		})
	}
}
func TestCompanyInterfaceActiveLogoutRefreshRaceAndOriginalGrantCustody(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	in := ciStart(t)
	epoch, e := r.BeginLocal(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	tokens := ciIssue(t, r, u, in)
	if e = r.ActivateLocal(context.Background(), in, epoch, tokens); e != nil {
		t.Fatal(e)
	}
	active, e := r.LocalActive(context.Background(), in.LocalSession)
	if e != nil {
		t.Fatal(e)
	}
	operation, e := r.BeginLocalRotation(context.Background(), active)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { defer wg.Done(); <-start; _, err := r.Refresh(ciHost(), operation.Request); errs <- err }()
	go func() {
		defer wg.Done()
		<-start
		_, err := r.RevokeFamilyProof(ciHost(), operation.Request)
		errs <- err
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, ci.ErrDenied) && !errors.Is(err, ci.ErrConflict) {
			t.Fatal(err)
		}
	}
	var revoked bool
	if e = db.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, tokens.SessionID).Scan(&revoked); e != nil || !revoked {
		t.Fatal("terminal race lost", e)
	}
	// Restore no authority: use a newly approved login family with the SAME
	// installation. Its receipt has no installation credential, but encrypted
	// local activation must retain the exact grant for later old-context cleanup.
	login := ciStart(t)
	login.Mode = "login"
	login.InstallationProof = in.InstallationProof
	login.PreviousGrantID = tokens.GrantID
	login.GrantCredential = tokens.InstallationCredential
	loginEpoch, e := r.BeginLocal(context.Background(), login)
	if e != nil {
		t.Fatal(e)
	}
	loginTokens := ciIssue(t, r, u, login)
	if loginTokens.InstallationCredential != "" {
		t.Fatal("login issued installation secret")
	}
	if e = r.ActivateLocal(context.Background(), login, loginEpoch, loginTokens); e != nil {
		t.Fatal(e)
	}
	next := ciStart(t)
	next.Mode = "rebind"
	next.InstallationProof = in.InstallationProof
	next.PreviousGrantID = tokens.GrantID
	next.LocalOrigin = "https://replacement.example"
	nextEpoch, e := r.BeginLocal(context.Background(), next)
	if e != nil {
		t.Fatal(e)
	}
	nextTokens := ciIssue(t, r, u, next)
	if e = r.ActivateLocal(context.Background(), next, nextEpoch, nextTokens); e != nil {
		t.Fatal(e)
	}
	oldLogout, e := r.BeginLocalLogout(context.Background(), login.LocalSession, loginEpoch)
	if e != nil {
		t.Fatal("original login grant custody lost on rebind", e)
	}
	if oldLogout.GrantID != tokens.GrantID || oldLogout.Request.GrantCredential != tokens.InstallationCredential || oldLogout.Request.LocalOrigin != login.LocalOrigin {
		t.Fatal("logout retargeted successor")
	}
	confirmation, e := r.RevokeFamilyProof(ciHost(), oldLogout.Request)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.SettleLocalLogout(context.Background(), oldLogout, confirmation); e != nil {
		t.Fatal(e)
	}
	if _, e = r.LocalActive(context.Background(), next.LocalSession); e != nil {
		t.Fatal("old cleanup changed successor", e)
	}
	if e = db.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, nextTokens.SessionID).Scan(&revoked); e != nil || revoked {
		t.Fatal("successor family revoked", e)
	}
	if time.Until(nextTokens.RefreshExpiresAt) <= 0 {
		t.Fatal("fixture lifetime")
	}
}
