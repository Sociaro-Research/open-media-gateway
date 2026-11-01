package worker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type repoStub struct{ task core.Task }

func (r *repoStub) CreateOrGet(context.Context, core.Task, string, string) (core.Task, bool, error) {
	return r.task, true, nil
}
func (r *repoStub) Get(context.Context, string) (core.Task, error) { return r.task, nil }
func (r *repoStub) ListRecent(context.Context, int) ([]core.Task, error) {
	return []core.Task{r.task}, nil
}
func (r *repoStub) MarkEnqueued(context.Context, string) error { return nil }
func (r *repoStub) MarkRunning(context.Context, string) error {
	r.task.Status = core.TaskRunning
	now := time.Now()
	r.task.WorkerStartedAt = &now
	return nil
}
func (r *repoStub) SetProviderTaskID(_ context.Context, _ string, id string) error {
	r.task.ProviderTaskID = &id
	return nil
}
func (r *repoStub) SetProviderUsage(context.Context, string, core.ProviderUsage) error { return nil }
func (r *repoStub) SetProviderResultURL(_ context.Context, _ string, resultURL string) error {
	r.task.ProviderResultURL = &resultURL
	return nil
}
func (r *repoStub) MarkSucceeded(_ context.Context, _ string, result core.StoredMedia, lastFrameURL string) error {
	r.task.Status = core.TaskSucceeded
	url := "/v1/generations/" + r.task.ID + "/content"
	r.task.ResultURL = &url
	r.task.StorageKey = &result.StorageKey
	if result.LastFrameStorageKey != "" {
		r.task.LastFrameStorageKey = &result.LastFrameStorageKey
	}
	if lastFrameURL != "" {
		r.task.LastFrameURL = &lastFrameURL
	}
	return nil
}
func (r *repoStub) MarkFailed(_ context.Context, _ string, message string) error {
	r.task.Status = core.TaskFailed
	r.task.Error = &message
	return nil
}
func (r *repoStub) ListRecoverable(context.Context) ([]string, error) { return nil, nil }

type providerStub struct {
	name    string
	polls   int
	created int
}

type storageStub struct {
	archivedURL string
	err         error
}

func (s *storageStub) Archive(_ context.Context, taskID, sourceURL string) (core.StoredMedia, error) {
	s.archivedURL = sourceURL
	if s.err != nil {
		return core.StoredMedia{}, s.err
	}
	content := []byte("fixture-data") // Fixed 12-byte synthetic archive.
	return core.StoredMedia{StorageKey: taskID + "/video.mp4", ContentType: "video/mp4", SizeBytes: int64(len(content)), SHA256: fmt.Sprintf("%x", sha256.Sum256(content))}, nil
}

func (p *providerStub) Name() string {
	if p.name == "" {
		return "stub"
	}
	return p.name
}
func (p *providerStub) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	p.created++
	return core.ProviderTask{ID: "provider-task"}, nil
}

func TestProcessUsesTaskProvider(t *testing.T) {
	repo := &repoStub{task: core.Task{ID: "task-2", Status: core.TaskQueued, Provider: "bytedance", Model: "model", Prompt: "prompt"}}
	mockProvider := &providerStub{name: "mock"}
	byteDanceProvider := &providerStub{name: "bytedance"}
	processor := NewWithProviders(
		repo,
		nil,
		map[string]core.MediaProvider{"mock": mockProvider, "bytedance": byteDanceProvider},
		&storageStub{},
		time.Millisecond,
		time.Second,
		2,
		time.Second,
	)
	if err := processSteps(processor, repo); err != nil {
		t.Fatal(err)
	}
	if mockProvider.created != 0 || byteDanceProvider.created != 1 {
		t.Fatalf("wrong provider used: mock=%d bytedance=%d", mockProvider.created, byteDanceProvider.created)
	}
}
func (p *providerStub) GetTask(context.Context, string) (core.ProviderResult, error) {
	p.polls++
	if p.polls == 1 {
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	}
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: "https://cdn.example/video.mp4"}, nil
}

func TestProcessQueuedTaskToSuccess(t *testing.T) {
	repo := &repoStub{task: core.Task{ID: "task-1", Status: core.TaskQueued, Model: "model", Prompt: "prompt"}}
	provider := &providerStub{}
	mediaStorage := &storageStub{}
	processor := New(repo, nil, provider, mediaStorage, time.Millisecond, time.Second, 2, time.Second)
	if err := processSteps(processor, repo); err != nil {
		t.Fatal(err)
	}
	if repo.task.Status != core.TaskSucceeded || repo.task.ResultURL == nil {
		t.Fatalf("unexpected task: %#v", repo.task)
	}
	if mediaStorage.archivedURL != "https://cdn.example/video.mp4" {
		t.Fatalf("provider result was not archived: %q", mediaStorage.archivedURL)
	}
}

type artifactStorage struct {
	cancel      context.CancelFunc
	ids         []string
	failFrame   bool
	failPrimary bool
}

