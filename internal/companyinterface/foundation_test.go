package companyinterface

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type authorityFixture struct {
	grant                         Grant
	session                       Session
	roles                         map[string]string
	sessionReads, membershipReads int
}

func (f *authorityFixture) Installation(_ context.Context, h [32]byte) (Grant, error) {
	if h != sha256.Sum256([]byte(strings.Repeat("i", 32))) {
		return Grant{}, ErrDenied
	}
	return f.grant, nil
}
func (f *authorityFixture) Session(_ context.Context, h [32]byte) (Session, error) {
	f.sessionReads++
	if h != sha256.Sum256([]byte(strings.Repeat("s", 32))) {
		return Session{}, ErrDenied
	}
	return f.session, nil
}
func (f *authorityFixture) Membership(_ context.Context, company, actor string) (string, error) {
	f.membershipReads++
	if company != f.grant.Company {
		return "", ErrDenied
	}
	return f.roles[actor], nil
}

type keyFixture struct {
	identity          KeyIdentity
	err               error
	calls             int
	original, company string
}

func (k *keyFixture) AuthenticateCompanyKey(_ context.Context, key, company string) (KeyIdentity, error) {
	k.calls++
	k.original = key
	k.company = company
	return k.identity, k.err
}
func admissionFixture() (*authorityFixture, Credentials, time.Time) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	g := Grant{ID: uuid.NewString(), InstallationHash: sha256.Sum256([]byte("installation proof")), Company: uuid.NewString(), CentralOrigin: "https://central.example", LocalOrigin: "https://local.example", Purpose: Purpose, Version: Version, Epoch: 1}
	s := Session{ID: uuid.NewString(), GrantID: g.ID, Company: g.Company, Actor: uuid.NewString(), LocalSessionHash: sha256.Sum256([]byte("local browser session")), GrantEpoch: 1, Epoch: 1, Version: 1, ExpiresAt: now.Add(time.Hour)}
	f := &authorityFixture{grant: g, session: s, roles: map[string]string{s.Actor: "member"}}
	return f, Credentials{Installation: strings.Repeat("i", 32), Session: strings.Repeat("s", 32), LocalSessionHash: s.LocalSessionHash}, now
}

func TestProtocolRequiresEveryEssentialCapabilityWithoutDowngrade(t *testing.T) {
	p := Protocol{Purpose: Purpose, Version: Version, Capabilities: RequiredCapabilities()}
	if err := Negotiate(p); err != nil {
		t.Fatal(err)
	}
	for i := range p.Capabilities {
		missing := p
		missing.Capabilities = append(append([]string{}, p.Capabilities[:i]...), p.Capabilities[i+1:]...)
		if !errors.Is(Negotiate(missing), ErrProtocol) {
			t.Fatalf("missing %s accepted", p.Capabilities[i])
		}
	}
	for _, bad := range []Protocol{{Purpose: "generation", Version: 1, Capabilities: p.Capabilities}, {Purpose: Purpose, Version: 2, Capabilities: p.Capabilities}, {Purpose: Purpose, Version: 1, Capabilities: append(p.Capabilities, p.Capabilities[0])}} {
		if Negotiate(bad) == nil {
			t.Fatal("incompatible protocol accepted")
		}
	}
}

func TestPermanentGrantDoesNotExtendUserSessionOrExplicitKey(t *testing.T) {
	f, c, now := admissionFixture()
	gate := Gate{Authority: f, CentralOrigin: f.grant.CentralOrigin}
	for _, d := range []time.Duration{31 * 24 * time.Hour, 366 * 24 * time.Hour} {
		if f.grant.Validate(now.Add(d)) != nil {
			t.Fatal("permanent grant expired")
		}
	}
	if _, err := gate.AuthenticateBrowser(context.Background(), c, now.Add(31*24*time.Hour)); err == nil {
		t.Fatal("expired session accepted")
	}
	expires := now
	f.grant.ExpiresAt = &expires
	if f.grant.Validate(now) == nil {
		t.Fatal("ordinary explicit expiry ignored")
	}
}

func TestAdmissionRechecksMembershipAndRejectsInstallationOnly(t *testing.T) {
	f, c, now := admissionFixture()
	gate := Gate{Authority: f, CentralOrigin: f.grant.CentralOrigin}
	p, err := gate.AuthenticateBrowser(context.Background(), c, now)
	if err != nil || p.Actor != f.session.Actor || p.Role != "member" || p.Kind != CentralSession {
		t.Fatal(p, err)
	}
	f.roles[p.Actor] = "admin"
	p, err = gate.AuthenticateBrowser(context.Background(), c, now)
	if err != nil || p.Role != "admin" || f.membershipReads != 2 {
		t.Fatal("stale role", p, err)
	}
	delete(f.roles, p.Actor)
	if _, err = gate.AuthenticateBrowser(context.Background(), c, now); err == nil {
		t.Fatal("removed member accepted")
	}
	f.roles[p.Actor] = "system_admin"
	if _, err = gate.AuthenticateBrowser(context.Background(), c, now); err == nil {
		t.Fatal("global role accepted as company membership")
	}
	c.Session = ""
	if _, err = gate.AuthenticateBrowser(context.Background(), c, now); err == nil {
		t.Fatal("installation became actor")
	}
}

