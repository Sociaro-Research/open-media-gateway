package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

const legacyWrongOrigin = "https://wrong-central.fixture.test"
const legacyLocalOrigin = "https://legacy-local.fixture.test"

type legacyRecoveryWire struct {
	pauseOrigin, pausePath string
	entered, resume        chan struct{}
	loseCancel             bool
	mu                     sync.Mutex
	hosts                  map[string]http.Handler
	blocked                map[string]bool
	losePoll               string
	calls                  []string
}

func (w *legacyRecoveryWire) RoundTrip(r *http.Request) (*http.Response, error) {
	origin := r.URL.Scheme + "://" + r.URL.Host
	w.mu.Lock()
	w.calls = append(w.calls, origin+r.URL.Path)
	blocked := w.blocked[origin]
	handler := w.hosts[origin]
	lose := w.losePoll == origin && r.URL.Path == "/auth/workspace/poll"
	if lose {
		w.losePoll = ""
	}
	pause := w.pauseOrigin == origin && w.pausePath == r.URL.Path
	entered, resume := w.entered, w.resume
	if pause {
		w.pauseOrigin = ""
		w.pausePath = ""
	}
	loseCancel := w.loseCancel && r.URL.Path == "/auth/workspace/cancel"
	if loseCancel {
		w.loseCancel = false
	}
	w.mu.Unlock()
	if blocked || handler == nil {
		return nil, errors.New("synthetic original host unavailable")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	result := response.Result()
	if pause {
		close(entered)
		<-resume
	}
	if (lose || loseCancel) && result.StatusCode == 200 {
		result.Body.Close()
		result.Body = failedWorkspaceBody{}
	}
	return result, nil
}
func (w *legacyRecoveryWire) block(origin string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blocked[origin] = true
}

type legacyRecoveryFixture struct {
	localDB, oldDB, correctDB        *sql.DB
	repo                             *postgres.SystemRepository
	owner                            identity.User
	cookie, oldCookie, correctCookie *http.Cookie
	cipher                           *providerconfig.Cipher
	auth                             *auth.Manager
	wire                             *legacyRecoveryWire
	manager                          *remote.Manager
	local, old, correct              http.Handler
}

func legacyRecoverySetup(t *testing.T) *legacyRecoveryFixture {
	t.Helper()
	ctx := context.Background()
	f := &legacyRecoveryFixture{localDB: workspaceDatabase(t), oldDB: workspaceDatabase(t), correctDB: workspaceDatabase(t)}
	f.cipher, _ = providerconfig.NewCipher(strings.Repeat("r", 32))
	makeIdentity := func(db *sql.DB, origin string, admin bool) (identity.User, *auth.Manager, *http.Cookie) {
		people := postgres.NewAuthRepository(db)
		user, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Recovery fixture", "")
		if e != nil {
			t.Fatal(e)
		}
		if admin {
			if e = postgres.NewSystemRepository(db).SetAdmin(ctx, "", user.ID, true, true); e != nil {
				t.Fatal(e)
			}
		}
		manager, e := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: origin})
		if e != nil {
			t.Fatal(e)
		}
		token, _ := wa.DeviceCode()
		if e = people.CreateSession(ctx, wa.Digest(token), user.ID, time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
		return user, manager, &http.Cookie{Name: "media_gateway_session", Value: token}
	}
	f.owner, f.auth, f.cookie = makeIdentity(f.localDB, legacyLocalOrigin, true)
	_, oldAuth, oldCookie := makeIdentity(f.oldDB, installation.DefaultCentralOrigin, false)
	f.oldCookie = oldCookie
	_, correctAuth, correctCookie := makeIdentity(f.correctDB, installation.DefaultCentralOrigin, false)
	f.correctCookie = correctCookie
	host := func(db *sql.DB, a *auth.Manager) http.Handler {
		repo := postgres.NewSystemRepository(db)
		gateway := NewGateway(Dependencies{Service: &accountService{}, Auth: a, System: repo, Keys: postgres.NewAPIKeyRepository(db), WorkspaceAuthorization: wa.New(repo, f.cipher)})
		handler, e := WithPublicAPI(gateway, "", installation.DefaultCentralOrigin, "")
		if e != nil {
			t.Fatal(e)
		}
		return handler
	}
	f.old = host(f.oldDB, oldAuth)
	f.correct = host(f.correctDB, correctAuth)
	f.wire = &legacyRecoveryWire{hosts: map[string]http.Handler{legacyWrongOrigin: f.old, installation.DefaultCentralOrigin: f.correct}, blocked: map[string]bool{}}
	f.restart(t)
	return f
}
func (f *legacyRecoveryFixture) restart(t *testing.T) {
	t.Helper()
	f.repo = postgres.NewSystemRepository(f.localDB)
	f.manager = remote.New(f.repo, f.cipher, &http.Client{Transport: f.wire, Timeout: 5 * time.Second})
	gateway := NewGateway(Dependencies{Service: &accountService{}, Auth: f.auth, System: f.repo, Keys: postgres.NewAPIKeyRepository(f.localDB), RemoteWorkspace: f.manager})
	var e error
	f.local, e = WithPublicAPI(gateway, "", legacyLocalOrigin, "")
	if e != nil {
		t.Fatal(e)
	}
}
func legacyRecoveryCall(t *testing.T, h http.Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(method, legacyLocalOrigin+path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	result := httptest.NewRecorder()
	h.ServeHTTP(result, request)
	return result
}
func (f *legacyRecoveryFixture) approve(t *testing.T, view remote.AuthorizationView, old bool) {
	t.Helper()
	handler, cookie := f.correct, f.correctCookie
	if old {
		handler, cookie = f.old, f.oldCookie
	}
	response := legacyRecoveryCall(t, handler, "POST", "/v1/workspace-authorizations/approve", map[string]any{"user_code": view.Code, "consent": true}, cookie)
	if response.Code != 200 {
		t.Fatalf("actual central approval=%d: %s", response.Code, response.Body)
	}
}

func TestLegacyRecoveryRealProducerCanContinueAfterOriginalHostFailure(t *testing.T) {
	for _, scenario := range []string{"wrong origin pending", "first start unreachable", "issued reply lost", "saved old host unreachable", "saved commit before clear", "valid saved connection coexists"} {
		t.Run(scenario, func(t *testing.T) {
			f := legacyRecoverySetup(t)
			ctx := context.Background()
			version := int64(0)
			var saved remote.Record
			if scenario == "valid saved connection coexists" || scenario == "saved old host unreachable" {
				origin := installation.DefaultCentralOrigin
				old := false
				if scenario == "saved old host unreachable" {
					origin = legacyWrongOrigin
					old = true
				}
				first, e := f.manager.BeginAuthorization(ctx, f.owner.ID, origin, "Saved connection", legacyLocalOrigin, 0)
				if e != nil {
					t.Fatal(e)
				}
				f.approve(t, first, old)
				saved, e = f.manager.PollAuthorization(ctx, f.owner.ID, first.ID)
				if e != nil {
					t.Fatal(e)
				}
				version = saved.Version
			}
			if scenario == "first start unreachable" {
				f.wire.block(legacyWrongOrigin)
			}
			view, e := f.manager.BeginAuthorization(ctx, f.owner.ID, legacyWrongOrigin, "Original attempt", legacyLocalOrigin, version)
			if scenario == "valid saved connection coexists" || scenario == "first start unreachable" {
				want := remote.ErrConflict
				if scenario == "first start unreachable" {
					want = remote.ErrUnavailable
				}
				if !errors.Is(e, want) {
					t.Fatal("real producer did not retain failed initial state", e)
				}
				view, e = f.manager.Authorization(ctx, f.owner.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			original, e := f.repo.AuthorizationAttempt(ctx, f.owner.ID)
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "saved commit before clear" {
				f.approve(t, view, true)
				wrapper := &retainCompletedAttemptOnce{SystemRepository: f.repo}
				wrapper.fail.Store(true)
				f.manager = remote.New(wrapper, f.cipher, &http.Client{Transport: f.wire, Timeout: 5 * time.Second})
				if _, e = f.manager.PollAuthorization(ctx, f.owner.ID, view.ID); !errors.Is(e, remote.ErrUnavailable) {
					t.Fatal("save/clear interruption not reached", e)
				}
				saved, e = f.repo.RemoteWorkspace(ctx, f.owner.ID)
				if e != nil || !saved.Active {
					t.Fatal("save did not commit before clear failure", e)
				}
				version = saved.Version
			}
			if scenario == "issued reply lost" {
				f.approve(t, view, true)
				f.wire.losePoll = legacyWrongOrigin
				if _, e = f.manager.PollAuthorization(ctx, f.owner.ID, view.ID); !errors.Is(e, remote.ErrUnavailable) {
					t.Fatal("lost reply was not uncertain", e)
				}
				var keys int
				if e = f.oldDB.QueryRow(`SELECT count(*) FROM user_api_keys`).Scan(&keys); e != nil || keys != 1 {
					t.Fatal("actual issued key missing", e)
				}
			}
			f.wire.block(legacyWrongOrigin)
			if e = f.manager.CancelAuthorization(ctx, f.owner.ID, view.ID); !errors.Is(e, remote.ErrUnavailable) {
				t.Fatal("unreachable cancel was confirmed", e)
			}
			f.restart(t)
			retained, e := f.repo.AuthorizationAttempt(ctx, f.owner.ID)
			if e != nil || retained.ID != original.ID || !bytes.Equal(retained.Cipher, original.Cipher) || retained.Origin != legacyWrongOrigin {
				t.Fatal("restart lost original proof", e)
			}
			blocked := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization", map[string]any{"version": version, "consent": true}, f.cookie)
			if blocked.Code != 409 {
				t.Fatalf("expected original destination conflict, got%d", blocked.Code)
			}
			stateResponse := legacyRecoveryCall(t, f.local, "GET", "/v1/system/remote-workspace/authorization/recovery", nil, f.cookie)
			var state remote.RecoveryStatus
			if stateResponse.Code != 200 || json.Unmarshal(stateResponse.Body.Bytes(), &state) != nil || !state.RecoveryAllowed {
				t.Fatal("current recovery revision unavailable", stateResponse.Code)
			}
			input := map[string]any{"id": view.ID, "operation_id": uuid.NewString(), "version": version, "connection_revision": state.Revision, "consent": true}
			recovery := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/recover", input, f.cookie)
			if recovery.Code != 200 {
				t.Fatalf("supported recovery after %s: status%d %s", scenario, recovery.Code, recovery.Body)
			}
			var result remote.Recovery
			if json.Unmarshal(recovery.Body.Bytes(), &result) != nil || result.Version != version+1 || result.Revision != 1 || result.State != "pending" {
				t.Fatal("wrong recovery transition")
			}
			for _, forbidden := range []string{"cipher", "token", "proof", "saved_snapshot", "organization_id", "client_origin"} {
				if strings.Contains(recovery.Body.String(), forbidden) {
					t.Fatal("private custody leaked", forbidden)
				}
			}
			archived, e := f.repo.LegacyRecovery(ctx, f.owner.ID, result.OperationID)
			if e != nil || archived.Attempt.ID != original.ID || !bytes.Equal(archived.Attempt.Cipher, original.Cipher) || archived.Attempt.Origin != legacyWrongOrigin {
				t.Fatal("archive does not preserve original proof", e)
			}
			proof, e := f.cipher.Decrypt(archived.Attempt.Cipher)
			if e != nil || wa.UserCode(string(proof)) != view.Code {
				t.Fatal("archived proof no longer decrypts", e)
			}
			if saved.Active {
				current, e := f.repo.RemoteWorkspace(ctx, f.owner.ID)
				if e != nil || !current.Active || current.Version != version+1 || current.Origin != saved.Origin || current.OrganizationID != saved.OrganizationID || !bytes.Equal(current.Ciphertext, saved.Ciphertext) {
					t.Fatal("quarantine replaced valid saved connection", e)
				}
				if !archived.SavedPresent || archived.Saved.Origin != saved.Origin || archived.Saved.KeyID != saved.KeyID || !bytes.Equal(archived.Saved.Ciphertext, saved.Ciphertext) {
					t.Fatal("saved credential snapshot crossed pending destination")
				}
				if _, e = f.repo.SaveRemoteWorkspace(ctx, saved, version-1); !errors.Is(e, remote.ErrConflict) {
					t.Fatal("old version+1 shortcut crossed quarantine", e)
				}
			}
			// A lost quarantine reply is recovered by the same exact operation, including
			// after manager/repository restart. It does not re-run or advance the fence.
			f.restart(t)
			replay := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/recover", input, f.cookie)
			if replay.Code != 200 || replay.Body.String() != recovery.Body.String() {
				t.Fatal("quarantine replay changed transition", replay.Code)
			}
			cleanup := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/recovery/cleanup", map[string]string{"id": result.OperationID}, f.cookie)
			if cleanup.Code != 502 {
				t.Fatal("unreachable archive cleanup was confirmed", cleanup.Code)
			}
			if scenario == "saved old host unreachable" || scenario == "saved commit before clear" {
				stillBlocked := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization", map[string]any{"version": result.Version, "consent": true}, f.cookie)
				if stillBlocked.Code != 409 {
					t.Fatal("quarantine implicitly disconnected old authority")
				}
				disconnected := legacyRecoveryCall(t, f.local, "DELETE", "/v1/system/remote-workspace", map[string]any{"version": result.Version}, f.cookie)
				if disconnected.Code != 200 || strings.Contains(disconnected.Body.String(), "revoked") {
					t.Fatal("explicit local disconnect claimed revocation", disconnected.Code)
				}
				var value remote.Record
				json.Unmarshal(disconnected.Body.Bytes(), &value)
				result.Version = value.Version
				custody, e := f.repo.LegacyRecovery(ctx, f.owner.ID, result.OperationID)
				if e != nil || custody.State != "pending" || !bytes.Equal(custody.Saved.Ciphertext, saved.Ciphertext) {
					t.Fatal("disconnect erased unresolved original credential", e)
				}
			}
			begun := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization", map[string]any{"version": result.Version, "consent": true}, f.cookie)
			if begun.Code != 200 {
				t.Fatalf("correct-origin successor failed%d: %s", begun.Code, begun.Body)
			}
			var successor remote.AuthorizationView
			json.Unmarshal(begun.Body.Bytes(), &successor)
			if successor.ID == view.ID || successor.Origin != installation.DefaultCentralOrigin || successor.ConnectionRevision != 1 {
				t.Fatal("successor reused archived attempt")
			}
			f.approve(t, successor, false)
			connected := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/poll", map[string]string{"id": successor.ID}, f.cookie)
			if connected.Code != 200 {
				t.Fatalf("successor activation failed%d: %s", connected.Code, connected.Body)
			}
			active, e := f.repo.RemoteWorkspace(ctx, f.owner.ID)
			if e != nil || !active.Active || active.Origin != installation.DefaultCentralOrigin {
				t.Fatal("successor not authoritative", e)
			}
			if scenario == "valid saved connection coexists" && active.OrganizationID != saved.OrganizationID {
				t.Fatal("same-origin successor reprovisioned company")
			}
			if _, e = f.manager.PollAuthorization(ctx, f.owner.ID, view.ID); e == nil {
				t.Fatal("old poll adopted successor")
			}
			if e = f.manager.CancelAuthorization(ctx, f.owner.ID, view.ID); e == nil {
				t.Fatal("old cancel consumed successor")
			}
			kept, e := f.repo.LegacyRecovery(ctx, f.owner.ID, result.OperationID)
			if e != nil || kept.State != "pending" || !bytes.Equal(kept.Attempt.Cipher, original.Cipher) {
				t.Fatal("successor erased unresolved history", e)
			}
			if scenario == "saved commit before clear" {
				f.wire.mu.Lock()
				f.wire.blocked[legacyWrongOrigin] = false
				f.wire.mu.Unlock()
				if e = f.manager.CleanupLegacyRecovery(ctx, f.owner.ID, result.OperationID); e != nil {
					t.Fatal(e)
				}
				var revoked bool
				if e = f.oldDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM user_api_keys WHERE id=$1`, saved.KeyID).Scan(&revoked); e != nil || !revoked {
					t.Fatal("original saved request key not revoked", e)
				}
				if e = f.correctDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM user_api_keys WHERE id=$1`, active.KeyID).Scan(&revoked); e != nil || revoked {
					t.Fatal("cleanup revoked replacement key", e)
				}
			}
			final, e := f.repo.RemoteWorkspace(ctx, f.owner.ID)
			if e != nil || final.Version != active.Version || final.KeyID != active.KeyID {
				t.Fatal("stale operation changed successor", e)
			}
		})
	}
}

func TestLegacyRecoveryDelayedRepliesAndOriginalOnlyCleanup(t *testing.T) {
	for _, phase := range []string{"start", "poll", "verify", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			f := legacyRecoverySetup(t)
			ctx := context.Background()
			origin := legacyWrongOrigin
			path := "/auth/workspace/" + phase
			if phase == "verify" {
				origin = installation.DefaultCentralOrigin
				path = "/v1/key"
			}
			entered, resume := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(resume) })
			defer release()
			pause := func() {
				f.wire.mu.Lock()
				f.wire.pauseOrigin = origin
				f.wire.pausePath = path
				f.wire.entered = entered
				f.wire.resume = resume
				f.wire.mu.Unlock()
			}
			var view remote.AuthorizationView
			var e error
			done := make(chan error, 1)
			if phase == "start" {
				pause()
				go func() {
					_, e := f.manager.BeginAuthorization(ctx, f.owner.ID, origin, "Delayed attempt", legacyLocalOrigin, 0)
					done <- e
				}()
			} else {
				view, e = f.manager.BeginAuthorization(ctx, f.owner.ID, origin, "Delayed attempt", legacyLocalOrigin, 0)
				if e != nil {
					t.Fatal(e)
				}
				f.approve(t, view, origin == legacyWrongOrigin)
				if phase == "cancel" {
					f.wire.losePoll = origin
					if _, e = f.manager.PollAuthorization(ctx, f.owner.ID, view.ID); !errors.Is(e, remote.ErrUnavailable) {
						t.Fatal(e)
					}
				}
				pause()
				if phase == "cancel" {
					go func() { done <- f.manager.CancelAuthorization(ctx, f.owner.ID, view.ID) }()
				} else {
					go func() { _, e := f.manager.PollAuthorization(ctx, f.owner.ID, view.ID); done <- e }()
				}
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("original reply never reached pause")
			}
			if phase == "start" {
				view, e = f.manager.Authorization(ctx, f.owner.ID)
				if e != nil {
					t.Fatal(e)
				}
			}
			state, e := f.manager.RecoveryStatus(ctx, f.owner.ID)
			if e != nil {
				t.Fatal(e)
			}
			input := map[string]any{"id": view.ID, "operation_id": uuid.NewString(), "version": 0, "connection_revision": state.Revision, "consent": true}
			response := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/recover", input, f.cookie)
			if response.Code != 200 {
				t.Fatalf("quarantine%d %s", response.Code, response.Body)
			}
			var recovery remote.Recovery
			json.Unmarshal(response.Body.Bytes(), &recovery)
			next, e := f.manager.BeginAuthorization(ctx, f.owner.ID, installation.DefaultCentralOrigin, "Successor", legacyLocalOrigin, recovery.Version)
			if e != nil {
				t.Fatal(e)
			}
			f.approve(t, next, false)
			active, e := f.manager.PollAuthorization(ctx, f.owner.ID, next.ID)
			if e != nil {
				t.Fatal(e)
			}
			release()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("delayed old operation reported success after successor")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("delayed operation did not finish")
			}
			kept, e := f.repo.RemoteWorkspace(ctx, f.owner.ID)
			if e != nil || kept.KeyID != active.KeyID || kept.Version != active.Version {
				t.Fatal("old reply overwrote successor", e)
			}
			f.wire.mu.Lock()
			f.wire.loseCancel = true
			beforeCalls := len(f.wire.calls)
			f.wire.mu.Unlock()
			cleanup := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/recovery/cleanup", map[string]string{"id": recovery.OperationID}, f.cookie)
			if cleanup.Code != 502 {
				t.Fatal("lost cleanup reply was confirmed", cleanup.Code)
			}
			archive, e := f.repo.LegacyRecovery(ctx, f.owner.ID, recovery.OperationID)
			if e != nil || archive.State != "pending" {
				t.Fatal("uncertain cleanup erased proof", e)
			}
			f.restart(t)
			cleanup = legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/recovery/cleanup", map[string]string{"id": recovery.OperationID}, f.cookie)
			if cleanup.Code != 200 {
				t.Fatal("exact original cleanup retry failed", cleanup.Code)
			}
			archive, e = f.repo.LegacyRecovery(ctx, f.owner.ID, recovery.OperationID)
			if e != nil || archive.State != "cancelled" || len(archive.Attempt.Cipher) == 0 {
				t.Fatal("terminal history lost", e)
			}
			f.wire.mu.Lock()
			calls := append([]string(nil), f.wire.calls[beforeCalls:]...)
			f.wire.mu.Unlock()
			for _, call := range calls {
				if call != origin+"/auth/workspace/cancel" {
					t.Fatal("cleanup escaped original destination", call)
				}
			}
			var revoked bool
			if e = f.correctDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM user_api_keys WHERE id=$1`, active.KeyID).Scan(&revoked); e != nil || revoked {
				t.Fatal("cleanup revoked successor key", e)
			}
			oldDB := f.oldDB
			if origin == installation.DefaultCentralOrigin {
				oldDB = f.correctDB
			}
			var oldKey sql.NullString
			if e = oldDB.QueryRow(`SELECT key_id::text FROM workspace_authorizations WHERE user_code=$1`, view.Code).Scan(&oldKey); e != nil {
				t.Fatal(e)
			}
			if oldKey.Valid {
				if e = oldDB.QueryRow(`SELECT revoked_at IS NOT NULL FROM user_api_keys WHERE id=$1`, oldKey.String).Scan(&revoked); e != nil || !revoked {
					t.Fatal("exact old issued key remains live", e)
				}
			}
		})
	}
}

func TestLegacyRecoveryControlsRejectForeignActorsAndInputs(t *testing.T) {
	f := legacyRecoverySetup(t)
	ctx := context.Background()
	pending, e := f.manager.BeginAuthorization(ctx, f.owner.ID, legacyWrongOrigin, "Private pending", legacyLocalOrigin, 0)
	if e != nil {
		t.Fatal(e)
	}
	path := "/v1/system/remote-workspace/authorization/recovery"
	if w := legacyRecoveryCall(t, f.local, "GET", path, nil, nil); w.Code != 401 {
		t.Fatal("anonymous recovery read", w.Code)
	}
	for _, query := range []string{"?cursor=bad", "?cursor=a&cursor=b", "?origin=https://foreign.example", "?cursor=%zz"} {
		if w := legacyRecoveryCall(t, f.local, "GET", path+query, nil, f.cookie); w.Code != 400 {
			t.Fatal("invalid recovery query accepted", query, w.Code)
		}
	}
	operation := uuid.NewString()
	body := map[string]any{"id": pending.ID, "operation_id": operation, "version": 0, "connection_revision": 0, "consent": true}
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest("POST", legacyLocalOrigin+"/v1/system/remote-workspace/authorization/recover", bytes.NewReader(raw))
	request.AddCookie(f.cookie)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://foreign.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	denied := httptest.NewRecorder()
	f.local.ServeHTTP(denied, request)
	if denied.Code != 403 {
		t.Fatal("cross-origin recovery accepted", denied.Code)
	}
	body["destination"] = "https://foreign.example"
	if w := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/recover", body, f.cookie); w.Code != 400 {
		t.Fatal("caller selected cleanup destination", w.Code)
	}
	delete(body, "destination")
	response := legacyRecoveryCall(t, f.local, "POST", "/v1/system/remote-workspace/authorization/recover", body, f.cookie)
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	people := postgres.NewAuthRepository(f.localDB)
	other, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Other admin", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.repo.SetAdmin(ctx, f.owner.ID, other.ID, true, false); e != nil {
		t.Fatal(e)
	}
	token, _ := wa.DeviceCode()
	if e = people.CreateSession(ctx, wa.Digest(token), other.ID, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	otherCookie := &http.Cookie{Name: "media_gateway_session", Value: token}
	visible := legacyRecoveryCall(t, f.local, "GET", path, nil, otherCookie)
	var state remote.RecoveryStatus
	if visible.Code != 200 || json.Unmarshal(visible.Body.Bytes(), &state) != nil || len(state.Items) != 0 {
		t.Fatal("foreign owner custody visible", visible.Code)
	}
	cleanup := legacyRecoveryCall(t, f.local, "POST", path+"/cleanup", map[string]string{"id": operation}, otherCookie)
	if cleanup.Code != 404 {
		t.Fatal("foreign owner cleanup accepted", cleanup.Code)
	}
	status := legacyRecoveryCall(t, f.local, "GET", path, nil, f.cookie)
	for _, secret := range []string{"ciphertext", "device_code", "identity_cipher", "token_hash", "saved_snapshot"} {
		if strings.Contains(status.Body.String(), secret) {
			t.Fatal("private recovery field leaked", secret)
		}
	}
	archive, e := f.repo.LegacyRecovery(ctx, f.owner.ID, operation)
	if e != nil || archive.State != "pending" {
		t.Fatal("denied cleanup changed custody", e)
	}
}

func TestLegacyRecoveryControlBoundsStalledBody(t *testing.T) {
	f := legacyRecoverySetup(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		defer cancel()
		f.local.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	conn, e := net.Dial("tcp", parsed.Host)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	started := time.Now()
	fmt.Fprintf(conn, "POST /v1/system/remote-workspace/authorization/recover HTTP/1.1\r\nHost: %s\r\nCookie: %s=%s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n", parsed.Host, f.cookie.Name, f.cookie.Value)
	response, e := http.ReadResponse(bufio.NewReader(conn), nil)
	if e != nil {
		t.Fatal("stalled body did not receive bounded error", e)
	}
	defer response.Body.Close()
	if response.StatusCode != 400 || time.Since(started) > 1500*time.Millisecond {
		t.Fatal("stalled request exceeded inherited deadline", response.StatusCode)
	}
	var count int
	if e = f.localDB.QueryRow(`SELECT count(*) FROM remote_workspace_recovery`).Scan(&count); e != nil || count != 0 {
		t.Fatal("stalled request changed custody", e)
	}
}
