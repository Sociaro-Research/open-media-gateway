package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/google/uuid"
)

type lookupDirectory struct {
	provider core.MediaProvider
	err      error
}

func (d *lookupDirectory) Provider(context.Context, string) (core.MediaProvider, error) {
	return d.provider, d.err
}
func (d *lookupDirectory) Providers(context.Context) (map[string]core.MediaProvider, error) {
	return nil, d.err
}

type lookupProvider struct{ created, polls int }

func (*lookupProvider) Name() string { return "mock" }
func (p *lookupProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	p.created++
	return core.ProviderTask{ID: "new-provider-job", ResultURL: "https://example.test/result"}, nil
}
func (p *lookupProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	p.polls++
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: "https://example.test/result"}, nil
}

func TestWorkerProviderLookupFailureRecoversWithoutResubmission(t *testing.T) {
	for _, status := range []core.TaskStatus{core.TaskQueued, core.TaskRunning} {
		t.Run(string(status), func(t *testing.T) {
			ctx, db, repo, q, client, prefix := operationTestServices(t)
			now := time.Now().UTC()
			webhook := "https://example.test/events"
			task := core.Task{ID: uuid.NewString(), Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "lookup recovery", CreatedAt: now, UpdatedAt: now, WebhookURL: &webhook}
			if _, _, err := repo.CreateOrGet(ctx, task, "", "fixture"); err != nil {
				t.Fatal(err)
			}
			if status == core.TaskRunning {
				if err := repo.MarkRunning(ctx, task.ID); err != nil {
					t.Fatal(err)
				}
				if err := repo.SetProviderTaskID(ctx, task.ID, "accepted-provider-job"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := repo.Get(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			provider := &lookupProvider{}
			lookupErr := errors.New("temporary provider settings read failure")
			directory := &lookupDirectory{provider: provider, err: lookupErr}
			w := New(repo, q, provider, &storageStub{}, time.Second, time.Minute, 2, time.Second)
			w.Directory = directory
			if err := q.Enqueue(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			claim := func() error {
				t.Helper()
				claimCtx := core.WithClaimToken(ctx, uuid.NewString())
				id, err := q.Dequeue(claimCtx)
				if err != nil || id != task.ID {
					t.Fatalf("delivery = %s, %v", id, err)
				}
				return w.runClaim(claimCtx, id, repo, q)
			}
			claimErr := claim()
			after, err := repo.Get(ctx, task.ID)
			if !errors.Is(claimErr, lookupErr) {
				t.Fatalf("lookup failure must be retryable: error=%v status=%s task_error=%v", claimErr, after.Status, after.Error)
			}
			if err != nil || after.Status != before.Status || after.Error != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("lookup failure changed durable task: %#v, %v", after, err)
			}
			if status == core.TaskRunning && (after.ProviderTaskID == nil || *after.ProviderTaskID != *before.ProviderTaskID || !after.WorkerStartedAt.Equal(*before.WorkerStartedAt)) {
				t.Fatal("lookup failure lost accepted provider job or reset its deadline")
			}
			if provider.created != 0 || provider.polls != 0 {
				t.Fatal("provider called before lookup recovered")
			}
			var claims, failures int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE worker_token IS NOT NULL`).Scan(&claims); err != nil || claims != 0 {
				t.Fatalf("claim not released: %d, %v", claims, err)
			}
			if n, err := client.ZCard(ctx, prefix+"leases").Result(); err != nil || n != 0 {
				t.Fatalf("delivery not requeued: leases=%d, %v", n, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM webhook_deliveries WHERE event_type='task.failed'`).Scan(&failures); err != nil || failures != 0 {
				t.Fatalf("lookup emitted a terminal failure: %d, %v", failures, err)
			}
			directory.err = nil
			if err := claim(); err != nil {
				t.Fatal(err)
			}
			after, err = repo.Get(ctx, task.ID)
			if err != nil || after.Status != core.TaskSucceeded {
				t.Fatalf("task did not recover: %#v, %v", after, err)
			}
			// A duplicate delivery after completion must not repeat any operation.
			if err := q.Enqueue(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			if err := claim(); err != nil {
				t.Fatal(err)
			}
			wantSubmissions := 0
			if status == core.TaskQueued {
				wantSubmissions = 1
			}
			if provider.created != wantSubmissions || provider.polls != 1-wantSubmissions {
				t.Fatalf("operations: submissions=%d polls=%d", provider.created, provider.polls)
			}
			var completed int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM webhook_deliveries WHERE event_type='task.succeeded'`).Scan(&completed); err != nil || completed != 1 {
				t.Fatalf("completion events=%d, %v", completed, err)
			}
		})
	}
}

func TestWorkerMissingProviderIsTerminal(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		t.Run(fmt.Sprint(dynamic), func(t *testing.T) {
			repo := &repoStub{task: core.Task{ID: "missing", Status: core.TaskQueued, Provider: "missing"}}
			w := &Worker{repo: repo}
			if dynamic {
				w.Directory = &lookupDirectory{err: fmt.Errorf("connection missing: %w", core.ErrProviderUnavailable)}
			}
			if err := w.process(context.Background(), repo.task.ID); err != nil || repo.task.Status != core.TaskFailed {
				t.Fatalf("missing provider must fail explicitly: %s, %v", repo.task.Status, err)
			}
		})
	}
}
