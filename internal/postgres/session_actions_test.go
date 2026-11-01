package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
)

func sessionActionBinding(t *testing.T) (*providerResourceFixture, providerresources.Binding) {
	t.Helper()
	f := newProviderResourceFixture(t)
	f.job.Intent.Kind = "editing_session"
	f.job.Intent.Subtype = "selection"
	f.job.Intent.Name = "Owned editing session"
	j := f.start(t, "init-session")
	s := &core.MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceSHA256: f.job.Intent.Sources[0].SHA256, SourceVideoDuration: 4, FrameCount: 120, FPS: 30}
	b, e := f.repo.CompleteResource(f.ctx, j.ID, core.ProviderResource{Kind: "editing_session", Subtype: "selection", RemoteID: "private-editing-native-id", Status: core.ProviderSucceeded, Session: s})
	if e != nil {
		t.Fatal(e)
	}
	return f, b
}
func sessionActionTask(f *providerResourceFixture, b providerresources.Binding, action string) core.Task {
	now := time.Now().UTC()
	intent := &core.SessionActionIntent{BindingID: b.ID, Action: action, ExpectedRevision: b.Session.SelectionRevision}
	if action == "add" || action == "delete" {
		intent.Frame = &core.FrameSelection{FrameIndex: 0, Points: []core.SelectionPoint{{X: .25, Y: .5}}}
	}
	o := core.MediaOptions{SessionAction: intent, ResourceReferences: []core.ResourceReference{{BindingID: b.ID, ReferenceID: "session"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: b.ID, ReferenceID: "session", Kind: b.Kind, Subtype: b.Subtype, RemoteID: b.RemoteID, IsPreset: b.IsPreset, ConnectionIdentity: b.Revision, Sources: providerresources.CloneSources(b.Sources), Session: providerresources.CloneSession(b.Session)}}}
	return core.Task{ID: uuid.NewString(), OwnerID: b.OwnerID, ProviderOwnerID: b.ProviderOwnerID, StorageOwnerID: b.OwnerID, Provider: b.ProviderID, ProviderIdentity: b.Revision, Model: "reviewed-session-" + action, MediaType: "json", Prompt: "", Options: o, Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now}
}
func sessionActionAdmit(t *testing.T, f *providerResourceFixture, task core.Task, key string) core.Task {
	t.Helper()
	out, created, e := NewTaskRepository(f.db).CreateOrGet(f.ctx, task, key, key)
	if e != nil || !created {
		t.Fatal("admit", out, created, e)
	}
	return out
}
func sessionActionResult(b providerresources.Binding, action string) core.ProviderResource {
	s := providerresources.CloneSession(b.Session)
	s.SelectionRevision++
	if action == "clear" {
		s.Selections = nil
		s.HasSelection = false
	} else {
		s.Selections = []core.FrameSelection{{FrameIndex: 0, Points: []core.SelectionPoint{{X: .25, Y: .5}}}}
		s.HasSelection = true
	}
	return core.ProviderResource{RemoteID: b.RemoteID, Kind: b.Kind, Subtype: b.Subtype, Status: core.ProviderSucceeded, Session: s}
}

func TestSessionActionAdmissionSerializesAndReplays(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	task := sessionActionTask(f, b, "add")
	accepted := sessionActionAdmit(t, f, task, "session-once")
	live, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || live.Status != providerresources.Running {
		t.Fatal("accepted mutation did not reserve its binding", live, e)
	}
	if _, created, e := repo.CreateOrGet(f.ctx, sessionActionTask(f, b, "add"), "session-other", "session-other"); e == nil || created {
		t.Fatal("simultaneous session mutation admitted", created, e)
	}
	replay := task
	replay.ID = uuid.NewString()
	replay.Options.ResourceSnapshots = nil
	replay.AdmissionError = errors.New("mutable resolver is unavailable")
	old, created, e := repo.CreateOrGet(f.ctx, replay, "session-once", "session-once")
	if e != nil || created || old.ID != accepted.ID {
		t.Fatal("accepted replay revalidated session", old, created, e)
	}
	var n int
	if e = f.db.QueryRow(`SELECT count(*) FROM tasks WHERE owner_id=$1`, b.OwnerID).Scan(&n); e != nil || n != 1 {
		t.Fatal("rejected reservation persisted", n, e)
	}
}