func TestAdmissionRejectsSessionSubstitutionRevocationAndOldEpoch(t *testing.T) {
	cases := map[string]func(*authorityFixture, *Credentials){
		"company":         func(f *authorityFixture, _ *Credentials) { f.session.Company = uuid.NewString() },
		"grant":           func(f *authorityFixture, _ *Credentials) { f.session.GrantID = uuid.NewString() },
		"local session":   func(_ *authorityFixture, c *Credentials) { c.LocalSessionHash = sha256.Sum256([]byte("other")) },
		"grant epoch":     func(f *authorityFixture, _ *Credentials) { f.grant.Epoch++ },
		"grant revoked":   func(f *authorityFixture, _ *Credentials) { f.grant.Revoked = true },
		"session revoked": func(f *authorityFixture, _ *Credentials) { f.session.Revoked = true },
		"old purpose":     func(f *authorityFixture, _ *Credentials) { f.grant.Purpose = "generation" },
		"origin path":     func(f *authorityFixture, _ *Credentials) { f.grant.LocalOrigin += "/other" },
		"origin alias":    func(f *authorityFixture, _ *Credentials) { f.grant.CentralOrigin = "https://CENTRAL.example" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f, c, now := admissionFixture()
			change(f, &c)
			if _, err := (Gate{Authority: f, CentralOrigin: f.grant.CentralOrigin}).AuthenticateBrowser(context.Background(), c, now); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

type recipientFixture struct {
	err                     error
	company, actor, subject string
}

func (f *recipientFixture) AuthorizeKeyRecipient(_ context.Context, company, actor, subject string) error {
	f.company, f.actor, f.subject = company, actor, subject
	return f.err
}

func TestOriginalCentralKeyHasNoSessionFallbackAndUsesActualSubject(t *testing.T) {
	f, c, now := admissionFixture()
	subject := uuid.NewString()
	f.roles[subject] = "member"
	f.roles[f.session.Actor] = "supermanager"
	k := &keyFixture{identity: KeyIdentity{ID: uuid.NewString(), Company: f.grant.Company, Subject: subject, Scopes: []string{"generation"}}}
	policy := &recipientFixture{}
	gate := Gate{Authority: f, Keys: k, Recipients: policy, CentralOrigin: f.grant.CentralOrigin}
	c.KeyPresent = true
	c.Key = "original-central-key-fixture"
	p, err := gate.AuthenticateBrowser(context.Background(), c, now)
	if err != nil || p.Actor != subject || p.Recipient != subject || p.Role != "member" || p.BrowserActor != f.session.Actor || p.SessionID != f.session.ID || p.Kind != CentralKey || k.original != c.Key || k.company != f.grant.Company || f.sessionReads != 1 || policy.actor != f.session.Actor || policy.subject != subject {
		t.Fatal(p, err)
	}
	for _, bad := range []string{"", "short", "invalid-but-long-key-fixture"} {
		c.Key = bad
		k.err = ErrDenied
		if _, err = gate.AuthenticateBrowser(context.Background(), c, now); err == nil {
			t.Fatal("explicit key fell back", bad)
		}
	}
	c.Key = "original-central-key-fixture"
	k.err = nil
	k.identity.Company = uuid.NewString()
	if _, err = gate.AuthenticateBrowser(context.Background(), c, now); err == nil {
		t.Fatal("foreign company key accepted")
	}
	k.identity.Company = f.grant.Company
	policy.err = ErrDenied
	if _, err = gate.AuthenticateBrowser(context.Background(), c, now); err == nil {
		t.Fatal("recipient policy ignored")
	}
	gate.Recipients = nil
	if _, err = gate.AuthenticateBrowser(context.Background(), c, now); err == nil {
		t.Fatal("cross-recipient key accepted without policy")
	}
	gate.Recipients = policy
	policy.err = nil
	c.Session = "invalid-browser-session-fixture-long"
	if _, err = gate.AuthenticateBrowser(context.Background(), c, now); err == nil {
		t.Fatal("valid key replaced invalid browser session")
	}
	c.Session = ""
	c.LocalSessionHash = [32]byte{}
	reads := f.sessionReads
	p, err = gate.AuthenticateSDK(context.Background(), c, now)
	if err != nil || p.Actor != subject || p.Recipient != subject || p.Role != "member" || p.BrowserActor != "" || p.SessionID != "" || f.sessionReads != reads {
		t.Fatal("SDK did not use actual key subject", p, err)
	}
	delete(f.roles, subject)
	if _, err = gate.AuthenticateSDK(context.Background(), c, now); err == nil {
		t.Fatal("removed key subject accepted")
	}
	c.KeyPresent = false
	if _, err = gate.AuthenticateSDK(context.Background(), c, now); err == nil {
		t.Fatal("SDK accepted installation-only credential")
	}
}

func TestTrustedCentralOriginIsRequired(t *testing.T) {
	f, c, now := admissionFixture()
	for _, origin := range []string{"", "https://other.example"} {
		if _, err := (Gate{Authority: f, CentralOrigin: origin}).AuthenticateBrowser(context.Background(), c, now); err == nil {
			t.Fatal("untrusted central origin accepted")
		}
	}
}

func TestReadContractsDenyUnknownMethodsTenantFieldsAndBodies(t *testing.T) {
	p := Principal{Kind: CentralSession}
	id := uuid.NewString()
	content := "/v1/system/template-references/" + id + "/content"
	for _, path := range []string{"/v1/me", "/v1/providers", "/v1/models", "/v1/system/template-references", content} {
		for _, method := range []string{"GET", "HEAD"} {
			if _, err := RequestContract(method, path, "", nil, p); err != nil {
				t.Fatal(method, path, err)
			}
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE"} {
			if Classify(method, path).Class != Disallowed {
				t.Fatal(method, path)
			}
		}
		for _, q := range []string{"organization_id=" + uuid.NewString(), "actor_id=x", "recipient_id=x", "limit=1&limit=2", "%6cimit=1", "unknown=1", ";", "&"} {
			if _, err := RequestContract("GET", path, q, nil, p); err == nil {
				t.Fatal("query accepted", q)
			}
		}
		if _, err := RequestContract("HEAD", path, "", []byte("{}"), p); err == nil {
			t.Fatal("read body accepted")
		}
	}
	for _, path := range []string{"/v1/system/templates/library", "/v1/system/providers", "/v1/system/storage", "/v1/system/templates", "/v1/organizations/" + id, "/v1/models/vendor/model", "/v1/models/../providers", "/v1/%70roviders", "/v1//providers", content + "/", strings.Replace(content, id, "%2e%2e", 1)} {
		if Classify("GET", path).Class != Disallowed {
			t.Fatal(path)
		}
	}
	if Classify("GET", "/auth/session").Class != LocalControl {
		t.Fatal("local control misclassified")
	}
	if _, err := RequestContract("GET", "/auth/session", "", nil, p); err == nil {
		t.Fatal("local route forwarded")
	}
	p.Kind = CentralKey
	if _, err := RequestContract("GET", "/v1/providers", "", nil, p); err == nil {
		t.Fatal("missing key scope accepted")
	}
	p.Scopes = []string{"system:manage"}
	if _, err := RequestContract("GET", "/v1/providers", "", nil, p); err == nil {
		t.Fatal("global scope widened generation")
	}
	p.Scopes = []string{"generation"}
	if _, err := RequestContract("HEAD", content, "", nil, p); err != nil {
		t.Fatal(err)
	}
	if _, err := RequestContract("GET", "/v1/me", "", nil, p); err == nil {
		t.Fatal("key became browser session")
	}
}

func TestForwardHeadersStripIdentityAndHopHeadersPreserveSingleRange(t *testing.T) {
	c := Classify("GET", "/v1/system/template-references/"+uuid.NewString()+"/content")
	in := http.Header{"Authorization": {"Bearer attacker"}, "Cookie": {"session=attacker"}, "X-Api-Key": {"attacker"}, "X-Company-Interface-Session": {"attacker"}, "X-Forwarded-User": {"attacker"}, "X-Forwarded-Host": {"evil.example"}, "Host": {"evil.example"}, "Range": {"bytes=3-9"}, "If-Range": {"\"etag\""}, "Accept": {"video/mp4"}, "Connection": {"Accept"}}
	out, err := ForwardHeaders(c, in)
	if err != nil || len(out) != 2 || out.Get("Range") != "bytes=3-9" || out.Get("If-Range") != "\"etag\"" {
		t.Fatal(out, err)
	}
	for _, value := range []string{"bytes=0-1,2-3", "bytes=+1-3", "bytes=9-2", "bytes=-0", "bytes=-", "bytes=1-999999999999999999999", "bytes=1-2\r\nCookie: x"} {
		if _, err := ForwardHeaders(c, http.Header{"Range": {value}}); err == nil {
			t.Fatal(value)
		}
	}
	for _, value := range []string{"bytes=0-", "bytes=-10", "bytes=0-0"} {
		if _, err := ForwardHeaders(c, http.Header{"Range": {value}}); err != nil {
			t.Fatal(value, err)
		}
	}
	if _, err := ForwardHeaders(c, http.Header{"Range": {"bytes=1-2"}, "range": {"bytes=3-4"}}); err == nil {
		t.Fatal("duplicate differently cased header accepted")
	}
	if _, err := ForwardHeaders(c, http.Header{"Range": {"bytes=1-2", "bytes=3-4"}}); err == nil {
		t.Fatal("duplicate header accepted")
	}
}
