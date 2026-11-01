package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

type providerResourceFixture struct {
	db   *sql.DB
	ctx  context.Context
	repo *ProviderResourceRepository
	job  providerresources.Job
}

func newProviderResourceFixture(t *testing.T) *providerResourceFixture {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	owner, powner := uuid.NewString(), uuid.NewString()
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), owner), powner, owner)
	duration := 4.0
	j := providerresources.Job{ProviderID: "actual-kling-connection", ConnectionID: "actual-kling-connection", Revision: "immutable-canonical-resource-revision", Intent: providerresources.Intent{Kind: "element", Subtype: "video_character", Name: "Synthetic element", Sources: []core.ResourceSourceEvidence{{ReferenceID: uuid.NewString(), MediaType: "video", SHA256: strings.Repeat("a", 64), Duration: &duration}}}}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM provider_resources WHERE owner_id=$1`, owner)
		db.Exec(`DELETE FROM provider_resource_jobs WHERE owner_id=$1`, owner)
		db.Exec(`DELETE FROM tasks WHERE owner_id=$1`, owner)
	})
	return &providerResourceFixture{db: db, ctx: ctx, repo: NewProviderResourceRepository(db), job: j}
}
func (f *providerResourceFixture) start(t *testing.T, key string) providerresources.Job {
	t.Helper()
	j, created, err := f.repo.ReserveCreate(f.ctx, f.job, key)
	if err != nil || !created {
		t.Fatal(j, created, err)
	}
	j, started, err := f.repo.StartJob(f.ctx, j.ID)
	if err != nil || !started {
		t.Fatal(j, started, err)
	}
	return j
}
func (f *providerResourceFixture) complete(t *testing.T, key string) providerresources.Binding {
	t.Helper()
	j := f.start(t, key)
	b, err := f.repo.CompleteCreate(f.ctx, j.ID, "900719925474099312345-"+key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func (f *providerResourceFixture) scope(context.Context, string) (core.ProviderConnectionScope, error) {
	return core.ProviderConnectionScope{ID: f.job.ConnectionID, OwnerID: identity.ProviderOwnerID(f.ctx), Revision: f.job.Revision}, nil
}

func TestProviderResourcePersistenceIdempotencyPrivacyAndScope(t *testing.T) {
	f := newProviderResourceFixture(t)
	j := f.start(t, "create-once")
	if _, err := f.repo.GetBinding(f.ctx, j.BindingID); !errors.Is(err, providerresources.ErrNotFound) {
		t.Fatal("unconfirmed remote create became a binding", err)
	}
	if err := f.repo.SaveHandle(f.ctx, j.ID, "private-signed-handle", "secret raw warning https://provider.invalid"); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SaveHandle(f.ctx, j.ID, "changed-handle", ""); !errors.Is(err, providerresources.ErrConflict) {
		t.Fatal("private handle was mutable", err)
	}
	b, err := f.repo.CompleteResource(f.ctx, j.ID, core.ProviderResource{RemoteID: "9007199254740993123456789", Kind: "element", Subtype: "video_character", Status: core.ProviderSucceeded, Sources: []core.ResourceSourceInput{{URL: "https://private.invalid/provider-source", Duration: func() *float64 { n := 999.0; return &n }()}}})
	if err != nil || b.RemoteID != "9007199254740993123456789" || *b.Sources[0].Duration != 4 {
		t.Fatal("private ID or inspected source evidence changed", b, err)
	}
	restarted := NewProviderResourceRepository(f.db)
	again, created, err := restarted.ReserveCreate(f.ctx, f.job, "create-once")
	if err != nil || created || again.ID != j.ID || again.Status != providerresources.Succeeded {
		t.Fatal("idempotent restart resubmitted resource", again, created, err)
	}
	changed := f.job
	changed.Intent.Name = "different intent"
	if _, _, err = restarted.ReserveCreate(f.ctx, changed, "create-once"); !errors.Is(err, providerresources.ErrConflict) {
		t.Fatal("changed intent replayed accepted create", err)
	}
	for _, other := range []context.Context{identity.WithResourceOwners(identity.WithOwnerID(context.Background(), uuid.NewString()), identity.ProviderOwnerID(f.ctx), identity.ProviderOwnerID(f.ctx)), identity.WithResourceOwners(identity.WithOwnerID(context.Background(), identity.OwnerID(f.ctx)), uuid.NewString(), uuid.NewString())} {
		if _, err = restarted.GetBinding(other, b.ID); !errors.Is(err, providerresources.ErrNotFound) {
			t.Fatal("foreign scope read binding", err)
		}
		if _, err = restarted.GetJob(other, j.ID); !errors.Is(err, providerresources.ErrNotFound) {
			t.Fatal("foreign scope read private job", err)
		}
		if list, e := restarted.ListBindings(other, f.job.ProviderID, 20, 0); e != nil || len(list) != 0 {
			t.Fatal(list, e)
		}
	}
	refs := []core.ResourceReference{{BindingID: b.ID, ReferenceID: "Character1"}}
	snapshot, err := providerresources.New(restarted, f.scope).ResolveResourceReferences(f.ctx, f.job.ProviderID, refs)
	if err != nil || snapshot[0].ConnectionIdentity != f.job.Revision {
		t.Fatal(snapshot, err)
	}
	for _, mismatch := range []string{"connection", "revision"} {
		scope := func(context.Context, string) (core.ProviderConnectionScope, error) {
			current, _ := f.scope(f.ctx, f.job.ProviderID)
			if mismatch == "connection" {
				current.ID = "same-keys-other-connection"
			} else {
				current.Revision = "rotated"
			}
			return current, nil
		}
		if _, e := providerresources.New(restarted, scope).ResolveResourceReferences(f.ctx, f.job.ProviderID, refs); !errors.Is(e, providerresources.ErrNotFound) {
			t.Fatal(mismatch, e)
		}
	}
	stored, err := restarted.GetJob(f.ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{b, stored} {
		raw, _ := json.Marshal(value)
		for _, private := range []string{b.RemoteID, "private-signed-handle", f.job.Revision, "secret raw warning", "private.invalid", "provider-source"} {
			if strings.Contains(string(raw), private) {
				t.Fatal("private persistence escaped public projection", string(raw))
			}
		}
	}
	var raw string
	if err = f.db.QueryRow(`SELECT intent::text FROM provider_resource_jobs WHERE id=$1`, j.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "https://") || strings.Contains(raw, "999") {
		t.Fatal("unverified provider data replaced trusted intent", raw)
	}
	if stored.Warning == "" || strings.Contains(stored.Warning, "secret") {
		t.Fatal("raw warning retained", stored.Warning)
	}
}

func TestProviderResourceUncertainCreateAndPendingDeleteSurviveRestart(t *testing.T) {
	f := newProviderResourceFixture(t)
	j := f.start(t, "uncertain-create")
	if err := f.repo.RecordUncertain(f.ctx, j.ID, "credential-and-private-provider-body"); err != nil {
		t.Fatal(err)
	}
	restarted := NewProviderResourceRepository(f.db)
	state, started, err := restarted.StartJob(f.ctx, j.ID)
	if err != nil || started || state.Status != providerresources.Running || state.Handle != "" || state.Warning == "" || strings.Contains(state.Warning, "credential") {
		t.Fatal("uncertain create became resubmittable", state, started, err)
	}
	replay, created, err := restarted.ReserveCreate(f.ctx, f.job, "uncertain-create")
	if err != nil || created || replay.ID != j.ID {
		t.Fatal(replay, created, err)
	}
	pending, err := restarted.PendingJobs(f.ctx, 100)
	if err != nil || len(pending) != 1 || pending[0].ID != j.ID {
		t.Fatal(pending, err)
	}
	b, err := restarted.CompleteCreate(f.ctx, j.ID, "confirmed-private-remote-id")
	if err != nil {
		t.Fatal(err)
	}
	deletion, created, err := restarted.BeginDelete(f.ctx, b.ID, "delete-once")
	if err != nil || !created {
		t.Fatal(deletion, created, err)
	}
	if _, err = providerresources.New(restarted, f.scope).ResolveResourceReferences(f.ctx, f.job.ProviderID, []core.ResourceReference{{BindingID: b.ID, ReferenceID: "Character1"}}); !errors.Is(err, providerresources.ErrNotFound) {
		t.Fatal("pending delete remained usable", err)
	}
	deletion, started, err = restarted.StartJob(f.ctx, deletion.ID)
	if err != nil || !started {
		t.Fatal(deletion, started, err)
	}
	if err = restarted.SaveHandle(f.ctx, deletion.ID, "private-delete-handle", ""); err != nil {
		t.Fatal(err)
	}
	restarted = NewProviderResourceRepository(f.db)
	b, err = restarted.GetBinding(f.ctx, b.ID)
	if err != nil || b.Status != providerresources.Deleting || b.DeletedAt != nil {
		t.Fatal("submitted delete falsely terminal", b, err)
	}
	if _, started, err = restarted.StartJob(f.ctx, deletion.ID); err != nil || started {
		t.Fatal("restart resubmitted deletion", started, err)
	}
	if err = restarted.CompleteDelete(f.ctx, deletion.ID); err != nil {
		t.Fatal(err)
	}
	b, err = restarted.GetBinding(f.ctx, b.ID)
	if err != nil || b.Status != providerresources.Deleted || b.DeletedAt == nil {
		t.Fatal(b, err)
	}
	terminal, err := restarted.GetJob(f.ctx, deletion.ID)
	if err != nil || terminal.Status != providerresources.Succeeded {
		t.Fatal(terminal, err)
	}
	if err = restarted.CompleteDelete(f.ctx, deletion.ID); err != nil {
		t.Fatal("terminal deletion was not idempotent", err)
	}
	replay, created, err = restarted.BeginDelete(f.ctx, b.ID, "delete-once")
	if err != nil || created || replay.ID != deletion.ID {
		t.Fatal("delete retry created another remote attempt", replay, created, err)
	}
}

func TestProviderResourceTerminalResultAndAutoVoiceAreAtomic(t *testing.T) {
	f := newProviderResourceFixture(t)
	j := f.start(t, "atomic-create")
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	fn, trigger := "resource_fail_"+suffix, "resource_trigger_"+suffix
	_, err := f.db.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic terminal commit failure'; END $$; CREATE TRIGGER %s BEFORE UPDATE ON provider_resource_jobs FOR EACH ROW WHEN (NEW.id='%s'::uuid AND NEW.status='succeeded') EXECUTE FUNCTION %s()`, fn, trigger, j.ID, fn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger + ` ON provider_resource_jobs`)
		f.db.Exec(`DROP FUNCTION IF EXISTS ` + fn + `()`)
	})
	result := core.ProviderResource{RemoteID: "private-element-atomic", Kind: "element", Subtype: "video_character", Status: core.ProviderSucceeded, BoundVoice: &core.ProviderResource{RemoteID: "private-automatic-voice", Kind: "voice", Status: core.ProviderSucceeded}}
	if _, err = f.repo.CompleteResource(f.ctx, j.ID, result); err == nil {
		t.Fatal("synthetic terminal failure did not abort")
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM provider_resources WHERE owner_id=$1`, identity.OwnerID(f.ctx)).Scan(&count); err != nil || count != 0 {
		t.Fatal("terminal rollback left an element or orphan bound voice", count, err)
	}
	state, err := f.repo.GetJob(f.ctx, j.ID)
	if err != nil || state.Status != providerresources.Running {
		t.Fatal("terminal rollback changed accepted job", state, err)
	}
	if _, err = f.db.Exec(`DROP TRIGGER ` + trigger + ` ON provider_resource_jobs`); err != nil {
		t.Fatal(err)
	}
	b, err := f.repo.CompleteResource(f.ctx, j.ID, result)
	if err != nil || b.BoundVoiceBindingID == "" {
		t.Fatal(b, err)
	}
	voice, err := f.repo.GetBinding(f.ctx, b.BoundVoiceBindingID)
	if err != nil || voice.Kind != "voice" || voice.Subtype != "video_bound" || voice.RemoteID != result.BoundVoice.RemoteID || voice.Revision != b.Revision || voice.ConnectionID != b.ConnectionID || voice.OwnerID != b.OwnerID {
		t.Fatal("auto-bound voice lost its local authorized identity", voice, err)
	}
	if _, _, err = f.repo.BeginDelete(f.ctx, voice.ID, "voice-still-bound"); !errors.Is(err, providerresources.ErrInUse) {
		t.Fatal("active element lost its bound voice", err)
	}
	if replay, e := f.repo.CompleteResource(f.ctx, j.ID, result); e != nil || replay.BoundVoiceBindingID != voice.ID {
		t.Fatal("terminal replay duplicated voice binding", replay, e)
	}
}

func TestProviderResourceAdmissionDeleteGateAndSnapshotChecks(t *testing.T) {
	f := newProviderResourceFixture(t)
	b := f.complete(t, "admission-gate")
	refs := []core.ResourceReference{{BindingID: b.ID, ReferenceID: "Character1"}}
	snapshots, err := providerresources.New(f.repo, f.scope).ResolveResourceReferences(f.ctx, f.job.ProviderID, refs)
	if err != nil {
		t.Fatal(err)
	}
	task := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(f.ctx), ProviderOwnerID: identity.ProviderOwnerID(f.ctx), Provider: f.job.ProviderID, ProviderIdentity: f.job.Revision, Options: core.MediaOptions{ResourceReferences: refs, ResourceSnapshots: snapshots}}
	for _, mutation := range []string{"remote id", "revision", "empty revision", "duration", "preset", "actual connection", "missing ref"} {
		changed := task
		changed.Options.ResourceSnapshots = append([]core.ResourceSnapshot(nil), snapshots...)
		changed.Options.ResourceSnapshots[0].Sources = providerresources.CloneSources(snapshots[0].Sources)
		switch mutation {
		case "remote id":
			changed.Options.ResourceSnapshots[0].RemoteID = "other"
		case "revision":
			changed.Options.ResourceSnapshots[0].ConnectionIdentity = "stale"
		case "empty revision":
			changed.ProviderIdentity = ""
		case "duration":
			*changed.Options.ResourceSnapshots[0].Sources[0].Duration = 99
		case "preset":
			changed.Options.ResourceSnapshots[0].IsPreset = !b.IsPreset
		case "actual connection":
			changed.Provider = "other-connection"
		case "missing ref":
			changed.Options.ResourceReferences = nil
		}
		tx, e := f.db.BeginTx(f.ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		e = validateTaskResourceSnapshots(f.ctx, tx, changed)
		tx.Rollback()
		if !errors.Is(e, core.ErrOptionsInvalid) {
			t.Fatal(mutation, e)
		}
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var lockerPID int
	if err = tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&lockerPID); err != nil {
		t.Fatal(err)
	}
	if err = validateTaskResourceSnapshots(f.ctx, tx, task); err != nil {
		t.Fatal(err)
	}
	type result struct{ err error }
	deleted := make(chan result, 1)
	go func() { _, _, e := f.repo.BeginDelete(f.ctx, b.ID, "concurrent-delete"); deleted <- result{e} }()
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, lockerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case outcome := <-deleted:
			t.Fatal("delete did not wait for admission share lock", outcome.err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !blocked {
		t.Fatal("delete was not blocked by admission gate")
	}
	raw, _ := json.Marshal(task.Options)
	_, err = tx.ExecContext(f.ctx, `INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,status,prompt,request_options,provider_identity) VALUES($1,$2,$3,$2,$4,'synthetic','queued','Synthetic',$5,$6)`, task.ID, task.OwnerID, task.ProviderOwnerID, task.Provider, raw, task.ProviderIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-deleted:
		if !errors.Is(outcome.err, providerresources.ErrInUse) {
			t.Fatal("delete bypassed the committed admitted task", outcome.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not finish after admission commit")
	}
	if _, err = f.db.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.repo.BeginDelete(f.ctx, b.ID, "after-generation"); err != nil {
		t.Fatal(err)
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = validateTaskResourceSnapshots(f.ctx, tx, task)
	tx.Rollback()
	if !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("new admission used a deleting resource", err)
	}
}

func TestProviderResourceAdditiveRollbackRetainsEvidence(t *testing.T) {
	f := newProviderResourceFixture(t)
	b := f.complete(t, "rollback-evidence")
	raw, err := migrations.ReadFile("migrations/084_provider_resources.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration rollback boundary missing")
	}
	if _, err = f.db.Exec(parts[1]); err != nil {
		t.Fatal(err)
	}
	if saved, e := f.repo.GetBinding(f.ctx, b.ID); e != nil || saved.RemoteID != b.RemoteID {
		t.Fatal("rollback erased resource identity", saved, e)
	}
	if _, err = f.db.Exec(parts[0]); err != nil {
		t.Fatal(err)
	}
	if saved, e := f.repo.GetBinding(f.ctx, b.ID); e != nil || saved.Revision != b.Revision {
		t.Fatal("re-upgrade replaced immutable evidence", saved, e)
	}
}

func TestProviderResourceGenerationSessionsCaptureTrustedInputAtomically(t *testing.T) {
	f := newProviderResourceFixture(t)
	rawURL := "https://canonical.fixture.invalid/input-video"
	options := core.MediaOptions{VideoURLs: []string{rawURL}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{rawURL: {Type: "video", Duration: 4, SHA256: strings.Repeat("b", 64)}}}
	taskID := uuid.NewString()
	encoded, _ := json.Marshal(options)
	if _, err := f.db.Exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,status,prompt,request_options,provider_identity) VALUES($1,$2,$3,$2,$4,'synthetic','running','Synthetic',$5,$6)`, taskID, identity.OwnerID(f.ctx), identity.ProviderOwnerID(f.ctx), f.job.ProviderID, encoded, f.job.Revision); err != nil {
		t.Fatal(err)
	}
	session := &core.MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceSHA256: strings.Repeat("b", 64), SourceVideoDuration: 4, Faces: []core.SessionFace{{ID: "private-remote-face", StartMS: 0, EndMS: 4000}}}
	resources := []core.ProviderResource{{Kind: "face_session", RemoteID: "private-face-session", Status: core.ProviderSucceeded, Session: session}, {Kind: "editing_session", RemoteID: "private-edit-session", Status: core.ProviderSucceeded, Session: &core.MediaSessionEvidence{ExpiresAt: session.ExpiresAt, SourceSHA256: session.SourceSHA256, SourceVideoDuration: 4, FrameCount: 120, FPS: 30, SelectionRevision: 1, HasSelection: true, Selections: []core.FrameSelection{{FrameIndex: 0, Points: []core.SelectionPoint{{X: 0.5, Y: 0.5}}}}}}}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := captureTaskResourceBindings(f.ctx, tx, taskID, resources)
	if err != nil || len(refs) != 2 {
		tx.Rollback()
		t.Fatal(refs, err)
	}
	tx.Rollback()
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM provider_resources WHERE origin_task_id=$1`, taskID).Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback retained reusable session identity", count, err)
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	refs, err = captureTaskResourceBindings(f.ctx, tx, taskID, resources)
	if err != nil {
		t.Fatal(err)
	}
	captured, _ := json.Marshal(refs)
	if _, err = tx.Exec(`UPDATE tasks SET status='succeeded',result_resource_bindings=$2 WHERE id=$1`, taskID, captured); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i, ref := range refs {
		b, e := f.repo.GetBinding(f.ctx, ref.BindingID)
		if e != nil || b.OriginTaskID != taskID || b.RemoteID != resources[i].RemoteID || b.OwnerID != identity.OwnerID(f.ctx) || b.Revision != f.job.Revision || b.Sources[0].SHA256 != session.SourceSHA256 || *b.Sources[0].Duration != 4 {
			t.Fatal("captured session lost accepted source/scope", b, e)
		}
		public, _ := json.Marshal(b)
		if strings.Contains(string(public), b.RemoteID) || strings.Contains(string(public), taskID) || strings.Contains(string(public), "private-remote-face") {
			t.Fatal("captured private session leaked", string(public))
		}
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	replay, e := captureTaskResourceBindings(f.ctx, tx, taskID, resources)
	tx.Rollback()
	if e != nil || len(replay) != 2 || replay[0].BindingID != refs[0].BindingID {
		t.Fatal("terminal capture duplicated local session", replay, e)
	}
}
func TestProviderResourceGenerationCannotCaptureUnknownOrForgedVideoEvidence(t *testing.T) {
	for _, kind := range []string{"unknown hash", "wrong hash", "wrong duration", "foreign scope", "empty canonical revision"} {
		t.Run(kind, func(t *testing.T) {
			f := newProviderResourceFixture(t)
			taskID := uuid.NewString()
			rawURL := "https://uninspected.fixture.invalid/video"
			evidence := core.InputMediaEvidence{Type: "video", Duration: 4, SHA256: strings.Repeat("b", 64)}
			if kind == "unknown hash" {
				evidence.SHA256 = ""
			}
			options := core.MediaOptions{VideoURLs: []string{rawURL}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{rawURL: evidence}}
			raw, _ := json.Marshal(options)
			revision := f.job.Revision
			if kind == "empty canonical revision" {
				revision = ""
			}
			if _, err := f.db.Exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,status,prompt,request_options,provider_identity) VALUES($1,$2,$3,$2,$4,'synthetic','running','Synthetic',$5,$6)`, taskID, identity.OwnerID(f.ctx), identity.ProviderOwnerID(f.ctx), f.job.ProviderID, raw, revision); err != nil {
				t.Fatal(err)
			}
			session := &core.MediaSessionEvidence{ExpiresAt: time.Now().Add(time.Hour), SourceSHA256: strings.Repeat("b", 64), SourceVideoDuration: 4}
			if kind == "wrong hash" {
				session.SourceSHA256 = strings.Repeat("c", 64)
			}
			if kind == "wrong duration" {
				session.SourceVideoDuration = 99
			}
			ctx := f.ctx
			if kind == "foreign scope" {
				ctx = identity.WithResourceOwners(identity.WithOwnerID(context.Background(), uuid.NewString()), identity.ProviderOwnerID(f.ctx), identity.ProviderOwnerID(f.ctx))
			}
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			refs, err := captureTaskResourceBindings(ctx, tx, taskID, []core.ProviderResource{{Kind: "face_session", RemoteID: "private-invalid-session", Status: core.ProviderSucceeded, Session: session}})
			tx.Rollback()
			if kind == "unknown hash" {
				if err != nil || len(refs) != 0 {
					t.Fatal("uninspected remote video did not remain artifact-only", refs, err)
				}
			} else if err == nil {
				t.Fatal("forged or foreign evidence became reusable", refs)
			}
			var count int
			if e := f.db.QueryRow(`SELECT count(*) FROM provider_resources WHERE origin_task_id=$1`, taskID).Scan(&count); e != nil || count != 0 {
				t.Fatal("invalid capture retained a binding", count, e)
			}
		})
	}
}