func TestSessionActionCompletionUpdatesSameBindingAndPreservesScope(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	task := sessionActionAdmit(t, f, sessionActionTask(f, b, "add"), "add")
	if e := repo.MarkRunning(f.ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	r := sessionActionResult(b, "add")
	if e := repo.MarkSucceeded(f.ctx, task.ID, core.StoredMedia{Resources: []core.ProviderResource{r}}, ""); e != nil {
		t.Fatal("complete mutation", e)
	}
	got, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || got.Status != providerresources.Succeeded || got.Session.SelectionRevision != 1 || !got.Session.HasSelection {
		t.Fatal(got, e)
	}
	if got.RemoteID != b.RemoteID || got.OriginTaskID != b.OriginTaskID || got.Revision != b.Revision || !providerresources.EqualSources(got.Sources, b.Sources) || got.Session.SourceSHA256 != b.Session.SourceSHA256 || !got.Session.ExpiresAt.Equal(b.Session.ExpiresAt) {
		t.Fatal("session mutation replaced native/source identity", got)
	}
	stored, e := repo.Get(f.ctx, task.ID)
	if e != nil || len(stored.StoredResourceReferences) != 1 || stored.StoredResourceReferences[0].BindingID != b.ID {
		t.Fatal("completion created different resource", stored, e)
	}
	var count int
	if e = f.db.QueryRow(`SELECT count(*) FROM provider_resources WHERE owner_id=$1`, b.OwnerID).Scan(&count); e != nil || count != 1 {
		t.Fatal("session mutation duplicated a binding", count, e)
	}
	// Restarted repository sees the same revision and can reserve the next action.
	restarted := NewTaskRepository(f.db)
	next := sessionActionTask(f, got, "clear")
	if _, created, e := restarted.CreateOrGet(f.ctx, next, "clear", "clear"); e != nil || !created {
		t.Fatal("next revision unavailable", created, e)
	}
}

func TestSessionActionPreviewDoesNotMutateOrCreateBindings(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	task := sessionActionAdmit(t, f, sessionActionTask(f, b, "preview"), "preview")
	got, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || got.Status != providerresources.Succeeded || got.Session.SelectionRevision != 0 {
		t.Fatal("preview reserved mutable state", got, e)
	}
	if _, created, e := repo.CreateOrGet(f.ctx, sessionActionTask(f, b, "add"), "during-preview", "during-preview"); e == nil || created {
		t.Fatal("mutation overlapped queued preview", created, e)
	}
	if e = repo.MarkRunning(f.ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	if e = repo.MarkSucceeded(f.ctx, task.ID, core.StoredMedia{}, ""); e != nil {
		t.Fatal(e)
	}
	stored, e := repo.Get(f.ctx, task.ID)
	if e != nil || len(stored.StoredResourceReferences) != 0 {
		t.Fatal("preview created resource", stored, e)
	}
	if _, created, e := repo.CreateOrGet(f.ctx, sessionActionTask(f, b, "add"), "after-preview", "after-preview"); e != nil || !created {
		t.Fatal("completed preview retained reservation", created, e)
	}
}

func TestSessionActionFailureRequiresExplicitRecoveryClear(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	task := sessionActionAdmit(t, f, sessionActionTask(f, b, "add"), "uncertain")
	if e := repo.MarkRunning(f.ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	if e := repo.MarkFailed(f.ctx, task.ID, "provider outcome uncertain"); e != nil {
		t.Fatal(e)
	}
	failed, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || failed.Status != providerresources.Failed || failed.Session.SelectionRevision != 0 {
		t.Fatal("unknown mutation silently restored ready state", failed, e)
	}
	for _, action := range []string{"add", "preview", "clear"} {
		next := sessionActionTask(f, failed, action)
		if _, created, e := repo.CreateOrGet(f.ctx, next, "blocked-"+action, "blocked-"+action); e == nil || created {
			t.Fatal("failed session reused without explicit recovery", action, created, e)
		}
	}
	recover := sessionActionTask(f, failed, "clear")
	recover.Options.SessionAction.Recover = true
	recover = sessionActionAdmit(t, f, recover, "recover-clear")
	if e = repo.MarkRunning(f.ctx, recover.ID); e != nil {
		t.Fatal(e)
	}
	if e = repo.MarkSucceeded(f.ctx, recover.ID, core.StoredMedia{Resources: []core.ProviderResource{sessionActionResult(failed, "clear")}}, ""); e != nil {
		t.Fatal(e)
	}
	ready, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || ready.Status != providerresources.Succeeded || ready.Session.SelectionRevision != 1 || ready.Session.HasSelection {
		t.Fatal("clear recovery did not establish next revision", ready, e)
	}
}

func TestSessionActionAdmissionRejectsStaleChangedForeignAndActiveUse(t *testing.T) {
	for _, tc := range []string{"stale revision", "changed snapshot", "foreign owner", "foreign provider owner", "changed connection", "active generation"} {
		t.Run(tc, func(t *testing.T) {
			f, b := sessionActionBinding(t)
			repo := NewTaskRepository(f.db)
			task := sessionActionTask(f, b, "add")
			ctx := f.ctx
			switch tc {
			case "stale revision":
				task.Options.SessionAction.ExpectedRevision = 1
			case "changed snapshot":
				task.Options.ResourceSnapshots[0].Session.FPS = 60
			case "foreign owner":
				ctx = identity.WithResourceOwners(identity.WithOwnerID(context.Background(), uuid.NewString()), b.ProviderOwnerID, b.OwnerID)
				task.OwnerID = identity.OwnerID(ctx)
			case "foreign provider owner":
				ctx = identity.WithResourceOwners(identity.WithOwnerID(context.Background(), b.OwnerID), uuid.NewString(), b.OwnerID)
				task.ProviderOwnerID = identity.ProviderOwnerID(ctx)
			case "changed connection":
				task.Provider = "other-actual-connection"
			case "active generation":
				active := sessionActionTask(f, b, "preview")
				active.Options.SessionAction = nil
				sessionActionAdmit(t, f, active, "active-render")
			}
			if _, created, e := repo.CreateOrGet(ctx, task, "reject", "reject"); e == nil || created {
				t.Fatal("invalid session action admitted", tc, created, e)
			}
			got, e := f.repo.GetBinding(f.ctx, b.ID)
			if e != nil || got.Status != providerresources.Succeeded || got.Session.SelectionRevision != 0 {
				t.Fatal("rejection changed binding", got, e)
			}
		})
	}
}

func TestSessionActionConcurrentMutationsHaveOneWinner(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			_, _, e := repo.CreateOrGet(f.ctx, sessionActionTask(f, b, "add"), fmt.Sprint("concurrent-", i), fmt.Sprint("concurrent-", i))
			results <- e
		}(i)
	}
	close(start)
	group.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("session writes were not serialized", wins)
	}
}

