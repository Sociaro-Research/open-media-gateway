package companyinterface_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

type readinessBody struct {
	io.Reader
	before func()
}

func (b *readinessBody) Read(p []byte) (int, error) {
	if b.before != nil {
		f := b.before
		b.before = nil
		f()
	}
	return b.Reader.Read(p)
}
func TestCompanyHTTPServerReadinessControlsIssuanceAndPreservesMaintenance(t *testing.T) {
	db := fixtureDB(t)
	a, _, cookie := fixtureAuth(t, db, fixtureCentral, false)
	repo, e := postgres.NewCompanyInterfaceLifecycle(db, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	production, e := api.WithPublicAPI(api.NewGateway(api.Dependencies{CompanyInterface: repo, CompanyCentralOrigin: fixtureCentral, Auth: a}), "", fixtureCentral, "")
	if e != nil {
		t.Fatal(e)
	}
	secret := func() string {
		t.Helper()
		v, e := ci.NewSecret()
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	fresh := func() ci.Start {
		return ci.Start{AttemptID: uuid.NewString(), InstallationProof: secret(), Proof: secret(), LocalSession: secret(), LocalOrigin: fixtureLocal, Label: "Synthetic installation", Mode: "connect", Protocol: ci.Protocol{Purpose: ci.Purpose, Version: ci.Version, Capabilities: ci.RequiredCapabilities()}}
	}
	proof := func(h http.Handler, operation string, value any, before func()) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		body := &readinessBody{Reader: bytes.NewReader(raw), before: before}
		r := httptest.NewRequest("POST", fixtureCentral+ci.ProtocolBase+"/"+operation, body)
		r.Header.Set("Content-Type", "application/json")
		// No HTTP header can opt a production server into complete readiness.
		r.Header.Set("X-Company-Interface-Ready", "full-company-surface/v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	counts := func() [5]int {
		return [5]int{countFixture(t, db, "company_interface_attempts"), countFixture(t, db, "company_interface_grants"), countFixture(t, db, "company_interface_sessions"), countFixture(t, db, "organizations"), countFixture(t, db, "user_api_keys")}
	}
	before := counts()
	in := fresh()
	denied := proof(production, "start", in, nil)
	expectFixture(t, denied, 409)
	if !strings.Contains(denied.Body.String(), "company_interface_update_required") || counts() != before {
		t.Fatal("complete client bypassed partial server", denied.Code, counts(), before)
	}
	for _, h := range []http.Handler{production, ci.FixtureServerReadiness(production, nil)} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", fixtureCentral+ci.ProtocolBase+"/capabilities", nil))
		expectFixture(t, w, 200)
		var p ci.Protocol
		json.Unmarshal(w.Body.Bytes(), &p)
		if ci.Negotiate(p) == nil {
			t.Fatal("partial server advertised complete mode")
		}
	}
	var ready atomic.Bool
	h := ci.FixtureServerReadiness(production, ready.Load)
	ready.Store(true)
	started := proof(h, "start", in, nil)
	expectFixture(t, started, 200)
	var challenge ci.Challenge
	if e := json.Unmarshal(started.Body.Bytes(), &challenge); e != nil {
		t.Fatal(e)
	}
	expectFixture(t, callFixture(t, h, fixtureCentral, "/v1/company-interface/authorizations/inspect", `{"user_code":"`+challenge.Code+`"}`, cookie), 200)
	approval := `{"user_code":"` + challenge.Code + `","operation_id":"` + uuid.NewString() + `","consent":true}`
	before = counts()
	ready.Store(false)
	expectFixture(t, callFixture(t, h, fixtureCentral, "/v1/company-interface/authorizations/approve", approval, cookie), 409)
	if counts() != before {
		t.Fatal("readiness loss minted authority", counts(), before)
	}
	ready.Store(true)
	expectFixture(t, callFixture(t, h, fixtureCentral, "/v1/company-interface/authorizations/approve", approval, cookie), 200)
	redemption := ci.Redemption{AttemptID: in.AttemptID, OperationID: uuid.NewString(), InstallationProof: in.InstallationProof, Proof: in.Proof, LocalSession: in.LocalSession, LocalOrigin: in.LocalOrigin, ConnectionRevision: in.ConnectionRevision}
	before = counts()
	ready.Store(false)
	expectFixture(t, proof(h, "redeem", redemption, nil), 409)
	if counts() != before {
		t.Fatal("redemption failure changed approved custody", counts(), before)
	}
	var state string
	if e := db.QueryRow(`SELECT state FROM company_interface_attempts WHERE id=$1`, in.AttemptID).Scan(&state); e != nil || state != "approved" {
		t.Fatal("approved recovery lost", state, e)
	}
	ready.Store(true)
	redeemed := proof(h, "redeem", redemption, nil)
	expectFixture(t, redeemed, 200)
	var tokens ci.Tokens
	if e := json.Unmarshal(redeemed.Body.Bytes(), &tokens); e != nil {
		t.Fatal(e)
	}
	// Existing authenticated families can rotate while full portal readiness is
	// unavailable. This neither provisions a company nor advertises activation.
	ready.Store(false)
	before = counts()
	refresh := ci.Refresh{OperationID: uuid.NewString(), GrantCredential: tokens.InstallationCredential, RefreshToken: tokens.RefreshToken, LocalSession: in.LocalSession, LocalOrigin: in.LocalOrigin, SessionID: tokens.SessionID, Version: tokens.Version}
	refreshed := proof(h, "refresh", refresh, nil)
	expectFixture(t, refreshed, 200)
	var next ci.Tokens
	if e := json.Unmarshal(refreshed.Body.Bytes(), &next); e != nil {
		t.Fatal(e)
	}
	if next.Company != tokens.Company || next.GrantID != tokens.GrantID || next.SessionID != tokens.SessionID || next.Version != tokens.Version+1 || counts() != before {
		t.Fatal("maintenance reprovisioned authority", counts(), before)
	}
	expectFixture(t, proof(h, "refresh", refresh, nil), 200) // exact committed rotation recovery
	// The gate is evaluated after slow request body consumption, not once on
	// handler entry. No attempt can be created after readiness is lost here.
	ready.Store(true)
	before = counts()
	expectFixture(t, proof(h, "start", fresh(), func() { ready.Store(false) }), 409)
	if counts() != before {
		t.Fatal("body-read readiness loss admitted attempt", counts(), before)
	}
	// Pending cancellation and reconciliation stay usable while partial.
	ready.Store(true)
	pending := fresh()
	w := proof(h, "start", pending, nil)
	expectFixture(t, w, 200)
	ready.Store(false)
	cancel := ci.Redemption{AttemptID: pending.AttemptID, OperationID: uuid.NewString(), InstallationProof: pending.InstallationProof, Proof: pending.Proof, LocalSession: pending.LocalSession, LocalOrigin: pending.LocalOrigin, ConnectionRevision: pending.ConnectionRevision}
	expectFixture(t, proof(h, "cancel", cancel, nil), 200)
	expectFixture(t, proof(h, "reconcile", cancel, nil), 200)
}
