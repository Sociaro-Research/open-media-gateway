package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
	"github.com/google/uuid"
)

const pixverseWorkerEndpoint = "https://workspace-fixture.ap-southeast-1.maas.aliyuncs.com/api/v1"

type pixverseWorkerTransport func(*http.Request) (*http.Response, error)

func (f pixverseWorkerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type pixverseWorkerRepo struct {
	repoStub
	noCharge int
}

func (r *pixverseWorkerRepo) SetProviderUsage(_ context.Context, _ string, usage core.ProviderUsage) error {
	r.task.Usage = &usage
	if r.task.Cost != nil {
		r.task.Cost.MeterUsage(usage)
	}
	return nil
}
func (r *pixverseWorkerRepo) ConfirmNoCharge(context.Context, string) error { r.noCharge++; return nil }

func pixverseWorkerProvider(t *testing.T, client *http.Client) core.MediaProvider {
	t.Helper()
	credentials := providerkit.Credentials{APIKey: "fixture-only", BaseURL: pixverseWorkerEndpoint}
	provider, err := alibaba.Definition().Factory(credentials,
		providerkit.Settings{"pixverse_reviewed_connection": alibaba.Definition().Identity(credentials)}, client)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
func pixverseWorkerResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func pixverseWorkerRequest(id string) core.MediaRequest {
	r := core.MediaRequest{Model: id, Prompt: "A slow camera move.", Options: core.MediaOptions{Duration: 5, Resolution: "720p"}}
	switch {
	case strings.HasSuffix(id, "-t2v"):
		r.Options.Ratio = "16:9"
	case strings.HasSuffix(id, "-it2v"):
		r.Options.ImageURLs = []string{"https://media.example.invalid/first.png"}
	case strings.HasSuffix(id, "-kf2v"):
		r.Options.ImageURLs = []string{"https://media.example.invalid/first.png", "https://media.example.invalid/last.png"}
	case strings.HasSuffix(id, "-r2v"):
		r.Options.Ratio = "16:9"
		r.Options.ImageURLs = []string{"https://media.example.invalid/person.png", "https://media.example.invalid/guitar.png"}
		r.Options.Parameters = map[string]any{"reference_names": []string{"person", "guitar"}}
		r.Prompt = "@person holds @guitar "
	}
	return r
}
func pixverseWorkerCost(t *testing.T, p core.MediaProvider, r core.MediaRequest) *core.TaskCost {
	t.Helper()
	m := core.MediaModel{ID: r.Model, MediaType: "video"}
	cost, _, err := p.(core.CostQuoter).Quote(context.Background(), m, r)
	if err != nil {
		t.Fatal(err)
	}
	return &cost
}

const pixverseWorkerSuccess = `{"output":{"task_id":"accepted-pixverse","task_status":"SUCCEEDED","video_url":"https://media.example.invalid/video.mp4","image_url":"https://media.example.invalid/wrong.png"},"usage":{"duration":3,"size":"1280*720","fps":24,"video_count":1,"audio":false,"SR":"720"}}`

func TestPixversePostgresRecoveryRetainsNativeIDIntentAndDownloadedBytes(t *testing.T) {
	for _, model := range []string{"pixverse/pixverse-v6-t2v", "pixverse/pixverse-v6-it2v", "pixverse/pixverse-v6-kf2v", "pixverse/pixverse-v6-r2v"} {
		t.Run(model, func(t *testing.T) {
			base, db, repo, _, _, _ := operationTestServices(t)
			ctx := identity.WithOwnerID(base, identity.SystemOwnerID)
			posts, polls, downloads := 0, 0, 0
			media := testmedia.MP4(3000, 1000, 72)
			client := &http.Client{Transport: pixverseWorkerTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "media.example.invalid" {
					downloads++
					if r.Method != "GET" || r.Header.Get("Authorization") != "" {
						t.Fatal("invalid media transfer")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, ContentLength: int64(len(media)), Body: io.NopCloser(bytes.NewReader(media))}, nil
				}
				if r.Method == "POST" {
					posts++
					return pixverseWorkerResponse(200, `{"output":{"task_id":"accepted-pixverse","task_status":"PENDING"}}`), nil
				}
				polls++
				if r.URL.Path != "/api/v1/tasks/accepted-pixverse" {
					t.Fatal("persisted native ID changed", r.URL.Path)
				}
				return pixverseWorkerResponse(200, pixverseWorkerSuccess), nil
			})}
			r := pixverseWorkerRequest(model)
			p := pixverseWorkerProvider(t, client)
			now, id := time.Now().UTC(), uuid.NewString()
			task := core.Task{ID: id, Status: core.TaskQueued, Provider: "alibaba", Model: model, MediaType: "video", Prompt: r.Prompt, Options: r.Options, Cost: pixverseWorkerCost(t, p, r), CreatedAt: now, UpdatedAt: now}
			if _, _, err := repo.CreateOrGet(ctx, task, "", "pixverse-postgres"); err != nil {
				t.Fatal(err)
			}
			task, err := repo.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			ctx = identity.WithResourceOwners(ctx, task.ProviderOwnerID, task.StorageOwnerID)
			root := t.TempDir()
			store, err := storage.NewLocal(root, client, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			firstCtx := core.WithClaimToken(ctx, uuid.NewString())
			if acquired, err := repo.AcquireTask(firstCtx, id, time.Minute); err != nil || !acquired {
				t.Fatal(acquired, err)
			}
			first := New(repo, nil, p, store, time.Millisecond, time.Minute, 3, time.Second)
			var later *pollLater
			if err := first.process(firstCtx, id); !errors.As(err, &later) {
				t.Fatal("submission did not checkpoint", err)
			}
			if err := repo.ReleaseTask(firstCtx, id); err != nil {
				t.Fatal(err)
			}
			recovered := postgres.NewTaskRepository(db)
			task, err = recovered.Get(ctx, id)
			if err != nil || task.ProviderTaskID == nil || *task.ProviderTaskID != "accepted-pixverse" || task.Prompt != r.Prompt || posts != 1 || downloads != 0 {
				t.Fatal("durable native receipt or prompt lost", task, err)
			}
			secondCtx := core.WithClaimToken(ctx, uuid.NewString())
			if acquired, err := recovered.AcquireTask(secondCtx, id, time.Minute); err != nil || !acquired {
				t.Fatal(acquired, err)
			}
			store, err = storage.NewLocal(root, client, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			second := New(recovered, nil, pixverseWorkerProvider(t, client), store, time.Millisecond, time.Minute, 3, time.Second)
			if err := second.process(secondCtx, id); err != nil {
				t.Fatal(err)
			}
			done, err := recovered.Get(ctx, id)
			if err != nil || done.Status != core.TaskSucceeded || done.StorageKey == nil || posts != 1 || polls != 1 || downloads != 1 || done.Cost.MeteredUSD == nil || done.Cost.MeteredUSD.String() != "0.120000" {
				t.Fatal("recovery duplicated or lost metering", done, err)
			}
			object, err := store.Open(ctx, *done.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(object.Reader)
			_ = object.Reader.Close()
			if err != nil || !bytes.Equal(data, media) {
				t.Fatal("recovered video bytes changed", err)
			}
			if err := second.process(secondCtx, id); err != nil || posts != 1 || downloads != 1 {
				t.Fatal("terminal delivery repeated native work", err)
			}
		})
	}
}

func TestPixverseWorkerRestartPollsNativeIDAndArchivesAllModes(t *testing.T) {
	for _, id := range []string{"pixverse/pixverse-v6-t2v", "pixverse/pixverse-v6-it2v", "pixverse/pixverse-v6-kf2v", "pixverse/pixverse-v6-r2v"} {
		t.Run(id, func(t *testing.T) {
			posts, gets := 0, 0
			client := &http.Client{Transport: pixverseWorkerTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "workspace-fixture.ap-southeast-1.maas.aliyuncs.com" {
					t.Fatal("wrong workspace")
				}
				if req.Method == "POST" {
					posts++
					if req.URL.Path != "/api/v1/services/aigc/video-generation/video-synthesis" || req.Header.Get("X-DashScope-Async") != "enable" {
						t.Fatal("wrong PixVerse submission route/header")
					}
					return pixverseWorkerResponse(200, `{"output":{"task_id":"accepted-pixverse","task_status":"PENDING"}}`), nil
				}
				gets++
				if req.Method != "GET" || req.URL.Path != "/api/v1/tasks/accepted-pixverse" {
					t.Fatal("native task ID changed during recovery")
				}
				if gets == 1 {
					return pixverseWorkerResponse(503, `{"code":"InternalError"}`), nil
				}
				return pixverseWorkerResponse(200, pixverseWorkerSuccess), nil
			})}
			r := pixverseWorkerRequest(id)
			p := pixverseWorkerProvider(t, client)
			cost := pixverseWorkerCost(t, p, r)
			repo := &pixverseWorkerRepo{repoStub: repoStub{task: core.Task{ID: "task", Status: core.TaskQueued, Provider: "alibaba", Model: id, MediaType: "video", Prompt: r.Prompt, Options: r.Options, Cost: cost}}}
			dest := &pixverseCountArchive{}
			w := New(repo, nil, p, dest, time.Millisecond, time.Minute, 3, time.Second)
			var later *pollLater
			if err := w.process(context.Background(), repo.task.ID); !errors.As(err, &later) {
				t.Fatal("submission did not checkpoint", err)
			}
			if repo.task.ProviderTaskID == nil || *repo.task.ProviderTaskID != "accepted-pixverse" {
				t.Fatal("native billing/recovery ID replaced")
			}
			// Recreate provider and worker after the native ID has been persisted.
			p = pixverseWorkerProvider(t, client)
			w = New(repo, nil, p, dest, time.Millisecond, time.Minute, 3, time.Second)
			for i := 0; i < 5 && repo.task.Status == core.TaskRunning; i++ {
				err := w.process(context.Background(), repo.task.ID)
				if errors.As(err, &later) {
					repo.task.WorkerPollErrors = later.errors
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if posts != 1 || gets != 2 || dest.calls != 1 || repo.task.Status != core.TaskSucceeded || dest.archivedURL != "https://media.example.invalid/video.mp4" {
				t.Fatalf("wrong recovery/storage: post=%d get=%d task=%+v", posts, gets, repo.task)
			}
			if cost.ExpectedUSD.String() != "0.200000" || cost.MeteredUSD == nil || cost.MeteredUSD.String() != "0.120000" || repo.noCharge != 0 {
				t.Fatalf("accepted tariff/reporting changed: %+v", cost)
			}
		})
	}
}

type pixverseCancelArchive struct {
	storageStub
	cancel context.CancelFunc
}

func (s *pixverseCancelArchive) Archive(ctx context.Context, id, source string) (core.StoredMedia, error) {
	if s.cancel != nil {
		cancel := s.cancel
		s.cancel = nil
		cancel()
		return core.StoredMedia{}, ctx.Err()
	}
	return s.storageStub.Archive(ctx, id, source)
}

func TestPixverseWorkerArchiveCancellationAndExpiredLookupNeverResubmit(t *testing.T) {
	for _, mode := range []string{"archive-cancel", "archive-error", "expired-unknown"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			client := &http.Client{Transport: pixverseWorkerTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == "POST" {
					posts++
					t.Fatal("already accepted task was submitted again")
				}
				if req.URL.Path != "/api/v1/tasks/accepted-pixverse" {
					t.Fatal("accepted native ID lost")
				}
				if mode == "expired-unknown" {
					return pixverseWorkerResponse(200, `{"output":{"task_id":"accepted-pixverse","task_status":"UNKNOWN"}}`), nil
				}
				return pixverseWorkerResponse(200, pixverseWorkerSuccess), nil
			})}
			model := "pixverse/pixverse-v6-t2v"
			if mode == "archive-cancel" {
				model = "pixverse/pixverse-v6-r2v"
			}
			r := pixverseWorkerRequest(model)
			p := pixverseWorkerProvider(t, client)
			id, started := "accepted-pixverse", time.Now()
			cost := pixverseWorkerCost(t, p, r)
			repo := &pixverseWorkerRepo{repoStub: repoStub{task: core.Task{ID: "accepted", Status: core.TaskRunning, Provider: "alibaba", Model: r.Model, MediaType: "video", Options: r.Options, Cost: cost, ProviderTaskID: &id, WorkerStartedAt: &started}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dest := &pixverseCancelArchive{}
			if mode == "archive-cancel" {
				dest.cancel = cancel
			}
			if mode == "archive-error" {
				dest.err = errors.New("synthetic archive failure")
			}
			w := New(repo, nil, p, dest, time.Second, time.Minute, 1, time.Second)
			err := w.process(ctx, repo.task.ID)
			if mode == "archive-cancel" {
				if !errors.Is(err, context.Canceled) || repo.task.Status != core.TaskRunning {
					t.Fatal("canceled archive lost recoverable task", err)
				}
				w = New(repo, nil, pixverseWorkerProvider(t, client), dest, time.Second, time.Minute, 1, time.Second)
				if err := w.process(context.Background(), repo.task.ID); err != nil || repo.task.Status != core.TaskSucceeded {
					t.Fatal("archive did not recover", err)
				}
			} else if err != nil || repo.task.Status != core.TaskFailed {
				t.Fatal("expected existing terminal failure behavior", err)
			}
			if posts != 0 || repo.noCharge != 0 || *repo.task.ProviderTaskID != id {
				t.Fatal("recovery changed paid-task identity or invented zero charge")
			}
			if mode == "expired-unknown" && cost.MeteredUSD != nil {
				t.Fatal("expired lookup invented metering")
			}
		})
	}
}

type pixverseCountArchive struct {
	storageStub
	calls int
}

func (s *pixverseCountArchive) Archive(ctx context.Context, id, source string) (core.StoredMedia, error) {
	s.calls++
	return s.storageStub.Archive(ctx, id, source)
}

func TestPixverseWorkerRetainsAcceptedIDDespiteUncertainCreationFields(t *testing.T) {
	bodies := []string{
		"{\"output\":{\"task_id\":\"accepted-pixverse\"}}",
		"{\"output\":{\"task_id\":\"accepted-pixverse\",\"task_status\":\"UNREVIEWED_FUTURE_STATE\"}}",
		"{\"code\":\"InternalError\",\"output\":{\"task_id\":\"accepted-pixverse\",\"task_status\":\"PENDING\"},\"usage\":{\"output_video_duration\":100}}",
	}
	for _, body := range bodies {
		posts, gets := 0, 0
		client := &http.Client{Transport: pixverseWorkerTransport(func(req *http.Request) (*http.Response, error) {
			if req.Method == "POST" {
				posts++
				return pixverseWorkerResponse(200, body), nil
			}
			gets++
			if req.URL.Path != "/api/v1/tasks/accepted-pixverse" {
				t.Fatal("accepted native task ID lost")
			}
			return pixverseWorkerResponse(200, pixverseWorkerSuccess), nil
		})}
		r := pixverseWorkerRequest("pixverse/pixverse-v6-t2v")
		provider := pixverseWorkerProvider(t, client)
		cost := pixverseWorkerCost(t, provider, r)
		repo := &pixverseWorkerRepo{repoStub: repoStub{task: core.Task{ID: "uncertain-status", Status: core.TaskQueued, Provider: "alibaba", Model: r.Model, MediaType: "video", Prompt: r.Prompt, Options: r.Options, Cost: cost}}}
		dest := &pixverseCountArchive{}
		w := New(repo, nil, provider, dest, time.Second, time.Minute, 3, time.Second)
		var pending *pollLater
		if err := w.process(context.Background(), repo.task.ID); !errors.As(err, &pending) || repo.task.Status != core.TaskRunning || repo.task.ProviderTaskID == nil || *repo.task.ProviderTaskID != "accepted-pixverse" {
			t.Fatal("accepted receipt discarded", err, repo.task)
		}
		if repo.task.Usage != nil {
			t.Fatal("unverified creation usage was metered")
		}
		w = New(repo, nil, pixverseWorkerProvider(t, client), dest, time.Second, time.Minute, 3, time.Second)
		if err := w.process(context.Background(), repo.task.ID); err != nil {
			t.Fatal(err)
		}
		if posts != 1 || gets != 1 || dest.calls != 1 || repo.task.Status != core.TaskSucceeded || cost.MeteredUSD == nil || cost.MeteredUSD.String() != "0.120000" {
			t.Fatalf("uncertain creation did not reconcile through its original ID: %+v", repo.task)
		}
	}
}
