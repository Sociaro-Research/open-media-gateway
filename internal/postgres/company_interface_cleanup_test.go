package postgres

import (
	"errors"
	"sync"
	"testing"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
)

func TestCompanyInterfaceProofCleanupRollbackAndRefreshRace(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	in := ciStart(t)
	tokens := ciIssue(t, r, u, in)
	other := ciStart(t)
	other.Mode = "login"
	other.InstallationProof = in.InstallationProof
	other.PreviousGrantID = tokens.GrantID
	other.GrantCredential = tokens.InstallationCredential
	unaffected := ciIssue(t, r, u, other)
	proof := ciRedemption(in)
	fault := errors.New("isolated cleanup commit interruption")
	r.failWrite = func(point string) error {
		if point == "cleanup_family" {
			return fault
		}
		return nil
	}
	if e := r.RevokeIssuedFamily(ciHost(), proof); !errors.Is(e, fault) {
		t.Fatal("failure point not reached", e)
	}
	r.failWrite = nil
	var revoked bool
	var epoch int64
	if e := db.QueryRow(`SELECT revoked_at IS NOT NULL,session_epoch FROM company_interface_sessions WHERE id=$1`, tokens.SessionID).Scan(&revoked, &epoch); e != nil || revoked || epoch != 1 {
		t.Fatal("cleanup escaped rollback", e)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { defer wg.Done(); <-start; _, e := r.Refresh(ciHost(), ciRefresh(in, tokens)); results <- e }()
	go func() { defer wg.Done(); <-start; results <- r.RevokeIssuedFamily(ciHost(), proof) }()
	close(start)
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil && !errors.Is(e, ci.ErrDenied) && !errors.Is(e, ci.ErrConflict) {
			t.Fatal("unexpected race result", e)
		}
	}
	if e := db.QueryRow(`SELECT revoked_at IS NOT NULL,session_epoch FROM company_interface_sessions WHERE id=$1`, tokens.SessionID).Scan(&revoked, &epoch); e != nil || !revoked || epoch != 2 {
		t.Fatal("cleanup lost terminal race", e)
	}
	if _, e := r.Refresh(ciHost(), ciRefresh(in, tokens)); e == nil {
		t.Fatal("revoked family refreshed")
	}
	if e := r.RevokeIssuedFamily(ciHost(), proof); e != nil {
		t.Fatal("cleanup replay", e)
	}
	if e := db.QueryRow(`SELECT revoked_at IS NOT NULL,session_epoch FROM company_interface_sessions WHERE id=$1`, unaffected.SessionID).Scan(&revoked, &epoch); e != nil || revoked || epoch != 1 {
		t.Fatal("other family changed", e)
	}
	if e := db.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_grants WHERE id=$1`, tokens.GrantID).Scan(&revoked); e != nil || revoked {
		t.Fatal("permanent grant changed", e)
	}
}