func TestSessionActionRejectsMalformedCompletionWithoutPartialUpdate(t *testing.T) {
	for _, tc := range []string{"missing resource", "wrong native ID", "changed source", "changed expiry", "wrong next revision", "eleven frames", "eleven points", "preview resource"} {
		t.Run(tc, func(t *testing.T) {
			f, b := sessionActionBinding(t)
			repo := NewTaskRepository(f.db)
			action := "add"
			if tc == "preview resource" {
				action = "preview"
			}
			task := sessionActionAdmit(t, f, sessionActionTask(f, b, action), "malformed")
			if e := repo.MarkRunning(f.ctx, task.ID); e != nil {
				t.Fatal(e)
			}
			result := sessionActionResult(b, "add")
			resources := []core.ProviderResource{result}
			switch tc {
			case "missing resource":
				resources = nil
			case "wrong native ID":
				resources[0].RemoteID = "different-native-session"
			case "changed source":
				resources[0].Session.SourceSHA256 = strings.Repeat("d", 64)
			case "changed expiry":
				resources[0].Session.ExpiresAt = resources[0].Session.ExpiresAt.Add(time.Hour)
			case "wrong next revision":
				resources[0].Session.SelectionRevision = 3
			case "eleven frames":
				for i := 1; i < 11; i++ {
					resources[0].Session.Selections = append(resources[0].Session.Selections, core.FrameSelection{FrameIndex: i, Points: []core.SelectionPoint{{X: .3, Y: .4}}})
				}
			case "eleven points":
				for i := 1; i < 11; i++ {
					resources[0].Session.Selections[0].Points = append(resources[0].Session.Selections[0].Points, core.SelectionPoint{X: float64(i) / 20, Y: .4})
				}
			}
			if e := repo.MarkSucceeded(f.ctx, task.ID, core.StoredMedia{Resources: resources}, ""); e == nil {
				t.Fatal("unverified mutation became terminal")
			}
			got, e := f.repo.GetBinding(f.ctx, b.ID)
			want := providerresources.Running
			if action == "preview" {
				want = providerresources.Succeeded
			}
			if e != nil || got.Status != want || got.Session.SelectionRevision != 0 {
				t.Fatal("failed completion changed session", got, e)
			}
			var n int
			if e = f.db.QueryRow(`SELECT count(*) FROM provider_resources WHERE owner_id=$1`, b.OwnerID).Scan(&n); e != nil || n != 1 {
				t.Fatal("invalid completion created another binding", n, e)
			}
		})
	}
}

