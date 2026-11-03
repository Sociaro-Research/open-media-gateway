package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

const ciTestMaster = "isolated company-interface fixture master"
const ciTestCentral = "https://central.example"

func ciHost() context.Context {
	return installation.WithContext(context.Background(), installation.Metadata{Role: "central", CentralOrigin: ciTestCentral})
}
func ciSecret(t *testing.T) string {
	t.Helper()
	s, e := ci.NewSecret()
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func ciRepository(t *testing.T, db *sql.DB) *CompanyInterfaceLifecycle {
	t.Helper()
	r, e := NewCompanyInterfaceLifecycle(db, ciTestMaster, ciTestCentral)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func ciUser(t *testing.T, db *sql.DB) (identity.User, [32]byte) {
	t.Helper()
	auth := NewAuthRepository(db)
	id := uuid.NewString()
	u, e := auth.UpsertGoogleUser(ciHost(), id, id+"@fixture.test", "Central fixture", "")
	if e != nil {
		t.Fatal(e)
	}
	hash := ci.Digest(ciSecret(t))
	if e = auth.CreateSession(ciHost(), hash[:], u.ID, time.Now().Add(400*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	u, e = auth.UserBySession(ciHost(), hash[:], time.Now())
	if e != nil || !ci.ValidID(u.SourceSessionID) {
		t.Fatal("private anchor missing", e)
	}
	raw, _ := json.Marshal(u)
	if strings.Contains(string(raw), u.SourceSessionID) {
		t.Fatal("private anchor serialized")
	}
	return u, hash
}
func ciStart(t *testing.T) ci.Start {
	return ci.Start{AttemptID: uuid.NewString(), InstallationProof: ciSecret(t), Proof: ciSecret(t), LocalSession: ciSecret(t), LocalOrigin: "https://local.example", Label: "Fixture installation", Mode: "connect", Protocol: ci.Protocol{Purpose: ci.Purpose, Version: ci.Version, Capabilities: ci.RequiredCapabilities()}}
}
func ciPrepare(t *testing.T, r *CompanyInterfaceLifecycle, u identity.User, in ci.Start) (context.Context, ci.Challenge, ci.Approval) {
	t.Helper()
	ctx := identity.WithUser(ciHost(), u)
	c, e := r.Start(ciHost(), in)
	if e != nil {
		t.Fatal("start", e)
	}
	if _, _, e = r.Describe(ctx, c.Code); e != nil {
		t.Fatal("describe", e)
	}
	return ctx, c, ci.Approval{OperationID: uuid.NewString(), Code: c.Code, Consent: true}
}
func ciRedemption(in ci.Start) ci.Redemption {
	return ci.Redemption{AttemptID: in.AttemptID, OperationID: uuid.NewString(), InstallationProof: in.InstallationProof, Proof: in.Proof, LocalSession: in.LocalSession, LocalOrigin: in.LocalOrigin, ConnectionRevision: in.ConnectionRevision}
}
func ciIssue(t *testing.T, r *CompanyInterfaceLifecycle, u identity.User, in ci.Start) ci.Tokens {
	t.Helper()
	ctx, _, approval := ciPrepare(t, r, u, in)
	if _, e := r.Approve(ctx, approval); e != nil {
		t.Fatal("approve", e)
	}
	v, e := r.Redeem(ciHost(), ciRedemption(in))
	if e != nil {
		t.Fatal("redeem", e)
	}
	return v
}
func ciExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, e := db.Exec(q, args...); e != nil {
		t.Fatal(e)
	}
}
func ciCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	m := map[string]int{}
	for _, table := range []string{"organizations", "organization_members", "company_interface_installations", "company_interface_grants", "company_interface_sessions", "user_api_keys", "playground_key_slots", "playground_key_requests", "organization_credits"} {
		var n int
		if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		m[table] = n
	}
	return m
}
func ciCredentials(in ci.Start, v ci.Tokens) ci.Credentials {
	return ci.Credentials{Installation: v.InstallationCredential, Session: v.AccessToken, LocalSessionHash: ci.Digest(in.LocalSession)}
}
func ciGate(db *sql.DB) ci.Gate {
	a := NewCompanyInterfaceAuthority(db)
	return ci.Gate{Authority: a, Keys: a, Recipients: a, CentralOrigin: ciTestCentral}
}
func ciRefresh(in ci.Start, v ci.Tokens) ci.Refresh {
	return ci.Refresh{OperationID: uuid.NewString(), GrantCredential: v.InstallationCredential, RefreshToken: v.RefreshToken, LocalSession: in.LocalSession, LocalOrigin: in.LocalOrigin, SessionID: v.SessionID, Version: v.Version}
}

func TestCompanyInterfaceAtomicProvisioningFailureAtEveryWrite(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	for _, point := range []string{"company", "membership", "installation", "grant", "session", "key", "key_slot", "key_request", "managed_key", "receipt"} {
		t.Run(point, func(t *testing.T) {
			in := ciStart(t)
			ctx, _, a := ciPrepare(t, r, u, in)
			before := ciCounts(t, db)
			fault := errors.New("fixture write interruption")
			r.failWrite = func(name string) error {
				if name == point {
					return fault
				}
				return nil
			}
			if _, e := r.Approve(ctx, a); !errors.Is(e, fault) {
				t.Fatal("fault not exercised", e)
			}
			r.failWrite = nil
			if !reflect.DeepEqual(before, ciCounts(t, db)) {
				t.Fatal("partial provisioning escaped rollback")
			}
			if _, e := r.Approve(ctx, a); e != nil {
				t.Fatal("same operation did not recover rollback", e)
			}
			v, e := r.Redeem(ciHost(), ciRedemption(in))
			if e != nil || v.PlaygroundKey == "" {
				t.Fatal("atomic key missing", e)
			}
			user, key, e := NewAPIKeyRepository(db).Authenticate(ciHost(), apikey.Hash(v.PlaygroundKey))
			if e != nil || user.ID != u.ID || key.OrganizationID != v.Company || !reflect.DeepEqual(key.Scopes, []string{apikey.Generation}) {
				t.Fatal("wrong managed authority", e)
			}
			after := ciCounts(t, db)
			if after["organization_credits"] != before["organization_credits"] {
				t.Fatal("provisioning created funds")
			}
		})
	}
}
func TestCompanyInterfaceExactApprovalRedemptionAndSubstitution(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	other, _ := ciUser(t, db)
	in := ciStart(t)
	ctx, c, a := ciPrepare(t, r, u, in)
	if _, _, e := r.Describe(identity.WithUser(ciHost(), other), c.Code); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("account switch", e)
	}
	if _, e := r.Approve(ctx, ci.Approval{OperationID: a.OperationID, Code: c.Code}); e == nil {
		t.Fatal("missing explicit consent accepted")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := r.Approve(ctx, a); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("same approve operation", e)
		}
	}
	counts := ciCounts(t, db)
	if counts["organizations"] != 1 || counts["company_interface_grants"] != 1 || counts["user_api_keys"] != 1 {
		t.Fatal("duplicate initial provisioning", counts)
	}
	changed := a
	changed.OperationID = uuid.NewString()
	if _, e := r.Approve(ctx, changed); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("changed approval operation", e)
	}
	// A committed lost reply survives process/repository restart and challenge expiry.
	ciExec(t, db, `UPDATE company_interface_attempts SET expires_at=clock_timestamp()-INTERVAL '1 minute' WHERE id=$1`, in.AttemptID)
	r = ciRepository(t, db)
	red := ciRedemption(in)
	v, e := r.Redeem(ciHost(), red)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.Redeem(ciHost(), red)
	if e != nil || !reflect.DeepEqual(v, again) {
		t.Fatal("not exact committed result", e)
	}
	for _, change := range []func(*ci.Redemption){func(v *ci.Redemption) { v.OperationID = uuid.NewString() }, func(v *ci.Redemption) { v.Proof = ciSecret(t) }, func(v *ci.Redemption) { v.InstallationProof = ciSecret(t) }, func(v *ci.Redemption) { v.LocalSession = ciSecret(t) }, func(v *ci.Redemption) { v.LocalOrigin = "https://foreign.example" }, func(v *ci.Redemption) { v.ConnectionRevision++ }, func(v *ci.Redemption) { v.AttemptID = uuid.NewString() }} {
		bad := red
		change(&bad)
		if _, e = r.Redeem(ciHost(), bad); e == nil {
			t.Fatal("substituted redemption accepted")
		}
	}
	if e = r.Cancel(ciHost(), red); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("cancel erased committed issuance", e)
	}
	var receipt []byte
	if e = db.QueryRow(`SELECT receipt FROM company_interface_attempts WHERE id=$1`, in.AttemptID).Scan(&receipt); e != nil {
		t.Fatal(e)
	}
	corrupt := append([]byte(nil), receipt...)
	corrupt[len(corrupt)-1] ^= 1
	ciExec(t, db, `UPDATE company_interface_attempts SET receipt=$2 WHERE id=$1`, in.AttemptID, corrupt)
	if _, e = r.Redeem(ciHost(), red); e == nil {
		t.Fatal("tampered receipt opened")
	}
	ciExec(t, db, `UPDATE company_interface_attempts SET receipt=$2 WHERE id=$1`, in.AttemptID, receipt)
	another := ciStart(t)
	otherTokens := ciIssue(t, r, other, another)
	if otherTokens.Company == v.Company {
		t.Fatal("two installations shared company")
	}
}