func TestProviderResourcePendingCreateRetainsBoundVoice(t *testing.T) {
	f := newProviderResourceFixture(t)
	f.job.Intent.Kind = "voice"
	f.job.Intent.Subtype = ""
	f.job.Intent.Name = "Synthetic voice"
	f.job.Intent.Sources[0].MediaType = "audio"
	voice := f.complete(t, "pending-bound-voice")
	f.job.Intent.Kind = "element"
	f.job.Intent.Subtype = "video_character"
	f.job.Intent.Name = "Synthetic element"
	f.job.Intent.BoundVoiceBindingID = voice.ID
	creation, created, err := f.repo.ReserveCreate(f.ctx, f.job, "create-with-existing-voice")
	if err != nil || !created {
		t.Fatal(creation, created, err)
	}
	if _, _, err = f.repo.BeginDelete(f.ctx, voice.ID, "delete-retained-voice"); !errors.Is(err, providerresources.ErrInUse) {
		t.Fatal("queued creation lost its bound voice", err)
	}
	if err = f.repo.FailJob(f.ctx, creation.ID, "synthetic definitive failure", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.repo.BeginDelete(f.ctx, voice.ID, "delete-after-failed-create"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.repo.ReserveCreate(f.ctx, f.job, "create-after-voice-delete"); !errors.Is(err, providerresources.ErrNotFound) {
		t.Fatal("new create retained a deleting voice", err)
	}
}

func resourcePaidTaskOptions(kind, subtype string, source core.ResourceSourceInput) core.MediaOptions {
	request := &core.ResourceCreateRequest{Kind: kind, Subtype: subtype, Name: "Synthetic paid resource"}
	if kind == "voice" {
		request.VoiceSources = []core.ResourceSourceInput{source}
	} else if source.MediaType == "image" {
		request.Images = []core.ResourceSourceInput{source}
	} else {
		request.Videos = []core.ResourceSourceInput{source}
	}
	return core.MediaOptions{ResourceIntent: &core.ResourceIntent{Kind: kind, Subtype: subtype, Name: request.Name, ReferenceIDs: []string{source.ReferenceID}}, ResourceCreation: request}
}
func insertPaidResourceTask(t *testing.T, f *providerResourceFixture, options core.MediaOptions) string {
	t.Helper()
	id := uuid.NewString()
	raw, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,status,prompt,request_options,provider_identity) VALUES($1,$2,$3,$2,$4,'synthetic-create','running','',$5,$6)`, id, identity.OwnerID(f.ctx), identity.ProviderOwnerID(f.ctx), f.job.ProviderID, raw, f.job.Revision); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestProviderResourcePaidCreationCaptureRetainsOnlyAcceptedProvenance(t *testing.T) {
	f := newProviderResourceFixture(t)
	duration := 4.0
	source := core.ResourceSourceInput{ReferenceID: uuid.NewString(), URL: "https://canonical.invalid/signed-video", MediaType: "video", SHA256: strings.Repeat("a", 64), Duration: &duration}
	options := resourcePaidTaskOptions("element", "video_character", source)
	taskID := insertPaidResourceTask(t, f, options)
	result := core.ProviderResource{Kind: "element", Subtype: "video_character", RemoteID: "private-paid-created-element", Status: core.ProviderSucceeded, Sources: []core.ResourceSourceInput{{URL: "https://unverified.invalid/source", SHA256: strings.Repeat("f", 64), Duration: func() *float64 { n := 999.0; return &n }()}}, BoundVoice: &core.ProviderResource{Kind: "voice", RemoteID: "private-paid-auto-voice", Status: core.ProviderSucceeded}}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := captureTaskResourceBindings(f.ctx, tx, taskID, []core.ProviderResource{result})
	if err != nil || len(refs) != 1 {
		tx.Rollback()
		t.Fatal(refs, err)
	}
	if _, err = tx.Exec(`UPDATE tasks SET status='synthetic-invalid-terminal-state' WHERE id=$1`, taskID); err == nil {
		t.Fatal("terminal failure fixture did not fail")
	}
	tx.Rollback()
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM provider_resources WHERE owner_id=$1`, identity.OwnerID(f.ctx)).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed terminal task left a resource or bound voice", count, err)
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	refs, err = captureTaskResourceBindings(f.ctx, tx, taskID, []core.ProviderResource{result})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(refs)
	if _, err = tx.Exec(`UPDATE tasks SET status='succeeded',result_resource_bindings=$2 WHERE id=$1`, taskID, encoded); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	b, err := f.repo.GetBinding(f.ctx, refs[0].BindingID)
	if err != nil || b.OriginTaskID != taskID || b.BoundVoiceBindingID == "" || len(b.Sources) != 1 || b.Sources[0].ReferenceID != source.ReferenceID || b.Sources[0].SHA256 != source.SHA256 || *b.Sources[0].Duration != 4 {
		t.Fatal("paid capture replaced trusted source evidence", b, err)
	}
	voice, err := f.repo.GetBinding(f.ctx, b.BoundVoiceBindingID)
	if err != nil || voice.OriginTaskID != taskID || voice.RemoteID != result.BoundVoice.RemoteID || voice.OwnerID != b.OwnerID {
		t.Fatal("paid creation dropped scoped bound voice", voice, err)
	}
	public, _ := json.Marshal(b)
	if strings.Contains(string(public), result.RemoteID) || strings.Contains(string(public), "unverified.invalid") {
		t.Fatal("paid resource leaked private provider data", string(public))
	}
}
func TestProviderResourcePaidCreationMetadataMustMatchLocalIntent(t *testing.T) {
	source := core.ResourceSourceInput{ReferenceID: uuid.NewString(), URL: "https://canonical.invalid/voice", MediaType: "audio", SHA256: strings.Repeat("a", 64)}
	for _, kind := range []string{"missing public intent", "missing private evidence", "kind", "subtype", "source id", "source hash", "unproven empty source"} {
		options := resourcePaidTaskOptions("voice", "", source)
		switch kind {
		case "missing public intent":
			options.ResourceIntent = nil
		case "missing private evidence":
			options.ResourceCreation = nil
		case "kind":
			options.ResourceCreation.Kind = "element"
		case "subtype":
			options.ResourceCreation.Subtype = "other"
		case "source id":
			options.ResourceCreation.VoiceSources[0].ReferenceID = uuid.NewString()
		case "source hash":
			options.ResourceCreation.VoiceSources[0].SHA256 = ""
		case "unproven empty source":
			options.ResourceCreation.VoiceSources = nil
			options.ResourceIntent.ReferenceIDs = nil
		}
		if _, err := resourceTaskCreationIntent(options); !errors.Is(err, providerresources.ErrInvalid) {
			t.Fatal(kind, err)
		}
	}
	// Video input is legitimate voice provenance too; it must not be coerced to audio.
	source.MediaType = "video"
	duration := 4.0
	source.Duration = &duration
	if intent, err := resourceTaskCreationIntent(resourcePaidTaskOptions("voice", "", source)); err != nil || intent.Sources[0].MediaType != "video" {
		t.Fatal(intent, err)
	}
}
func TestProviderResourcePaidGeneratedVideoSourceRemainsImmutable(t *testing.T) {
	f := newProviderResourceFixture(t)
	generatedID := uuid.NewString()
	duration := 4.0
	options := core.MediaOptions{ResourceIntent: &core.ResourceIntent{Kind: "voice", Name: "Voice from generated video", GeneratedTaskID: generatedID}, ResourceCreation: &core.ResourceCreateRequest{Kind: "voice", Name: "Voice from generated video", GeneratedVideo: &core.GeneratedResourceSource{TaskID: generatedID, TaskHandle: "private-upstream-task", OutputID: "private-upstream-video", SHA256: strings.Repeat("c", 64), Duration: &duration}}}
	taskID := insertPaidResourceTask(t, f, options)
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	refs, err := captureTaskResourceBindings(f.ctx, tx, taskID, []core.ProviderResource{{Kind: "voice", RemoteID: "private-generated-voice", Status: core.ProviderSucceeded}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(refs)
	if _, err = tx.Exec(`UPDATE tasks SET status='succeeded',result_resource_bindings=$2 WHERE id=$1`, taskID, encoded); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	b, err := f.repo.GetBinding(f.ctx, refs[0].BindingID)
	if err != nil || b.Sources[0].ReferenceID != generatedID || b.Sources[0].SHA256 != strings.Repeat("c", 64) || *b.Sources[0].Duration != 4 {
		t.Fatal("generated local provenance lost", b, err)
	}
	options.ResourceCreation.GeneratedVideo.TaskID = uuid.NewString()
	if _, err = resourceTaskCreationIntent(options); !errors.Is(err, providerresources.ErrInvalid) {
		t.Fatal("host source and local generated task mismatched", err)
	}
	ordinary := insertPaidResourceTask(t, f, core.MediaOptions{})
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = captureTaskResourceBindings(f.ctx, tx, ordinary, []core.ProviderResource{{Kind: "voice", RemoteID: "arbitrary-import", Status: core.ProviderSucceeded}})
	tx.Rollback()
	if !errors.Is(err, providerresources.ErrInvalid) {
		t.Fatal("ordinary task imported a remote resource ID", err)
	}
}
func TestProviderResourcePaidCreationRetainsAndValidatesBoundVoice(t *testing.T) {
	f := newProviderResourceFixture(t)
	f.job.Intent.Kind = "voice"
	f.job.Intent.Subtype = ""
	voice := f.complete(t, "paid-existing-voice")
	snapshot, err := providerresources.New(f.repo, f.scope).ResolveResourceReferences(f.ctx, f.job.ProviderID, []core.ResourceReference{{BindingID: voice.ID, ReferenceID: "existing_voice"}})
	if err != nil {
		t.Fatal(err)
	}
	duration := 4.0
	source := core.ResourceSourceInput{ReferenceID: uuid.NewString(), MediaType: "video", SHA256: strings.Repeat("b", 64), Duration: &duration}
	options := resourcePaidTaskOptions("element", "video_character", source)
	options.ResourceIntent.BoundVoiceBindingID = voice.ID
	options.ResourceCreation.BoundVoice = &snapshot[0]
	taskID := insertPaidResourceTask(t, f, options)
	task := core.Task{ID: taskID, OwnerID: identity.OwnerID(f.ctx), ProviderOwnerID: identity.ProviderOwnerID(f.ctx), Provider: f.job.ProviderID, ProviderIdentity: f.job.Revision, Options: options}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = validateTaskResourceSnapshots(f.ctx, tx, task)
	tx.Rollback()
	if err != nil {
		t.Fatal("valid paid bound voice rejected", err)
	}
	options.ResourceCreation.BoundVoice.RemoteID = "forged-private-voice"
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = validateTaskResourceSnapshots(f.ctx, tx, task)
	tx.Rollback()
	if !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("paid create accepted forged bound voice", err)
	}
	if _, _, err = f.repo.BeginDelete(f.ctx, voice.ID, "delete-paid-retained-voice"); !errors.Is(err, providerresources.ErrInUse) {
		t.Fatal("paid task lost its selected voice", err)
	}
}

// resourceLegacyProjection enumerates the complete pre-upgrade column set and
// explicitly rebuilds every old field; it never hides an arbitrary new-column
// name from a whole-row JSON comparison.
type resourceLegacyProjection struct {
	tables  []string
	columns map[string][]string
}

func resourceTestIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
func newResourceLegacyProjection(t *testing.T, db *sql.DB, tables ...string) resourceLegacyProjection {
	t.Helper()
	projection := resourceLegacyProjection{tables: append([]string(nil), tables...), columns: map[string][]string{}}
	for _, table := range tables {
		rows, err := db.Query(`SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position`, table)
		if err != nil {
			t.Fatal(err)
		}
		var columns []string
		for rows.Next() {
			var column string
			if err = rows.Scan(&column); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			columns = append(columns, column)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(columns) == 0 {
			t.Fatal("pre-upgrade table has no explicit column projection", table, err)
		}
		projection.columns[table] = columns
	}
	return projection
}
func (p resourceLegacyProjection) snapshot(t *testing.T, db *sql.DB, phase string) string {
	t.Helper()
	var snapshot strings.Builder
	for _, table := range p.tables {
		var fields []string
		for _, column := range p.columns[table] {
			literal := strings.ReplaceAll(column, "'", "''")
			fields = append(fields, "jsonb_build_object('"+literal+"',t."+resourceTestIdentifier(column)+")")
		}
		expression := "(" + strings.Join(fields, " || ") + ")"
		var rows string
		var count int
		query := `SELECT count(*),COALESCE(jsonb_agg(` + expression + ` ORDER BY ` + expression + `::text),'[]'::jsonb)::text FROM ` + resourceTestIdentifier(table) + ` t`
		if err := db.QueryRow(query).Scan(&count, &rows); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(rows))
		t.Logf("preservation phase=%s table=%s old_fields=%d rows=%d sha256=%x", phase, table, len(p.columns[table]), count, digest)
		snapshot.WriteString(table)
		snapshot.WriteString(rows)
	}
	return snapshot.String()
}
func assertResourceBindingsEmptyDefault(t *testing.T, db *sql.DB) {
	t.Helper()
	var total, empty int
	var nullable, defaultValue string
	if err := db.QueryRow(`SELECT is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='tasks' AND column_name='result_resource_bindings'`).Scan(&nullable, &defaultValue); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE result_resource_bindings='[]'::jsonb) FROM tasks`).Scan(&total, &empty); err != nil {
		t.Fatal(err)
	}
	if nullable != "NO" || !strings.Contains(defaultValue, "[]") || total == 0 || empty != total {
		t.Fatal("084 did not add a non-null empty resource-binding default", total, empty, nullable)
	}
	t.Logf("preservation default=result_resource_bindings rows=%d empty_arrays=%d", total, empty)
}

type resourcePreservationSeed struct{ creationTask, consumerTask, owner, providerOwner string }

func seedResourcePreservationMetadata(t *testing.T, db *sql.DB, owner, providerOwner string) resourcePreservationSeed {
	t.Helper()
	duration := 4.0
	source := core.ResourceSourceInput{ReferenceID: uuid.NewString(), URL: "https://canonical.fixture.invalid/private-input", MediaType: "video", SHA256: strings.Repeat("d", 64), Duration: &duration}
	f := &providerResourceFixture{db: db, ctx: identity.WithResourceOwners(identity.WithOwnerID(context.Background(), owner), providerOwner, providerOwner), repo: NewProviderResourceRepository(db), job: providerresources.Job{ProviderID: "mock", ConnectionID: "mock", Revision: "preserved-canonical-resource-revision", Intent: providerresources.Intent{Kind: "voice", Name: "Preserved uncertain voice", Sources: []core.ResourceSourceEvidence{{ReferenceID: source.ReferenceID, MediaType: "video", SHA256: source.SHA256, Duration: &duration}}}}}
	creation := insertPaidResourceTask(t, f, resourcePaidTaskOptions("element", "video_character", source))
	tx, err := db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources := []core.ProviderResource{{Kind: "element", Subtype: "video_character", RemoteID: "private-preserved-element-" + uuid.NewString(), Status: core.ProviderSucceeded, BoundVoice: &core.ProviderResource{Kind: "voice", RemoteID: "private-preserved-auto-voice-" + uuid.NewString(), Status: core.ProviderSucceeded}}}
	refs, err := captureTaskResourceBindings(f.ctx, tx, creation, resources)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(refs)
	if _, err = tx.Exec(`UPDATE tasks SET status='succeeded',result_resource_bindings=$2 WHERE id=$1`, creation, encoded); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshots, err := providerresources.New(f.repo, f.scope).ResolveResourceReferences(f.ctx, "mock", refs)
	if err != nil {
		t.Fatal(err)
	}
	consumer := uuid.NewString()
	options, _ := json.Marshal(core.MediaOptions{ResourceReferences: refs, ResourceSnapshots: snapshots})
	if _, err = db.Exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,status,prompt,request_options,provider_identity,template_snapshot) VALUES($1,$2,$3,$2,'mock','preservation-consumer','queued','Retained snapshot',$4,$5,'{"purpose":"old-writer-preservation"}')`, consumer, owner, providerOwner, options, f.job.Revision); err != nil {
		t.Fatal(err)
	}
	// Keep a nonterminal private handle and immutable local-source intent too.
	j, created, err := f.repo.ReserveCreate(f.ctx, f.job, "preserved-uncertain-"+creation)
	if err != nil || !created {
		t.Fatal(j, created, err)
	}
	if _, started, e := f.repo.StartJob(f.ctx, j.ID); e != nil || !started {
		t.Fatal(started, e)
	}
	if err = f.repo.SaveHandle(f.ctx, j.ID, "private-preserved-signed-handle", ""); err != nil {
		t.Fatal(err)
	}
	return resourcePreservationSeed{creationTask: creation, consumerTask: consumer, owner: owner, providerOwner: providerOwner}
}
func assertResourceMetadataSurvivesOldWriter(t *testing.T, db *sql.DB, seed resourcePreservationSeed) {
	t.Helper()
	projection := newResourceLegacyProjection(t, db, "provider_resources", "provider_resource_jobs")
	before := projection.snapshot(t, db, "84-retained-before-schema81-write")
	var prompt, protectedBefore string
	if err := db.QueryRow(`SELECT prompt,jsonb_build_object('bindings',result_resource_bindings,'options',request_options,'template',template_snapshot,'artifacts',result_artifacts)::text FROM tasks WHERE id=$1`, seed.consumerTask).Scan(&prompt, &protectedBefore); err != nil {
		t.Fatal(err)
	}
	// Keep an actual old-column change through re-upgrade. New opaque data must
	// remain unchanged while a schema-81 writer edits its supported field.
	updatedPrompt := prompt + " [schema-81 edit]"
	if _, err := db.Exec(`UPDATE tasks SET prompt=$2 WHERE id=$1`, seed.consumerTask, updatedPrompt); err != nil {
		t.Fatal(err)
	}
	var changed, protectedAfter string
	if err := db.QueryRow(`SELECT prompt,jsonb_build_object('bindings',result_resource_bindings,'options',request_options,'template',template_snapshot,'artifacts',result_artifacts)::text FROM tasks WHERE id=$1`, seed.consumerTask).Scan(&changed, &protectedAfter); err != nil {
		t.Fatal(err)
	}
	if changed != updatedPrompt || protectedAfter != protectedBefore {
		t.Fatal("old-column write damaged accepted opaque binding/snapshot metadata")
	}
	// A schema-81 INSERT cannot name the future column. Its retained default must
	// still be [] even while populated resource rows and snapshots coexist.
	oldTask := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,status,prompt) VALUES($1,$2,$3,$2,'mock','old-writer-model','queued','Old writer fixture')`, oldTask, seed.owner, seed.providerOwner); err != nil {
		t.Fatal(err)
	}
	var defaults string
	if err := db.QueryRow(`SELECT result_resource_bindings::text FROM tasks WHERE id=$1`, oldTask).Scan(&defaults); err != nil || defaults != "[]" {
		t.Fatal("old writer did not receive additive empty default", err)
	}
	if projection.snapshot(t, db, "84-retained-after-schema81-write") != before {
		t.Fatal("schema-81 writer changed a resource binding/job field or row count")
	}
}
func TestProviderResourceCombinedRollbackPreservesAllFieldsAndDefaults(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 81); err != nil {
		t.Fatal(err)
	}
	owner, powner := uuid.NewString(), uuid.NewString()
	id := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,status,prompt,request_options) VALUES($1,$2,$3,$2,'mock','legacy-model','succeeded','Legacy row kept exactly','{"seed":17}')`, id, owner, powner); err != nil {
		t.Fatal(err)
	}
	legacy := newResourceLegacyProjection(t, db, "tasks")
	before := legacy.snapshot(t, db, "81-before-upgrade")
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if legacy.snapshot(t, db, "84-after-upgrade") != before {
		t.Fatal("084 changed an old task field or count")
	}
	assertResourceBindingsEmptyDefault(t, db)
	seed := seedResourcePreservationMetadata(t, db, owner, powner)
	full := newResourceLegacyProjection(t, db, "tasks", "provider_resources", "provider_resource_jobs")
	for round := 0; round < 2; round++ {
		prior := full.snapshot(t, db, "84-populated-before-down")
		if err := goose.DownTo(db, "migrations", 81); err != nil {
			t.Fatal(err)
		}
		if full.snapshot(t, db, "81-retained-after-down") != prior {
			t.Fatal("84-to-81 rollback erased populated resource identity or snapshots")
		}
		assertResourceMetadataSurvivesOldWriter(t, db, seed)
		afterOldWrite := full.snapshot(t, db, "81-after-persisted-old-writes")
		if err := goose.UpTo(db, "migrations", 84); err != nil {
			t.Fatal(err)
		}
		if full.snapshot(t, db, "84-populated-after-up") != afterOldWrite {
			t.Fatal("81-to-84 upgrade rewrote retained metadata or counts")
		}
	}
}
