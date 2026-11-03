package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

type legacyRepositoryTransport func(*http.Request) (*http.Response, error)

func (f legacyRepositoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func legacyRepositoryProducer(t *testing.T, r *SystemRepository, owner string) *remote.Manager {
	t.Helper()
	cipher, e := providerconfig.NewCipher(ciTestMaster)
	if e != nil {
		t.Fatal(e)
	}
	host := wa.New(r, cipher)
	transport := legacyRepositoryTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/auth/workspace/cancel" {
			var in struct {
				Device string `json:"device_code"`
			}
			if e := json.NewDecoder(request.Body).Decode(&in); e != nil {
				return nil, e
			}
			if e := r.Cancel(ciHost(), wa.Digest(in.Device)); e != nil {
				return nil, e
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"cancelled":true}`))}, nil
		}
		if request.URL.Path != "/auth/workspace/start" {
			return nil, errors.New("fixture only starts actual legacy challenges")
		}
		var in struct {
			Device  string `json:"device_code"`
			Name    string `json:"client_name"`
			Origin  string `json:"client_origin"`
			Proof   string `json:"installation_proof"`
			Replace string `json:"replace_key_id"`
		}
		if e := json.NewDecoder(request.Body).Decode(&in); e != nil {
			return nil, e
		}
		challenge, e := host.Start(ciHost(), in.Device, wa.Challenge{Label: in.Name, Origin: in.Origin, InstallationProof: in.Proof, ReplaceKey: in.Replace})
		if e != nil {
			return nil, e
		}
		raw, _ := json.Marshal(map[string]string{"user_code": challenge.Code})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})
	return remote.New(r, cipher, &http.Client{Transport: transport})
}
func TestLegacyRecoveryAtomicFailureConcurrencyAndMigration(t *testing.T) {
	db := auditDatabase(t, true)
	r := NewSystemRepository(db)
	user, _ := ciUser(t, db)
	ctx := context.Background()
	if e := r.SetAdmin(ctx, "", user.ID, true, true); e != nil {
		t.Fatal(e)
	}
	manager := legacyRepositoryProducer(t, r, user.ID)
	view, e := manager.BeginAuthorization(ctx, user.ID, "https://old.example", "Recovery fixture", "https://local.example", 0)
	if e != nil {
		t.Fatal(e)
	}
	original, e := r.AuthorizationAttempt(ctx, user.ID)
	if e != nil {
		t.Fatal(e)
	}
	input := remote.RecoveryInput{OperationID: uuid.NewString(), AttemptID: view.ID, Version: 0, Revision: 0, Destination: ciTestCentral}
	for _, point := range []string{"archive", "legacy-fence", "global-fence", "active-attempt", "audit"} {
		t.Run(point, func(t *testing.T) {
			fault := errors.New("synthetic legacy transaction interruption")
			r.legacyRecoveryFault = func(step string) error {
				if step == point {
					return fault
				}
				return nil
			}
			_, e := r.QuarantineAuthorization(ctx, user.ID, input)
			r.legacyRecoveryFault = nil
			if !errors.Is(e, fault) {
				t.Fatal("fault not reached", e)
			}
			var count int
			var revision int64
			if e = db.QueryRow(`SELECT count(*) FROM remote_workspace_recovery`).Scan(&count); e != nil || count != 0 {
				t.Fatal("partial archive survived", e)
			}
			if e = db.QueryRow(`SELECT revision FROM company_interface_local_control WHERE singleton=TRUE`).Scan(&revision); e != nil || revision != 0 {
				t.Fatal("partial global fence survived", e)
			}
			if _, e = r.RemoteWorkspace(ctx, user.ID); !errors.Is(e, remote.ErrNotFound) {
				t.Fatal("partial legacy tombstone survived", e)
			}
			kept, e := r.AuthorizationAttempt(ctx, user.ID)
			if e != nil || kept.ID != original.ID || !bytes.Equal(kept.Cipher, original.Cipher) {
				t.Fatal("failed transaction lost proof", e)
			}
		})
	}
	var group sync.WaitGroup
	results := make(chan remote.Recovery, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			v, e := r.QuarantineAuthorization(ctx, user.ID, input)
			results <- v
			errs <- e
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("same operation concurrency", e)
		}
	}
	for v := range results {
		if v.OperationID != input.OperationID || v.Version != 1 || v.Revision != 1 {
			t.Fatal("concurrent quarantine advanced twice")
		}
	}
	for _, changed := range []remote.RecoveryInput{{OperationID: input.OperationID, AttemptID: input.AttemptID, Version: 1, Revision: 0, Destination: input.Destination}, {OperationID: input.OperationID, AttemptID: input.AttemptID, Version: 0, Revision: 0, Destination: "https://other.example"}, {OperationID: input.OperationID, AttemptID: uuid.NewString(), Version: 0, Revision: 0, Destination: input.Destination}} {
		if _, e = r.QuarantineAuthorization(ctx, user.ID, changed); !errors.Is(e, remote.ErrConflict) {
			t.Fatal("changed quarantine binding accepted", e)
		}
	}
	before, e := r.LegacyRecovery(ctx, user.ID, input.OperationID)
	if e != nil {
		t.Fatal(e)
	}
	if e = goose.DownTo(db, "migrations", 90); e != nil {
		t.Fatal(e)
	}
	if e = goose.Up(db, "migrations"); e != nil {
		t.Fatal(e)
	}
	after, e := r.LegacyRecovery(ctx, user.ID, input.OperationID)
	if e != nil || after.State != "pending" || !bytes.Equal(after.Attempt.Cipher, before.Attempt.Cipher) || !bytes.Equal(after.IdentityCipher, before.IdentityCipher) {
		t.Fatal("migration cycle lost custody", e)
	}
	if _, e = db.Exec(`UPDATE remote_workspace_recovery SET origin='https://other.example' WHERE operation_id=$1`, input.OperationID); e == nil {
		t.Fatal("immutable archive target changed")
	}
	if _, e = r.CommitRemoteAuthorization(ctx, remote.Record{Owner: user.ID, Origin: original.Origin, ConnectionRevision: 0}, original); e == nil {
		t.Fatal("archived attempt accepted stale save")
	}
}
func TestLegacyRecoveryCannotInvalidateActiveCompanyContext(t *testing.T) {
	db := auditDatabase(t, true)
	r := NewSystemRepository(db)
	user, _ := ciUser(t, db)
	ctx := context.Background()
	if e := r.SetAdmin(ctx, "", user.ID, true, true); e != nil {
		t.Fatal(e)
	}
	manager := legacyRepositoryProducer(t, r, user.ID)
	pending, e := manager.BeginAuthorization(ctx, user.ID, "https://old.example", "Legacy pending", "https://local.example", 0)
	if e != nil {
		t.Fatal(e)
	}
	lifecycle := ciRepository(t, db)
	in := ciStart(t)
	epoch, e := lifecycle.BeginLocal(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	tokens := ciIssue(t, lifecycle, user, in)
	if e = lifecycle.ActivateLocal(ctx, in, epoch, tokens); e != nil {
		t.Fatal(e)
	}
	before, e := lifecycle.LocalActive(ctx, in.LocalSession)
	if e != nil {
		t.Fatal(e)
	}
	state, e := r.LegacyRecoveryStatus(ctx, user.ID)
	if e != nil || state.RecoveryAllowed {
		t.Fatal("active binding should block quarantine", e)
	}
	input := remote.RecoveryInput{OperationID: uuid.NewString(), AttemptID: pending.ID, Version: 0, Revision: state.Revision, Destination: ciTestCentral}
	if _, e = r.QuarantineAuthorization(ctx, user.ID, input); !errors.Is(e, remote.ErrConflict) {
		t.Fatal("active company context invalidated", e)
	}
	after, e := lifecycle.LocalActive(ctx, in.LocalSession)
	if e != nil || after.Revision != before.Revision || after.Epoch != before.Epoch || after.Tokens.SessionID != before.Tokens.SessionID {
		t.Fatal("company session/fence changed", e)
	}
	kept, e := r.AuthorizationAttempt(ctx, user.ID)
	if e != nil || kept.ID != pending.ID {
		t.Fatal("denied recovery lost legacy proof", e)
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM remote_workspace_recovery`).Scan(&n); e != nil || n != 0 {
		t.Fatal("denied recovery created custody", e)
	}
}
func TestLegacyRecoveryReadSeparatesCurrentAndAttemptRevision(t *testing.T) {
	db := auditDatabase(t, true)
	r := NewSystemRepository(db)
	user, _ := ciUser(t, db)
	ctx := context.Background()
	if e := r.SetAdmin(ctx, "", user.ID, true, true); e != nil {
		t.Fatal(e)
	}
	manager := legacyRepositoryProducer(t, r, user.ID)
	pending, e := manager.BeginAuthorization(ctx, user.ID, "https://old.example", "Historical pending", "https://local.example", 0)
	if e != nil {
		t.Fatal(e)
	}
	lifecycle := ciRepository(t, db)
	if e = lifecycle.AdvanceLocalRevision(ctx, 0); e != nil {
		t.Fatal(e)
	}
	current, e := manager.Authorization(ctx, user.ID)
	if e != nil || current.ConnectionRevision != 0 || current.CurrentRevision != 1 || !current.RecoveryAllowed {
		t.Fatal("read does not expose current revision separately", e)
	}
	recovered, e := manager.QuarantineAuthorization(ctx, user.ID, remote.RecoveryInput{OperationID: uuid.NewString(), AttemptID: pending.ID, Version: 0, Revision: current.CurrentRevision, Destination: ciTestCentral})
	if e != nil || recovered.Revision != 2 || recovered.Attempt.ConnectionRevision != 0 {
		t.Fatal("historical proof cannot recover using current read state", e)
	}
	raw, _ := json.Marshal(recovered)
	if strings.Contains(string(raw), "Cipher") || strings.Contains(string(raw), "proof") || strings.Contains(string(raw), "saved") {
		t.Fatal("recovery exposed private custody")
	}
}