// Compile-time JSON roundtrip ensures the private snapshot and public intent
// retain separate expected and accepted revisions in task persistence.
func TestSessionActionIntentJSONRetainsExpectedRevision(t *testing.T) {
	o := core.MediaOptions{SessionAction: &core.SessionActionIntent{BindingID: uuid.NewString(), Action: "clear", ExpectedRevision: 7, Recover: true}}
	raw, e := json.Marshal(o)
	if e != nil {
		t.Fatal(e)
	}
	var got core.MediaOptions
	if e = json.Unmarshal(raw, &got); e != nil || got.SessionAction == nil || got.SessionAction.ExpectedRevision != 7 || !got.SessionAction.Recover {
		t.Fatal(got, e)
	}
}

func TestSessionActionReservationRollsBackWithRejectedAdmission(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	task := sessionActionTask(f, b, "add")
	task.RequireCost = true
	if _, created, e := repo.CreateOrGet(f.ctx, task, "unpriced", "unpriced"); !errors.Is(e, core.ErrPricingUnavailable) || created {
		t.Fatal("unpriced session action admitted", created, e)
	}
	got, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || got.Status != providerresources.Succeeded || got.Session.SelectionRevision != 0 {
		t.Fatal("failed admission retained reservation", got, e)
	}
	var n int
	if e = f.db.QueryRow(`SELECT count(*) FROM tasks WHERE owner_id=$1`, b.OwnerID).Scan(&n); e != nil || n != 0 {
		t.Fatal("failed admission retained task", n, e)
	}
	if _, created, e := repo.CreateOrGet(f.ctx, sessionActionTask(f, b, "add"), "next", "next"); e != nil || !created {
		t.Fatal("rolled-back reservation blocked next admission", created, e)
	}
}