func TestCompanyInterfaceRefreshConcurrencyRecoveryAndRollback(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	in := ciStart(t)
	v := ciIssue(t, r, u, in)
	request := ciRefresh(in, v)
	for _, point := range []string{"rotation_family", "rotation_receipt"} {
		r.failWrite = func(name string) error {
			if name == point {
				return errors.New("rotation interruption")
			}
			return nil
		}
		if _, e := r.Refresh(ciHost(), request); e == nil {
			t.Fatal("fault not hit")
		}
		r.failWrite = nil
		var version int64
		if e := db.QueryRow(`SELECT token_version FROM company_interface_sessions WHERE id=$1`, v.SessionID).Scan(&version); e != nil || version != 1 {
			t.Fatal("rotation partially committed", e)
		}
	}
	type result struct {
		v ci.Tokens
		e error
	}
	out := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); v, e := r.Refresh(ciHost(), request); out <- result{v, e} }()
	}
	wg.Wait()
	close(out)
	var first ci.Tokens
	for got := range out {
		if got.e != nil {
			t.Fatal(got.e)
		}
		if first.SessionID == "" {
			first = got.v
		} else if !reflect.DeepEqual(first, got.v) {
			t.Fatal("same rotation issued two results")
		}
	}
	r = ciRepository(t, db)
	recovered, e := r.Refresh(ciHost(), request)
	if e != nil || !reflect.DeepEqual(first, recovered) {
		t.Fatal("rotation lost-reply recovery", e)
	}
	bad := request
	bad.LocalOrigin = "https://foreign.example"
	if _, e = r.Refresh(ciHost(), bad); e == nil {
		t.Fatal("changed payload recovered")
	}
	// Different simultaneous operations on version2: exactly one advances, the
	// unrelated stale operation conflicts without revoking the healthy family.
	first.InstallationCredential = v.InstallationCredential
	next := ciRefresh(in, first)
	other := next
	other.OperationID = uuid.NewString()
	out = make(chan result, 2)
	for _, q := range []ci.Refresh{next, other} {
		wg.Add(1)
		go func(q ci.Refresh) { defer wg.Done(); v, e := r.Refresh(ciHost(), q); out <- result{v, e} }(q)
	}
	wg.Wait()
	close(out)
	success, conflict := 0, 0
	var winner ci.Tokens
	for got := range out {
		if got.e == nil {
			success++
			winner = got.v
		} else if errors.Is(got.e, ci.ErrConflict) {
			conflict++
		} else {
			t.Fatal(got.e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	winner.InstallationCredential = v.InstallationCredential
	if _, e = ciGate(db).AuthenticateBrowser(ciHost(), ciCredentials(in, winner), time.Now()); e != nil {
		t.Fatal("healthy family revoked by stale concurrency", e)
	}
	if _, e = r.Refresh(ciHost(), request); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("old receipt restored superseded version", e)
	}
}

func TestCompanyInterfaceAdditionalUserAndBearerUsePreserveCurrentRoles(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	owner, _ := ciUser(t, db)
	member, _ := ciUser(t, db)
	in := ciStart(t)
	first := ciIssue(t, r, owner, in)
	ciExec(t, db, `INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'member')`, first.Company, member.ID)
	login := ciStart(t)
	login.Mode = "login"
	login.InstallationProof = in.InstallationProof
	login.PreviousGrantID = first.GrantID
	login.GrantCredential = first.InstallationCredential
	before := ciCounts(t, db)
	v := ciIssue(t, r, member, login)
	if v.Company != first.Company || v.PlaygroundKey != "" || v.InstallationCredential != "" || ciCounts(t, db)["user_api_keys"] != before["user_api_keys"] {
		t.Fatal("member login issued privilege/key")
	}
	// Existing direct-central semantics allow this verified browser member to use
	// an explicitly supplied coworker's key; creator role is never inherited.
	v.InstallationCredential = first.InstallationCredential
	c := ciCredentials(login, v)
	c.KeyPresent = true
	c.Key = first.PlaygroundKey
	p, e := ciGate(db).AuthenticateBrowser(ciHost(), c, time.Now())
	if e != nil || p.BrowserActor != member.ID || p.Actor != owner.ID {
		t.Fatal("coworker bearer-use semantics changed", e)
	}
	c.Key = "explicit invalid credential"
	if _, e = ciGate(db).AuthenticateBrowser(ciHost(), c, time.Now()); e == nil {
		t.Fatal("invalid key fell back to session")
	}
	c.Key = first.PlaygroundKey
	c.Session = ""
	if _, e = ciGate(db).AuthenticateBrowser(ciHost(), c, time.Now()); e == nil {
		t.Fatal("browser bypassed session")
	}
	if _, e = ciGate(db).AuthenticateSDK(ciHost(), c, time.Now()); e != nil {
		t.Fatal("SDK needed browser login", e)
	}
	key, secret, e := apikey.Generate(apikey.CreateInput{UserID: member.ID, OrganizationID: first.Company, Name: "Recipient fixture", Scopes: []string{apikey.Generation}}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = NewAPIKeyRepository(db).Create(identity.WithUser(ciHost(), owner), owner.ID, key, apikey.Hash(secret)); e != nil {
		t.Fatal(e)
	}
	c.Key = secret
	p, e = ciGate(db).AuthenticateSDK(ciHost(), c, time.Now())
	if e != nil || p.Actor != member.ID || p.Role != "member" {
		t.Fatal("creator elevated SDK recipient", e)
	}
	ciExec(t, db, `UPDATE organization_members SET removed_at=clock_timestamp() WHERE organization_id=$1 AND user_id=$2`, first.Company, member.ID)
	if _, e = ciGate(db).AuthenticateSDK(ciHost(), c, time.Now()); e == nil {
		t.Fatal("removed recipient used key")
	}
}

func TestCompanyInterfaceCentralLogoutResetAndLocalLogoutIsolation(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	owner, cookie := ciUser(t, db)
	in := ciStart(t)
	first := ciIssue(t, r, owner, in)
	login := ciStart(t)
	login.Mode = "login"
	login.InstallationProof = in.InstallationProof
	login.PreviousGrantID = first.GrantID
	login.GrantCredential = first.InstallationCredential
	second := ciIssue(t, r, owner, login)
	second.InstallationCredential = first.InstallationCredential
	if e := r.Logout(ciHost(), ciCredentials(in, first)); e != nil {
		t.Fatal(e)
	}
	if _, e := ciGate(db).AuthenticateBrowser(ciHost(), ciCredentials(login, second), time.Now()); e != nil {
		t.Fatal("local logout revoked another family", e)
	}
	if _, e := NewAuthRepository(db).UserBySession(ciHost(), cookie[:], time.Now()); e != nil {
		t.Fatal("local logout deleted central browser", e)
	}
	if e := NewAuthRepository(db).DeleteSession(ciHost(), cookie[:]); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Refresh(ciHost(), ciRefresh(login, second)); e == nil {
		t.Fatal("central logout family revived")
	}
	if _, _, e := NewAPIKeyRepository(db).Authenticate(ciHost(), apikey.Hash(first.PlaygroundKey)); e != nil {
		t.Fatal("central logout revoked independent key", e)
	}
	var revoked bool
	if e := db.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_source_sessions WHERE id=$1`, owner.SourceSessionID).Scan(&revoked); e != nil || !revoked {
		t.Fatal("source tombstone lost", e)
	}
	// A second actual central login creates another anchor; reset uses the real
	// password-reset repository path and invalidates every login of this user.
	hash := ci.Digest(ciSecret(t))
	auth := NewAuthRepository(db)
	if e := auth.CreateSession(ciHost(), hash[:], owner.ID, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	current, e := auth.UserBySession(ciHost(), hash[:], time.Now())
	if e != nil {
		t.Fatal(e)
	}
	login.AttemptID = uuid.NewString()
	login.Proof = ciSecret(t)
	login.LocalSession = ciSecret(t)
	third := ciIssue(t, r, current, login)
	third.InstallationCredential = first.InstallationCredential
	challenge := ci.Digest(ciSecret(t))
	ok, e := auth.SaveEmailChallenge(ciHost(), current.Email, "reset", challenge[:], time.Now().Add(time.Hour), nil)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if _, e = auth.CompleteEmailChallenge(ciHost(), challenge[:], "reset", "fixture password hash", time.Now(), nil); e != nil {
		t.Fatal(e)
	}
	if _, e = ciGate(db).AuthenticateBrowser(ciHost(), ciCredentials(login, third), time.Now()); e == nil {
		t.Fatal("password reset left delegated session live")
	}
	if _, _, e = NewAPIKeyRepository(db).Authenticate(ciHost(), apikey.Hash(first.PlaygroundKey)); e != nil {
		t.Fatal("reset revoked independent key", e)
	}
}

func TestCompanyInterfaceReconnectRebindRetainsCompanyAndRevokedHistory(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	in := ciStart(t)
	v := ciIssue(t, r, u, in)
	ctx := identity.WithUser(ciHost(), u)
	if e := r.Revoke(ctx, v.GrantID); e != nil {
		t.Fatal(e)
	}
	reconnect := ciStart(t)
	reconnect.InstallationProof = in.InstallationProof
	reconnect.PreviousGrantID = v.GrantID
	next := ciIssue(t, r, u, reconnect)
	if next.Company != v.Company || next.GrantID == v.GrantID || next.GrantEpoch <= v.GrantEpoch || next.PlaygroundKey != "" {
		t.Fatal("reconnect reprovisioned or resurrected")
	}
	for _, days := range []int{31, 366} {
		g, e := NewCompanyInterfaceAuthority(db).Installation(ciHost(), ci.Digest(next.InstallationCredential))
		if e != nil || g.Validate(time.Now().AddDate(0, 0, days)) != nil {
			t.Fatal("permanent installation cutoff", e)
		}
	}
	bad := ciStart(t)
	bad.InstallationProof = in.InstallationProof
	bad.PreviousGrantID = next.GrantID
	bad.LocalOrigin = "https://new-local.example"
	badctx, _, badapprove := ciPrepare(t, r, u, bad)
	if _, e := r.Approve(badctx, badapprove); e == nil {
		t.Fatal("implicit origin rebind accepted")
	}
	rebind := bad
	rebind.AttemptID = uuid.NewString()
	rebind.Proof = ciSecret(t)
	rebind.Mode = "rebind"
	rebound := ciIssue(t, r, u, rebind)
	if rebound.Company != v.Company || rebound.LocalOrigin != bad.LocalOrigin || rebound.GrantID == next.GrantID {
		t.Fatal("explicit rebind lost stable company")
	}
	var active, total int
	if e := db.QueryRow(`SELECT count(*) FILTER(WHERE revoked_at IS NULL),count(*) FROM company_interface_grants`).Scan(&active, &total); e != nil || active != 1 || total != 3 {
		t.Fatal("grant history/uniqueness", active, total, e)
	}
	if _, e := r.Refresh(ciHost(), ciRefresh(reconnect, next)); e == nil {
		t.Fatal("old grant session survived rebind")
	}
	if ciCounts(t, db)["organizations"] != 1 {
		t.Fatal("reprovisioned installation company")
	}
}

func TestCompanyInterfaceLocalCASLostReplyAndStaleContext(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	in := ciStart(t)
	epoch, e := r.BeginLocal(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.BeginLocal(context.Background(), in)
	if e != nil || again != epoch {
		t.Fatal("pending retry changed epoch", e)
	}
	persisted, pe, e := ciRepository(t, db).LocalPending(context.Background(), in.LocalSession)
	if e != nil || pe != epoch || !reflect.DeepEqual(persisted, in) {
		t.Fatal("pending proof restart recovery", e)
	}
	v := ciIssue(t, r, u, in)
	if e = r.ActivateLocal(context.Background(), in, epoch+1, v); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("stale local activation", e)
	}
	if e = r.ActivateLocal(context.Background(), in, epoch, v); e != nil {
		t.Fatal(e)
	}
	next, e := r.Refresh(ciHost(), ciRefresh(in, v))
	if e != nil {
		t.Fatal(e)
	}
	if e = r.RotateLocal(context.Background(), in.LocalSession, epoch, 0, 1, next); e != nil {
		t.Fatal(e)
	}
	if e = r.RotateLocal(context.Background(), in.LocalSession, epoch, 0, 1, next); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("old rotation overwrote later context", e)
	}
	if e = r.ClearLocal(context.Background(), in.LocalSession, epoch); e != nil {
		t.Fatal(e)
	}
	if e = r.RotateLocal(context.Background(), in.LocalSession, epoch, 0, 1, next); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("late rotation resurrected logout", e)
	}
	savedGrant, err := r.LocalInstallation(context.Background())
	if err != nil || savedGrant.Credential != v.InstallationCredential {
		t.Fatal("logout lost permanent installation credential", err)
	}
	newer := ciStart(t)
	newer.Mode = "login"
	newer.InstallationProof = in.InstallationProof
	newer.PreviousGrantID = v.GrantID
	newer.GrantCredential = v.InstallationCredential
	epoch, e = r.BeginLocal(context.Background(), newer)
	if e != nil {
		t.Fatal(e)
	}
	newtokens := ciIssue(t, r, u, newer)
	if e = r.AdvanceLocalRevision(context.Background(), 0); e != nil {
		t.Fatal(e)
	}
	if e = r.ActivateLocal(context.Background(), newer, epoch, newtokens); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("old global revision activated", e)
	}
	if _, _, e = r.LocalPending(context.Background(), newer.LocalSession); e != nil {
		t.Fatal("uncertain old proof was discarded", e)
	}
}

func TestCompanyInterfaceProtocolCryptoAndExpiredChallenges(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	in := ciStart(t)
	ctx, c, a := ciPrepare(t, r, u, in)
	ciExec(t, db, `UPDATE company_interface_attempts SET expires_at=clock_timestamp()-INTERVAL '1 second' WHERE id=$1`, in.AttemptID)
	if _, e := r.Approve(ctx, a); e == nil {
		t.Fatal("expired challenge provisioned")
	}
	if _, e := r.Start(context.Background(), ciStart(t)); e == nil {
		t.Fatal("noncentral host enabled protocol")
	}
	invalid := ciStart(t)
	invalid.Protocol.Capabilities = invalid.Protocol.Capabilities[1:]
	if _, e := r.Start(ciHost(), invalid); !errors.Is(e, ci.ErrProtocol) {
		t.Fatal("partial surface negotiated", e)
	}
	_ = c
	cipher, e := ci.NewReceiptCipher(ciTestMaster)
	if e != nil {
		t.Fatal(e)
	}
	sealed, e := cipher.Seal("fixture secret", []string{"one", "operation"})
	if e != nil {
		t.Fatal(e)
	}
	var s string
	if e = cipher.Open(sealed, []string{"other", "operation"}, &s); e == nil {
		t.Fatal("swapped receipt opened")
	}
	if _, e := db.Exec(`UPDATE company_interface_source_sessions SET revoked_at=clock_timestamp() WHERE id=$1`, u.SourceSessionID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE company_interface_source_sessions SET revoked_at=NULL WHERE id=$1`, u.SourceSessionID); e == nil {
		t.Fatal("source resurrected")
	}
}

func TestCompanyInterfaceLegacyApprovalPreservesOriginalCompanyAndKey(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	in := ciStart(t)
	ciExec(t, db, `UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`)
	legacyCipher, e := providerconfig.NewCipher(ciTestMaster)
	if e != nil {
		t.Fatal(e)
	}
	legacyRepo := NewSystemRepository(db)
	manager := wa.New(legacyRepo, legacyCipher)
	device, e := wa.DeviceCode()
	if e != nil {
		t.Fatal(e)
	}
	challenge, e := manager.Start(ciHost(), device, wa.Challenge{InstallationProof: in.InstallationProof, Label: in.Label, Origin: in.LocalOrigin})
	if e != nil {
		t.Fatal(e)
	}
	approved, e := legacyRepo.Approve(ciHost(), u.ID, challenge.Code, wa.Approval{Consent: true})
	if e != nil {
		t.Fatal(e)
	}
	issued, secret, e := manager.Poll(ciHost(), device)
	if e != nil {
		t.Fatal(e)
	}
	company := approved.Company
	key := apikey.Key{ID: issued.KeyID}
	if string(installation.ProofDigest(in.InstallationProof)) != string(wa.Digest("installation:"+in.InstallationProof)) {
		t.Fatal("legacy identity bytes changed")
	}
	var original string
	if e = db.QueryRow(`SELECT row_to_json(k)::text FROM user_api_keys k WHERE id=$1`, key.ID).Scan(&original); e != nil {
		t.Fatal(e)
	}
	ctx, _, approval := ciPrepare(t, r, u, in)
	if _, e = r.Approve(ctx, approval); e == nil {
		t.Fatal("legacy scope silently promoted")
	}
	upgrade := in
	upgrade.AttemptID = uuid.NewString()
	upgrade.Proof = ciSecret(t)
	upgrade.Mode = "legacy-upgrade"
	v := ciIssue(t, r, u, upgrade)
	if v.Company != company || v.PlaygroundKey == "" {
		t.Fatal("legacy upgrade reprovisioned/lacked managed key")
	}
	var after string
	if e = db.QueryRow(`SELECT row_to_json(k)::text FROM user_api_keys k WHERE id=$1`, key.ID).Scan(&after); e != nil || after != original {
		t.Fatal("old key scope/expiry/history changed", e)
	}
	if ciCounts(t, db)["organizations"] != 1 {
		t.Fatal("legacy company duplicated")
	}
	bad := ciStart(t)
	bad.Mode = "login"
	bad.InstallationProof = in.InstallationProof
	bad.PreviousGrantID = v.GrantID
	bad.GrantCredential = secret
	ctx, _, approval = ciPrepare(t, r, u, bad)
	if _, e = r.Approve(ctx, approval); e == nil {
		t.Fatal("legacy generation key adopted new purpose")
	}
}

func TestCompanyInterfaceRefreshRacesWithTerminalRevocation(t *testing.T) {
	for _, kind := range []string{"central_logout", "local_logout", "grant_revoke"} {
		t.Run(kind, func(t *testing.T) {
			db := auditDatabase(t, true)
			r := ciRepository(t, db)
			u, cookie := ciUser(t, db)
			in := ciStart(t)
			v := ciIssue(t, r, u, in)
			request := ciRefresh(in, v)
			ctx := identity.WithUser(ciHost(), u)
			start := make(chan struct{})
			done := make(chan error, 2)
			go func() { <-start; _, e := r.Refresh(ciHost(), request); done <- e }()
			go func() {
				<-start
				var e error
				switch kind {
				case "central_logout":
					e = NewAuthRepository(db).DeleteSession(ciHost(), cookie[:])
				case "local_logout":
					e = r.Logout(ciHost(), ciCredentials(in, v))
				case "grant_revoke":
					e = r.Revoke(ctx, v.GrantID)
				}
				done <- e
			}()
			close(start)
			first, second := <-done, <-done
			// A refresh that wins can invalidate the old access credential before local
			// logout authenticates. Retry logout using the exact committed rotation.
			if kind == "local_logout" && (first != nil || second != nil) {
				next, e := r.Refresh(ciHost(), request)
				if e == nil {
					next.InstallationCredential = v.InstallationCredential
					if e = r.Logout(ciHost(), ciCredentials(in, next)); e != nil {
						t.Fatal(e)
					}
				}
			}
			if _, e := r.Refresh(ciHost(), request); e == nil {
				t.Fatal("revocation race resurrected family")
			}
			var revoked bool
			if e := db.QueryRow(`SELECT revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, v.SessionID).Scan(&revoked); e != nil || !revoked {
				t.Fatal("family tombstone missing", e, first, second)
			}
			if _, _, e := NewAPIKeyRepository(db).Authenticate(ciHost(), apikey.Hash(v.PlaygroundKey)); e != nil {
				t.Fatal("browser revocation affected independent generation key", e)
			}
		})
	}
}

func TestCompanyInterfaceInitialConcurrencyAndPermissionChanges(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	first := ciStart(t)
	second := ciStart(t)
	second.InstallationProof = first.InstallationProof
	ctx, _, a := ciPrepare(t, r, u, first)
	_, _, b := ciPrepare(t, r, u, second)
	out := make(chan error, 2)
	var wg sync.WaitGroup
	for _, approval := range []ci.Approval{a, b} {
		wg.Add(1)
		go func(v ci.Approval) { defer wg.Done(); _, e := r.Approve(ctx, v); out <- e }(approval)
	}
	wg.Wait()
	close(out)
	success, failed := 0, 0
	for e := range out {
		if e == nil {
			success++
		} else {
			failed++
		}
	}
	if success != 1 || failed != 1 || ciCounts(t, db)["organizations"] != 1 {
		t.Fatal("concurrent distinct attempts reprovisioned", success, failed)
	}
	var company, grant string
	if e := db.QueryRow(`SELECT organization_id::text,id::text FROM company_interface_grants`).Scan(&company, &grant); e != nil {
		t.Fatal(e)
	}
	ciExec(t, db, `UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, company, u.ID)
	if e := r.Revoke(ctx, grant); e == nil {
		t.Fatal("demoted initiating actor retained Supermanager power")
	}
	var grants int
	if e := db.QueryRow(`SELECT count(*) FROM company_interface_grants`).Scan(&grants); e != nil || grants != 1 {
		t.Fatal("demotion reprovisioned/transferred installation")
	}
}

func TestCompanyInterfacePendingLogoutRetainsUnknownOutcomeProof(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "committed_lost_reply"}[approved], func(t *testing.T) {
			db := auditDatabase(t, true)
			r := ciRepository(t, db)
			u, _ := ciUser(t, db)
			in := ciStart(t)
			epoch, e := r.BeginLocal(context.Background(), in)
			if e != nil {
				t.Fatal(e)
			}
			ctx, _, approval := ciPrepare(t, r, u, in)
			if approved {
				if _, e = r.Approve(ctx, approval); e != nil {
					t.Fatal(e)
				}
			}
			if e = r.ClearLocal(context.Background(), in.LocalSession, epoch); e != nil {
				t.Fatal(e)
			}
			reopened := ciRepository(t, db)
			retained, originalEpoch, e := reopened.LocalPending(context.Background(), in.LocalSession)
			if e != nil || !reflect.DeepEqual(retained, in) || originalEpoch != epoch {
				t.Fatal("logout discarded original-destination uncertain proof", e)
			}
			recovery, e := reopened.RecoverLocalAttempt(context.Background(), in.AttemptID)
			if e != nil || recovery.State != "quarantined" || recovery.CentralOrigin != ciTestCentral || recovery.ContextEpoch != epoch {
				t.Fatal("recovery custody binding", e)
			}
			if e = reopened.SettleLocalRecovery(context.Background(), recovery, ci.Reconciliation{Binding: ci.BindStart(ciTestCentral, in), Fingerprint: ci.Fingerprint(ci.BindStart(ciTestCentral, in)), State: "unknown"}); e == nil {
				t.Fatal("unknown outcome discarded proof")
			}
			changed := recovery
			changed.Request.Proof = ciSecret(t)
			if e = reopened.SettleLocalRecovery(context.Background(), changed, ci.Reconciliation{Binding: ci.BindStart(ciTestCentral, in), Fingerprint: ci.Fingerprint(ci.BindStart(ciTestCentral, in)), State: "cancelled"}); e == nil {
				t.Fatal("substituted cleanup accepted", e)
			}
			// A newer pending context must survive reconciliation of the old attempt.
			newer := ciStart(t)
			newer.LocalSession = in.LocalSession
			newer.InstallationProof = in.InstallationProof
			newer.LocalOrigin = "https://newer-local.example"
			if _, e = reopened.BeginLocal(context.Background(), newer); e != nil {
				t.Fatal(e)
			}
			if approved {
				v, err := reopened.Redeem(ciHost(), ciRedemption(in))
				if err != nil {
					t.Fatal(err)
				}
				if e = reopened.ActivateLocal(context.Background(), in, epoch, v); !errors.Is(e, ci.ErrConflict) {
					t.Fatal("late callback resurrected logout", e)
				}
				if e = reopened.Logout(ciHost(), ciCredentials(in, v)); e != nil {
					t.Fatal(e)
				}
				if e = reopened.Revoke(ctx, v.GrantID); e != nil {
					t.Fatal("confirm exact grant revocation", e)
				}
			} else if e = reopened.Cancel(ciHost(), ciRedemption(in)); e != nil {
				t.Fatal(e)
			}
			confirmation, err := reopened.Reconcile(ciHost(), ciRedemption(in))
			if err != nil {
				t.Fatal(err)
			}
			if e = reopened.SettleLocalRecovery(context.Background(), recovery, confirmation); e != nil {
				t.Fatal("confirmed exact cleanup", e)
			}
			if _, e = reopened.RecoverLocalAttempt(context.Background(), in.AttemptID); e == nil {
				t.Fatal("settled secret retained")
			}
			current, _, e := reopened.LocalPending(context.Background(), newer.LocalSession)
			if e != nil || !reflect.DeepEqual(current, newer) {
				t.Fatal("old cleanup erased newer pending attempt", e)
			}

		})
	}
}

func TestCompanyInterfaceRebindFencesAnotherUsersDelayedResponse(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	a, _ := ciUser(t, db)
	b, _ := ciUser(t, db)
	in := ciStart(t)
	epochA, e := r.BeginLocal(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	first := ciIssue(t, r, a, in)
	if e = r.ActivateLocal(context.Background(), in, epochA, first); e != nil {
		t.Fatal(e)
	}
	ciExec(t, db, `INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'member')`, first.Company, b.ID)
	login := ciStart(t)
	login.Mode = "login"
	login.InstallationProof = in.InstallationProof
	login.PreviousGrantID = first.GrantID
	login.GrantCredential = first.InstallationCredential
	epochB, e := r.BeginLocal(context.Background(), login)
	if e != nil {
		t.Fatal(e)
	}
	second := ciIssue(t, r, b, login)
	if e = r.ActivateLocal(context.Background(), login, epochB, second); e != nil {
		t.Fatal(e)
	}
	if e = r.CheckLocalEpoch(context.Background(), login.LocalSession, epochB, 0, 1, second.SessionID, b.ID); e != nil {
		t.Fatal("before-body old grant fence", e)
	}
	second.InstallationCredential = first.InstallationCredential
	delayedRotation, e := r.Refresh(ciHost(), ciRefresh(login, second))
	if e != nil {
		t.Fatal(e)
	}
	rebind := in
	rebind.AttemptID = uuid.NewString()
	rebind.Proof = ciSecret(t)
	rebind.Mode = "rebind"
	rebind.PreviousGrantID = first.GrantID
	rebind.LocalOrigin = "https://rebound.example"
	nextEpoch, e := r.BeginLocal(context.Background(), rebind)
	if e != nil {
		t.Fatal(e)
	}
	next := ciIssue(t, r, a, rebind)
	if e = r.ActivateLocal(context.Background(), rebind, nextEpoch, next); e != nil {
		t.Fatal(e)
	}
	if e = r.CheckLocalEpoch(context.Background(), login.LocalSession, epochB, 0, 1, second.SessionID, b.ID); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("delayed old-grant body passed after rebind", e)
	}
	if e = r.RotateLocal(context.Background(), login.LocalSession, epochB, 0, 1, delayedRotation); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("delayed old-grant rotation survived rebind", e)
	}
	if e = r.CheckLocalEpoch(context.Background(), rebind.LocalSession, nextEpoch, 0, 1, next.SessionID, a.ID); e != nil {
		t.Fatal("new grant context not usable", e)
	}
}