func TestLegacyRecoveryPaginationFindsOldPendingAfterRestart(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewSystemRepository(db)
	user, _ := ciUser(t, db)
	ctx := context.Background()
	if e := repo.SetAdmin(ctx, "", user.ID, true, true); e != nil {
		t.Fatal(e)
	}
	manager := legacyRepositoryProducer(t, repo, user.ID)
	var oldest string
	for i := int64(0); i < 102; i++ {
		view, e := manager.BeginAuthorization(ctx, user.ID, "https://old.example", "Paged recovery", "https://local.example", i)
		if e != nil {
			t.Fatal(e)
		}
		archived, e := manager.QuarantineAuthorization(ctx, user.ID, remote.RecoveryInput{OperationID: uuid.NewString(), AttemptID: view.ID, Version: i, Revision: i, Destination: ciTestCentral})
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			oldest = archived.OperationID
		} else if e = manager.CleanupLegacyRecovery(ctx, user.ID, archived.OperationID); e != nil {
			t.Fatal(e)
		}
	}
	repo = NewSystemRepository(db)
	manager = legacyRepositoryProducer(t, repo, user.ID)
	cursor := ""
	seen := map[string]bool{}
	found := false
	pages := 0
	for {
		page, e := manager.RecoveryPage(ctx, user.ID, cursor)
		if e != nil || len(page.Items) > 100 {
			t.Fatal("bounded recovery discovery", e)
		}
		pages++
		for _, item := range page.Items {
			if seen[item.OperationID] {
				t.Fatal("pagination duplicated archive")
			}
			seen[item.OperationID] = true
			if item.OperationID == oldest {
				if item.State != "pending" {
					t.Fatal("old uncertainty was settled")
				}
				found = true
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if !found || len(seen) != 102 || pages != 2 {
		t.Fatal("old pending custody undiscoverable", found, len(seen), pages)
	}
	if e := manager.CleanupLegacyRecovery(ctx, user.ID, oldest); e != nil {
		t.Fatal("discovered original proof cannot clean up", e)
	}
	if _, e := manager.RecoveryPage(ctx, user.ID, "not-a-cursor"); e == nil {
		t.Fatal("malformed cursor accepted")
	}
}

func TestLegacyRecoveryCommittedReplaySurvivesLaterCompanyBinding(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewSystemRepository(db)
	user, _ := ciUser(t, db)
	ctx := context.Background()
	if e := repo.SetAdmin(ctx, "", user.ID, true, true); e != nil {
		t.Fatal(e)
	}
	manager := legacyRepositoryProducer(t, repo, user.ID)
	pending, e := manager.BeginAuthorization(ctx, user.ID, "https://old.example", "Replay fixture", "https://local.example", 0)
	if e != nil {
		t.Fatal(e)
	}
	input := remote.RecoveryInput{OperationID: uuid.NewString(), AttemptID: pending.ID, Destination: ciTestCentral}
	first, e := repo.QuarantineAuthorization(ctx, user.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	lifecycle := ciRepository(t, db)
	in := ciStart(t)
	in.ConnectionRevision = first.Revision
	epoch, e := lifecycle.BeginLocal(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	tokens := ciIssue(t, lifecycle, user, in)
	if e = lifecycle.ActivateLocal(ctx, in, epoch, tokens); e != nil {
		t.Fatal(e)
	}
	before, e := lifecycle.LocalActive(ctx, in.LocalSession)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := repo.QuarantineAuthorization(ctx, user.ID, input)
	if e != nil || replay.OperationID != first.OperationID || replay.Version != first.Version || replay.Revision != first.Revision {
		t.Fatal("committed receipt lost after binding", e)
	}
	after, e := lifecycle.LocalActive(ctx, in.LocalSession)
	if e != nil || after.Revision != before.Revision || after.Tokens.SessionID != before.Tokens.SessionID {
		t.Fatal("receipt replay changed active context", e)
	}
	input.OperationID = uuid.NewString()
	if _, e = repo.QuarantineAuthorization(ctx, user.ID, input); !errors.Is(e, remote.ErrConflict) {
		t.Fatal("new quarantine entered active company binding", e)
	}
}

func TestLegacyAuthorizationSaveAndReceiptRollback(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewSystemRepository(db)
	user, _ := ciUser(t, db)
	ctx := context.Background()
	if e := repo.SetAdmin(ctx, "", user.ID, true, true); e != nil {
		t.Fatal(e)
	}
	manager := legacyRepositoryProducer(t, repo, user.ID)
	view, e := manager.BeginAuthorization(ctx, user.ID, "https://old.example", "Save receipt fixture", "https://local.example", 0)
	if e != nil {
		t.Fatal(e)
	}
	pending, e := repo.AuthorizationAttempt(ctx, user.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.Approve(identity.WithUser(ciHost(), user), user.ID, view.Code, wa.Approval{Consent: true}); e != nil {
		t.Fatal(e)
	}
	proof, e := manager.Cipher.Decrypt(pending.Cipher)
	if e != nil {
		t.Fatal(e)
	}
	challenge, token, e := wa.New(repo, manager.Cipher).Poll(ciHost(), string(proof))
	if e != nil {
		t.Fatal(e)
	}
	encrypted, e := manager.Cipher.Encrypt([]byte(token))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	next := remote.Record{Owner: user.ID, Active: true, Origin: pending.Origin, OrganizationID: challenge.Company, OrganizationName: "Fixture", UserID: challenge.Actor, KeyID: challenge.KeyID, TokenHash: apikey.Hash(token), Ciphertext: encrypted, VerifiedAt: &now, Provisioning: "dedicated"}
	for _, point := range []string{"saved-record", "saved-audit", "saved-settings", "authorization-save", "authorization-receipt"} {
		t.Run(point, func(t *testing.T) {
			fault := errors.New("synthetic save interruption")
			repo.legacyRecoveryFault = func(step string) error {
				if step == point {
					return fault
				}
				return nil
			}
			_, e := repo.CommitRemoteAuthorization(ctx, next, pending)
			repo.legacyRecoveryFault = nil
			if !errors.Is(e, fault) {
				t.Fatal("save fault not exercised", e)
			}
			if _, e = repo.RemoteWorkspace(ctx, user.ID); !errors.Is(e, remote.ErrNotFound) {
				t.Fatal("partial verified save survived", e)
			}
			var receipts int
			if e = db.QueryRow(`SELECT count(*) FROM remote_workspace_authorization_receipts`).Scan(&receipts); e != nil || receipts != 0 {
				t.Fatal("partial receipt survived", e)
			}
			kept, e := repo.AuthorizationAttempt(ctx, user.ID)
			if e != nil || !bytes.Equal(kept.Cipher, pending.Cipher) {
				t.Fatal("save failure lost issued-key recovery proof", e)
			}
		})
	}
	saved, e := repo.CommitRemoteAuthorization(ctx, next, pending)
	if e != nil {
		t.Fatal(e)
	}
	// Simulate process loss after committed save/receipt and before Clear, without
	// modifying persisted state. Quarantine preserves that exact saved key.
	archive, e := repo.QuarantineAuthorization(ctx, user.ID, remote.RecoveryInput{OperationID: uuid.NewString(), AttemptID: pending.ID, Version: saved.Version, Revision: 0, Destination: ciTestCentral})
	if e != nil {
		t.Fatal(e)
	}
	if archive.Saved.KeyID != challenge.KeyID || !bytes.Equal(archive.Saved.Ciphertext, encrypted) {
		t.Fatal("committed-before-clear custody mismatched")
	}
	if _, _, e = repo.FinishSavedAuthorization(ctx, user.ID, pending.ID); e == nil {
		t.Fatal("archived receipt became activation success")
	}
	if e = manager.CleanupLegacyRecovery(ctx, user.ID, archive.OperationID); e != nil {
		t.Fatal(e)
	}
	var revoked bool
	if e = db.QueryRow(`SELECT revoked_at IS NOT NULL FROM user_api_keys WHERE id=$1`, challenge.KeyID).Scan(&revoked); e != nil || !revoked {
		t.Fatal("terminal device cleanup did not revoke its exact saved key", e)
	}
}