func (s *artifactStorage) Archive(_ context.Context, taskID, sourceURL string) (core.StoredMedia, error) {
	s.ids = append(s.ids, taskID)
	if s.failPrimary && len(s.ids) == 1 {
		return core.StoredMedia{}, fmt.Errorf("primary upload failed")
	}
	if s.cancel != nil && len(s.ids) == 2 {
		s.cancel()
		return core.StoredMedia{}, context.Canceled
	}
	if s.failFrame && len(s.ids) == 2 {
		return core.StoredMedia{}, fmt.Errorf("frame upload failed")
	}
	return core.StoredMedia{StorageKey: "s3:connection/" + taskID + "/image.png", ContentType: "image/png"}, nil
}
func TestArchivePreservesPrimaryWhenOptionalLastFrameFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failFrame bool
	}{{name: "frame archived"}, {name: "frame download failed", failFrame: true}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repoStub{task: core.Task{ID: "frame-task", Status: core.TaskRunning}}
			dest := &artifactStorage{failFrame: tc.failFrame}
			w := &Worker{repo: repo, storage: dest}
			if err := w.archiveResult(context.Background(), context.Background(), repo.task, "https://provider/video", "https://provider/frame"); err != nil {
				t.Fatal(err)
			}
			if len(dest.ids) != 2 || dest.ids[1] != "frame-task/last-frame" {
				t.Fatal(dest.ids)
			}
			if repo.task.Status != core.TaskSucceeded || repo.task.ResultURL == nil || repo.task.StorageKey == nil || *repo.task.StorageKey == "" {
				t.Fatal("primary result was lost", repo.task)
			}
			if tc.failFrame {
				if repo.task.LastFrameStorageKey != nil || repo.task.LastFrameURL != nil {
					t.Fatal("invalid optional frame was published")
				}
			} else if repo.task.LastFrameStorageKey == nil || *repo.task.LastFrameStorageKey == "" || repo.task.LastFrameURL == nil {
				t.Fatal("valid last frame not archived")
			}
		})
	}
}

func TestPrimaryArchiveFailureStillFailsTask(t *testing.T) {
	repo := &repoStub{task: core.Task{ID: "primary-task", Status: core.TaskRunning}}
	dest := &artifactStorage{failPrimary: true}
	w := &Worker{repo: repo, storage: dest}
	if err := w.archiveResult(context.Background(), context.Background(), repo.task, "https://provider/video", "https://provider/frame"); err != nil {
		t.Fatal(err)
	}
	if repo.task.Status != core.TaskFailed || len(dest.ids) != 1 {
		t.Fatal("required primary archive failure was swallowed", repo.task, dest.ids)
	}
}

func TestShutdownDuringLastFrameLeavesTaskRecoverable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &repoStub{task: core.Task{ID: "frame-task", Status: core.TaskRunning}}
	w := &Worker{repo: repo, storage: &artifactStorage{cancel: cancel}}
	err := w.archiveResult(ctx, ctx, repo.task, "https://provider/video", "https://provider/frame")
	if err != context.Canceled || repo.task.Status != core.TaskRunning {
		t.Fatalf("shutdown made task terminal: %v %s", err, repo.task.Status)
	}
}

// Simulate durable scheduling without sleeping in unit tests. Production runs
// exactly one step per claim and persists pending state in PostgreSQL.
func processSteps(w *Worker, r *repoStub) error {
	for i := 0; i < 10; i++ {
		err := w.process(context.Background(), r.task.ID)
		var pending *pollLater
		if errors.As(err, &pending) {
			r.task.WorkerPollErrors = pending.errors
			continue
		}
		return err
	}
	return fmt.Errorf("too many steps")
}

type failingPollProvider struct{ providerStub }

func (p *failingPollProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	p.polls++
	return core.ProviderResult{}, fmt.Errorf("temporary upstream error")
}
func TestPollingUsesPersistedDeadlineAndErrorCount(t *testing.T) {
	started := time.Now().Add(-time.Minute)
	id := "upstream"
	r := &repoStub{task: core.Task{ID: "poll", Status: core.TaskRunning, ProviderTaskID: &id, WorkerStartedAt: &started, WorkerPollErrors: 2}}
	p := &failingPollProvider{}
	w := New(r, nil, p, &storageStub{}, time.Second, time.Hour, 3, time.Second)
	if err := w.process(context.Background(), r.task.ID); err != nil {
		t.Fatal(err)
	}
	if r.task.Status != core.TaskFailed || p.polls != 1 {
		t.Fatal("persisted polling failure count ignored", r.task)
	}
	r.task.Status = core.TaskRunning
	r.task.WorkerPollErrors = 0
	started = time.Now().Add(-2 * time.Hour)
	if err := w.process(context.Background(), r.task.ID); err != nil {
		t.Fatal(err)
	}
	if r.task.Status != core.TaskFailed || p.polls != 1 || *r.task.Error != "provider task timed out" {
		t.Fatal("restarted task reset timeout", r.task)
	}
}