func TestSessionActionCaptureRollbackRetainsReservedRevision(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	task := sessionActionAdmit(t, f, sessionActionTask(f, b, "add"), "capture-rollback")
	if e := repo.MarkRunning(f.ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	tx, e := f.db.BeginTx(f.ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	refs, e := captureTaskResourceBindings(f.ctx, tx, task.ID, []core.ProviderResource{sessionActionResult(b, "add")})
	if e != nil || len(refs) != 1 || refs[0].BindingID != b.ID {
		tx.Rollback()
		t.Fatal(refs, e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	got, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || got.Status != providerresources.Running || got.Session.SelectionRevision != 0 {
		t.Fatal("rolled-back completion changed session", got, e)
	}
	if e = repo.MarkSucceeded(f.ctx, task.ID, core.StoredMedia{Resources: []core.ProviderResource{sessionActionResult(b, "add")}}, ""); e != nil {
		t.Fatal("real completion after rollback failed", e)
	}
}

func TestSessionActionStaleLeaseCannotCompleteOrInvalidateSuccessor(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	task := sessionActionAdmit(t, f, sessionActionTask(f, b, "add"), "claim")
	stale := core.WithClaimToken(f.ctx, uuid.NewString())
	successor := core.WithClaimToken(f.ctx, uuid.NewString())
	if ok, e := repo.AcquireTask(stale, task.ID, time.Minute); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if e := repo.MarkRunning(stale, task.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`UPDATE tasks SET worker_lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	result := core.StoredMedia{Resources: []core.ProviderResource{sessionActionResult(b, "add")}}
	if e := repo.MarkSucceeded(stale, task.ID, result, ""); !errors.Is(e, core.ErrLeaseLost) {
		t.Fatal("expired worker completed mutation", e)
	}
	if e := repo.MarkFailed(stale, task.ID, "stale failure"); !errors.Is(e, core.ErrLeaseLost) {
		t.Fatal("expired worker invalidated session", e)
	}
	if ok, e := repo.AcquireTask(successor, task.ID, time.Minute); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if e := repo.MarkSucceeded(successor, task.ID, result, ""); e != nil {
		t.Fatal(e)
	}
	if e := repo.MarkFailed(stale, task.ID, "late stale failure"); !errors.Is(e, core.ErrLeaseLost) {
		t.Fatal("stale failure bypassed claim fence", e)
	}
	got, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || got.Status != providerresources.Succeeded || got.Session.SelectionRevision != 1 {
		t.Fatal("stale worker overwrote successor selection", got, e)
	}
}

func TestSessionActionInitRequiresTypedInspectedSource(t *testing.T) {
	for _, tc := range []string{"valid", "wrong hash", "wrong duration", "wrong frames", "wrong FPS", "wrong subtype", "missing inspected source"} {
		t.Run(tc, func(t *testing.T) {
			f := newProviderResourceFixture(t)
			repo := NewTaskRepository(f.db)
			ref := uuid.NewString()
			duration := 4.
			public := &core.ResourceIntent{Kind: "editing_session", Subtype: "selection", Name: "Initialized session", ReferenceIDs: []string{ref}}
			private := &core.ResourceCreateRequest{Kind: public.Kind, Subtype: public.Subtype, Name: public.Name, Videos: []core.ResourceSourceInput{{ReferenceID: ref, MediaType: "video", ContentType: "video/mp4", SizeBytes: 1000, Width: 640, Height: 360, FrameCount: 120, FPS: 30, SHA256: strings.Repeat("b", 64), Duration: &duration}}}
			now := time.Now().UTC()
			task := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(f.ctx), ProviderOwnerID: identity.ProviderOwnerID(f.ctx), StorageOwnerID: identity.OwnerID(f.ctx), Provider: f.job.ProviderID, ProviderIdentity: f.job.Revision, Model: "reviewed-session-init", MediaType: "json", Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now, Options: core.MediaOptions{ResourceIntent: public, ResourceCreation: private}}
			if tc == "wrong subtype" {
				public.Subtype = "arbitrary"
				private.Subtype = "arbitrary"
			}
			if tc == "missing inspected source" {
				private.Videos = nil
			}
			accepted, created, e := repo.CreateOrGet(f.ctx, task, "init", "init")
			if tc == "wrong subtype" || tc == "missing inspected source" {
				if e == nil || created {
					t.Fatal("untyped session init admitted", created, e)
				}
				return
			}
			if e != nil || !created {
				t.Fatal("typed init rejected", accepted, e)
			}
			if e = repo.MarkRunning(f.ctx, task.ID); e != nil {
				t.Fatal(e)
			}
			session := &core.MediaSessionEvidence{ExpiresAt: now.Add(24 * time.Hour), SourceSHA256: private.Videos[0].SHA256, SourceVideoDuration: duration, FrameCount: 120, FPS: 30}
			switch tc {
			case "wrong hash":
				session.SourceSHA256 = strings.Repeat("c", 64)
			case "wrong duration":
				session.SourceVideoDuration = 5
			case "wrong frames":
				session.FrameCount = 119
			case "wrong FPS":
				session.FPS = 24
			}
			result := core.StoredMedia{Resources: []core.ProviderResource{{Kind: "editing_session", Subtype: "selection", RemoteID: "private-new-native-session", Status: core.ProviderSucceeded, Session: session}}}
			e = repo.MarkSucceeded(f.ctx, task.ID, result, "")
			if tc == "valid" {
				if e != nil {
					t.Fatal(e)
				}
				stored, e := repo.Get(f.ctx, task.ID)
				if e != nil || len(stored.StoredResourceReferences) != 1 {
					t.Fatal(stored, e)
				}
				b, e := f.repo.GetBinding(f.ctx, stored.StoredResourceReferences[0].BindingID)
				if e != nil || b.OriginTaskID != task.ID || b.Session.FrameCount != 120 || b.Sources[0].ReferenceID != ref {
					t.Fatal("init lost source evidence", b, e)
				}
			} else {
				if e == nil {
					t.Fatal("unverified init metadata became reusable")
				}
				var n int
				if e = f.db.QueryRow(`SELECT count(*) FROM provider_resources WHERE origin_task_id=$1`, task.ID).Scan(&n); e != nil || n != 0 {
					t.Fatal("invalid init created reusable binding", n, e)
				}
			}
		})
	}
}

func TestSessionActionCompletionMustMatchExactRequestedTransform(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	task := sessionActionAdmit(t, f, sessionActionTask(f, b, "add"), "exact-mask")
	if e := repo.MarkRunning(f.ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	wrong := sessionActionResult(b, "add")
	wrong.Session.Selections[0].Points[0].X = .75
	if e := repo.MarkSucceeded(f.ctx, task.ID, core.StoredMedia{Resources: []core.ProviderResource{wrong}}, ""); e == nil {
		t.Fatal("provider published a different selected point at the expected next revision")
	}
	got, e := f.repo.GetBinding(f.ctx, b.ID)
	if e != nil || got.Status != providerresources.Running || got.Session.SelectionRevision != 0 {
		t.Fatal("rejected transform changed session", got, e)
	}
}

func TestSessionActionAddDeletePreservesExactRemainingPoints(t *testing.T) {
	f, b := sessionActionBinding(t)
	repo := NewTaskRepository(f.db)
	complete := func(task core.Task, result core.ProviderResource, key string) providerresources.Binding {
		t.Helper()
		task = sessionActionAdmit(t, f, task, key)
		if e := repo.MarkRunning(f.ctx, task.ID); e != nil {
			t.Fatal(e)
		}
		if e := repo.MarkSucceeded(f.ctx, task.ID, core.StoredMedia{Resources: []core.ProviderResource{result}}, ""); e != nil {
			t.Fatal(key, e)
		}
		next, e := f.repo.GetBinding(f.ctx, b.ID)
		if e != nil {
			t.Fatal(e)
		}
		return next
	}
	b = complete(sessionActionTask(f, b, "add"), sessionActionResult(b, "add"), "first-point")
	second := sessionActionTask(f, b, "add")
	second.Options.SessionAction.Frame.Points = []core.SelectionPoint{{X: .75, Y: .5}}
	added := sessionActionResult(b, "add")
	added.Session.Selections[0].Points = []core.SelectionPoint{{X: .25, Y: .5}, {X: .75, Y: .5}}
	b = complete(second, added, "second-point")
	remove := sessionActionTask(f, b, "delete")
	remaining := sessionActionResult(b, "add")
	remaining.Session.Selections[0].Points = []core.SelectionPoint{{X: .75, Y: .5}}
	b = complete(remove, remaining, "remove-first-point")
	if b.Session.SelectionRevision != 3 || len(b.Session.Selections) != 1 || len(b.Session.Selections[0].Points) != 1 || b.Session.Selections[0].Points[0].X != .75 {
		t.Fatal("delete discarded or altered another point", b.Session)
	}
	last := sessionActionTask(f, b, "delete")
	last.Options.SessionAction.Frame.Points = []core.SelectionPoint{{X: .75, Y: .5}}
	empty := sessionActionResult(b, "clear")
	b = complete(last, empty, "remove-last-point")
	if b.Session.SelectionRevision != 4 || b.Session.HasSelection || len(b.Session.Selections) != 0 {
		t.Fatal("last point deletion retained phantom selection", b.Session)
	}
}

func TestSessionActionImpossibleTransformsRejectBeforeReservation(t *testing.T) {
	for _, tc := range []string{"delete absent frame", "delete absent point", "add existing point", "eleventh frame", "eleventh point"} {
		t.Run(tc, func(t *testing.T) {
			f, b := sessionActionBinding(t)
			if tc != "delete absent frame" {
				b.Session.HasSelection = true
				b.Session.Selections = []core.FrameSelection{{FrameIndex: 0, Points: []core.SelectionPoint{{X: .25, Y: .5}}}}
			}
			if tc == "eleventh frame" {
				for i := 1; i < 10; i++ {
					b.Session.Selections = append(b.Session.Selections, core.FrameSelection{FrameIndex: i, Points: []core.SelectionPoint{{X: .25, Y: .5}}})
				}
			}
			if tc == "eleventh point" {
				for i := 1; i < 10; i++ {
					b.Session.Selections[0].Points = append(b.Session.Selections[0].Points, core.SelectionPoint{X: float64(i) / 20, Y: .5})
				}
			}
			raw, _ := json.Marshal(b.Session)
			if _, e := f.db.Exec(`UPDATE provider_resources SET session=$2 WHERE id=$1`, b.ID, raw); e != nil {
				t.Fatal(e)
			}
			action := "add"
			if strings.HasPrefix(tc, "delete") {
				action = "delete"
			}
			task := sessionActionTask(f, b, action)
			if tc == "delete absent point" {
				task.Options.SessionAction.Frame.Points = []core.SelectionPoint{{X: .99, Y: .5}}
			}
			if tc == "eleventh frame" {
				task.Options.SessionAction.Frame.FrameIndex = 10
			}
			if tc == "eleventh point" {
				task.Options.SessionAction.Frame.Points = []core.SelectionPoint{{X: .99, Y: .5}}
			}
			if _, created, e := NewTaskRepository(f.db).CreateOrGet(f.ctx, task, "impossible", "impossible"); e == nil || created {
				t.Fatal("impossible mutation reserved paid work", tc, created, e)
			}
			got, e := f.repo.GetBinding(f.ctx, b.ID)
			if e != nil || got.Status != providerresources.Succeeded || !providerresources.EqualSession(got.Session, b.Session) {
				t.Fatal("rejected transform changed session", got, e)
			}
		})
	}
}