func TestCompanyInterfaceConfirmedCancellationClearsExactLivePending(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	in := ciStart(t)
	if _, e := r.BeginLocal(context.Background(), in); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Start(ciHost(), in); e != nil {
		t.Fatal(e)
	}
	recovery, e := r.RecoverLocalAttempt(context.Background(), in.AttemptID)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Cancel(ciHost(), ciRedemption(in)); e != nil {
		t.Fatal(e)
	}
	confirmation, err := r.Reconcile(ciHost(), ciRedemption(in))
	if err != nil {
		t.Fatal(err)
	}
	if e = r.SettleLocalRecovery(context.Background(), recovery, confirmation); e != nil {
		t.Fatal(e)
	}
	if _, _, e = r.LocalPending(context.Background(), in.LocalSession); e == nil {
		t.Fatal("confirmed cancelled proof still in live context")
	}
}

func TestCompanyInterfaceFamilyLogoutCannotSettleNewGrantCustody(t *testing.T) {
	for _, mode := range []string{"connect", "legacy-upgrade", "rebind"} {
		t.Run(mode, func(t *testing.T) {
			db := auditDatabase(t, true)
			r := ciRepository(t, db)
			u, cookie := ciUser(t, db)
			in := ciStart(t)
			if mode == "legacy-upgrade" {
				ciExec(t, db, `UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`)
				cipher, e := providerconfig.NewCipher(ciTestMaster)
				if e != nil {
					t.Fatal(e)
				}
				repo := NewSystemRepository(db)
				manager := wa.New(repo, cipher)
				device, e := wa.DeviceCode()
				if e != nil {
					t.Fatal(e)
				}
				challenge, e := manager.Start(ciHost(), device, wa.Challenge{InstallationProof: in.InstallationProof, Label: in.Label, Origin: in.LocalOrigin})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = repo.Approve(ciHost(), u.ID, challenge.Code, wa.Approval{Consent: true}); e != nil {
					t.Fatal(e)
				}
				if _, _, e = manager.Poll(ciHost(), device); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "rebind" {
				epoch, e := r.BeginLocal(context.Background(), in)
				if e != nil {
					t.Fatal(e)
				}
				original := ciIssue(t, r, u, in)
				if e = r.ActivateLocal(context.Background(), in, epoch, original); e != nil {
					t.Fatal(e)
				}
				in.AttemptID = uuid.NewString()
				in.Proof = ciSecret(t)
				in.PreviousGrantID = original.GrantID
				in.LocalOrigin = "https://rebound.example"
			}
			in.Mode = mode
			epoch, e := r.BeginLocal(context.Background(), in)
			if e != nil {
				t.Fatal(e)
			}
			ctx, _, approve := ciPrepare(t, r, u, in)
			if _, e = r.Approve(ctx, approve); e != nil {
				t.Fatal(e)
			}
			if e = r.ClearLocal(context.Background(), in.LocalSession, epoch); e != nil {
				t.Fatal(e)
			}
			recovery, e := r.RecoverLocalAttempt(context.Background(), in.AttemptID)
			if e != nil {
				t.Fatal(e)
			}
			result, e := r.Redeem(ciHost(), ciRedemption(in))
			if e != nil {
				t.Fatal(e)
			}
			if e = r.Logout(ciHost(), ciCredentials(in, result)); e != nil {
				t.Fatal(e)
			}
			confirmation, e := r.Reconcile(ciHost(), ciRedemption(in))
			if e != nil {
				t.Fatal(e)
			}
			if confirmation.GrantRevoked || !confirmation.SessionRevoked {
				t.Fatal("fixture did not distinguish family and grant revocation")
			}
			if e = r.SettleLocalRecovery(context.Background(), recovery, confirmation); e == nil {
				t.Fatal("family-only logout discarded the sole active permanent-grant custody")
			}
			if _, e = ciRepository(t, db).RecoverLocalAttempt(context.Background(), in.AttemptID); e != nil {
				t.Fatal("restart lost unresolved permanent-grant proof", e)
			}
			// Installation custody is independent of the initiating member and source
			// login. Recovery after removal/logout returns no user/session/key secrets.
			ciExec(t, db, `UPDATE organization_members SET removed_at=clock_timestamp() WHERE organization_id=$1 AND user_id=$2`, result.Company, u.ID)
			if e = NewAuthRepository(db).DeleteSession(ciHost(), cookie[:]); e != nil {
				t.Fatal(e)
			}
			recovered, e := ciRepository(t, db).RecoverInstallation(ciHost(), ciRedemption(in))
			if e != nil || recovered.Credential != result.InstallationCredential {
				t.Fatal("permanent installation-only recovery failed", e)
			}
			encoded, _ := json.Marshal(recovered)
			for _, secret := range []string{result.AccessToken, result.RefreshToken, result.PlaygroundKey} {
				if secret != "" && strings.Contains(string(encoded), secret) {
					t.Fatal("installation recovery exposed user secret")
				}
			}
			bad := confirmation
			bad.Binding.LocalOrigin = "https://substituted.example"
			if e = r.PreserveRecoveredInstallation(context.Background(), recovery, bad, recovered); e == nil {
				t.Fatal("substituted reconciliation accepted")
			}
			newer := in
			newer.AttemptID = uuid.NewString()
			newer.Proof = ciSecret(t)
			newerEpoch, e := r.BeginLocal(context.Background(), newer)
			if e != nil {
				t.Fatal(e)
			}
			if e = r.PreserveRecoveredInstallation(context.Background(), recovery, confirmation, recovered); !errors.Is(e, ci.ErrConflict) {
				t.Fatal("newer pending context overwritten", e)
			}
			if e = r.ClearLocal(context.Background(), newer.LocalSession, newerEpoch); e != nil {
				t.Fatal(e)
			}
			if e = r.PreserveRecoveredInstallation(context.Background(), recovery, confirmation, recovered); e != nil {
				t.Fatal("installation custody not preserved", e)
			}
			if e = r.SettleLocalRecovery(context.Background(), recovery, confirmation); e != nil {
				t.Fatal("durably preserved grant could not settle", e)
			}
			durable, e := ciRepository(t, db).LocalInstallation(context.Background())
			if e != nil || durable != recovered {
				t.Fatal("restart lost permanent custody", e)
			}
			if e = r.CheckLocalEpoch(context.Background(), in.LocalSession, epoch, 0, 1, result.SessionID, u.ID); e == nil {
				t.Fatal("installation-only recovery activated a user")
			}
		})
	}
}

func TestCompanyInterfaceRecoveredGrantRequiresOriginalRevision(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	in := ciStart(t)
	epoch, e := r.BeginLocal(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	ctx, _, approval := ciPrepare(t, r, u, in)
	if _, e = r.Approve(ctx, approval); e != nil {
		t.Fatal(e)
	}
	if e = r.ClearLocal(context.Background(), in.LocalSession, epoch); e != nil {
		t.Fatal(e)
	}
	recovery, e := r.RecoverLocalAttempt(context.Background(), in.AttemptID)
	if e != nil {
		t.Fatal(e)
	}
	confirmation, e := r.Reconcile(ciHost(), ciRedemption(in))
	if e != nil {
		t.Fatal(e)
	}
	grant, e := r.RecoverInstallation(ciHost(), ciRedemption(in))
	if e != nil {
		t.Fatal(e)
	}
	if e = r.AdvanceLocalRevision(context.Background(), 0); e != nil {
		t.Fatal(e)
	}
	if e = r.PreserveRecoveredInstallation(context.Background(), recovery, confirmation, grant); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("changed connection revision overwritten", e)
	}
	if _, e = r.RecoverLocalAttempt(context.Background(), in.AttemptID); e != nil {
		t.Fatal("conflict discarded proof", e)
	}
}

func TestCompanyInterfaceLoginCleanupRequiresSameDurableGrant(t *testing.T) {
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	first := ciStart(t)
	issued := ciIssue(t, r, u, first)
	login := ciStart(t)
	login.Mode = "login"
	login.InstallationProof = first.InstallationProof
	login.PreviousGrantID = issued.GrantID
	login.GrantCredential = issued.InstallationCredential
	epoch, e := r.BeginLocal(context.Background(), login)
	if e != nil {
		t.Fatal(e)
	}
	v := ciIssue(t, r, u, login)
	v.InstallationCredential = issued.InstallationCredential
	if e = r.ClearLocal(context.Background(), login.LocalSession, epoch); e != nil {
		t.Fatal(e)
	}
	if e = r.Logout(ciHost(), ciCredentials(login, v)); e != nil {
		t.Fatal(e)
	}
	recovery, e := r.RecoverLocalAttempt(context.Background(), login.AttemptID)
	if e != nil {
		t.Fatal(e)
	}
	confirmation, e := r.Reconcile(ciHost(), ciRedemption(login))
	if e != nil {
		t.Fatal(e)
	}
	if e = r.SettleLocalRecovery(context.Background(), recovery, confirmation); !errors.Is(e, ci.ErrConflict) {
		t.Fatal("login cleanup lost the only undurable installation credential", e)
	}
	initialEpoch, e := r.BeginLocal(context.Background(), first)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.ActivateLocal(context.Background(), first, initialEpoch, issued); e != nil {
		t.Fatal(e)
	}
	wrong := confirmation
	wrong.GrantID = uuid.NewString()
	if e = r.SettleLocalRecovery(context.Background(), recovery, wrong); e == nil {
		t.Fatal("foreign grant cleanup confirmation accepted")
	}
	if e = r.SettleLocalRecovery(context.Background(), recovery, confirmation); e != nil {
		t.Fatal("same durable grant login cleanup", e)
	}
	durable, e := ciRepository(t, db).LocalInstallation(context.Background())
	if e != nil || durable.GrantID != issued.GrantID || durable.Credential != issued.InstallationCredential {
		t.Fatal("login cleanup changed permanent grant", e)
	}
}
