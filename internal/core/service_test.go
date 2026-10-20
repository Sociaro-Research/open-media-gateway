package core

import (
	"context"
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

type memoryRepository struct {
	task        Task
	key         string
	hash        string
	createCount int
}

func (r *memoryRepository) CreateOrGet(_ context.Context, task Task, key, hash string) (Task, bool, error) {
	if r.createCount > 0 && key != "" && key == r.key {
		if hash != r.hash {
			return Task{}, false, ErrIdempotencyConflict
		}
		return r.task, false, nil
	}
	r.task, r.key, r.hash = task, key, hash
	r.createCount++
	return task, true, nil
}
func (r *memoryRepository) Get(_ context.Context, _ string) (Task, error) { return r.task, nil }
func (r *memoryRepository) ListRecent(_ context.Context, _ int) ([]Task, error) {
	return []Task{r.task}, nil
}
func (r *memoryRepository) MarkEnqueued(context.Context, string) error                    { return nil }
func (r *memoryRepository) MarkRunning(context.Context, string) error                     { return nil }
func (r *memoryRepository) SetProviderTaskID(context.Context, string, string) error       { return nil }
func (r *memoryRepository) SetProviderUsage(context.Context, string, ProviderUsage) error { return nil }
func (r *memoryRepository) SetProviderResultURL(context.Context, string, string) error    { return nil }
func (r *memoryRepository) MarkSucceeded(context.Context, string, StoredMedia, string) error {
	return nil
}
func (r *memoryRepository) MarkFailed(context.Context, string, string) error  { return nil }
func (r *memoryRepository) ListRecoverable(context.Context) ([]string, error) { return nil, nil }

type memoryQueue struct{ id string }

func (q *memoryQueue) Enqueue(_ context.Context, id string) error { q.id = id; return nil }
func (q *memoryQueue) Dequeue(context.Context) (string, error)    { return "", nil }
func (q *memoryQueue) Ack(context.Context, string) error          { return nil }
func (q *memoryQueue) Requeue(context.Context, string) error      { return nil }
func (q *memoryQueue) Recover(context.Context) error              { return nil }

type catalogProvider struct{ models []MediaModel }

func (p catalogProvider) Name() string { return "catalog" }
func (p catalogProvider) Generate(context.Context, MediaRequest) (ProviderTask, error) {
	return ProviderTask{}, nil
}
func (p catalogProvider) GetTask(context.Context, string) (ProviderResult, error) {
	return ProviderResult{}, nil
}
func (p catalogProvider) ListModels(context.Context) ([]MediaModel, error) { return p.models, nil }

type availableCatalogProvider struct {
	catalogProvider
	enabled    bool
	configured bool
}

func (p availableCatalogProvider) Availability(context.Context) (bool, bool, error) {
	return p.enabled, p.configured, nil
}

func TestTaskServiceCreateVideo(t *testing.T) {
	repo := &memoryRepository{}
	queue := &memoryQueue{}
	service := NewTaskService(repo, queue, "mock", "mock-video-v1")

	task, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: "  sunrise  ", Provider: "", Model: "", MediaType: "video", WebhookURL: ""}, "")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskQueued || task.Provider != "mock" || task.Model != "mock-video-v1" {
		t.Fatalf("unexpected task: %#v", task)
	}
	if repo.task.Prompt != "sunrise" {
		t.Fatalf("prompt was not normalized: %q", repo.task.Prompt)
	}
	if queue.id != task.ID {
		t.Fatalf("queued id %q, want %q", queue.id, task.ID)
	}
}

func TestTaskServiceAssignsAuthenticatedOwner(t *testing.T) {
	repo := &memoryRepository{}
	service := NewTaskService(repo, &memoryQueue{}, "mock", "mock-video-v1")
	ctx := identity.WithOwnerID(context.Background(), "11111111-1111-1111-1111-111111111111")
	task, err := service.CreateMediaRequest(ctx, CreateMediaInput{Prompt: "sunrise", Provider: "", Model: "", MediaType: "video", WebhookURL: ""}, "")
	if err != nil {
		t.Fatal(err)
	}
	if task.OwnerID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("owner = %q", task.OwnerID)
	}
}

func TestTaskServiceRejectsBlankPrompt(t *testing.T) {
	service := NewTaskService(&memoryRepository{}, &memoryQueue{}, "mock", "mock-video-v1")
	if _, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: "  ", Provider: "", Model: "", MediaType: "video", WebhookURL: ""}, ""); err != ErrPromptRequired {
		t.Fatalf("got %v, want %v", err, ErrPromptRequired)
	}
}

func TestTaskServiceIdempotency(t *testing.T) {
	repo := &memoryRepository{}
	service := NewTaskService(repo, &memoryQueue{}, "mock", "mock-video-v1")
	first, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: "sunrise", Provider: "", Model: "", MediaType: "video", WebhookURL: "https://client.example/events"}, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: "sunrise", Provider: "", Model: "", MediaType: "video", WebhookURL: "https://client.example/events"}, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || repo.createCount != 1 {
		t.Fatalf("idempotent replay created another task: first=%s second=%s count=%d", first.ID, second.ID, repo.createCount)
	}
	_, err = service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: "different", Provider: "", Model: "", MediaType: "video", WebhookURL: "https://client.example/events"}, "request-1")
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("got %v, want idempotency conflict", err)
	}
}

func TestTaskServiceSelectsProviderPerTask(t *testing.T) {
	repo := &memoryRepository{}
	service := NewTaskServiceWithProviders(
		repo,
		&memoryQueue{},
		map[string]MediaProvider{"mock": nil, "bytedance": nil},
		map[string]string{"mock": "mock-video-v1", "bytedance": "seedance-default"},
		"mock",
	)
	task, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: "sunrise", Provider: "bytedance", Model: "seedance-fast", MediaType: "video", WebhookURL: ""}, "")
	if err != nil {
		t.Fatal(err)
	}
	if task.Provider != "bytedance" || task.Model != "seedance-fast" {
		t.Fatalf("unexpected provider selection: %#v", task)
	}
	if _, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: "sunrise", Provider: "unknown", Model: "model", MediaType: "video", WebhookURL: ""}, ""); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("got %v, want provider unavailable", err)
	}
}

func TestTaskServicePersistsAndHashesVideoOptions(t *testing.T) {
	repo := &memoryRepository{}
	service := NewTaskServiceWithProviders(
		repo, &memoryQueue{}, map[string]MediaProvider{"mock": nil}, map[string]string{"mock": "mock-video-v1"}, "mock",
	)
	watermark := false
	input := CreateMediaInput{
		Prompt: "sunrise", Provider: "mock", Model: "mock-video-v1",
		Options: MediaOptions{Resolution: "720p", Ratio: "9:16", Duration: 7, Watermark: &watermark},
	}
	first, err := service.CreateMediaRequest(context.Background(), input, "options-key")
	if err != nil {
		t.Fatal(err)
	}
	if first.Options.Resolution != "720p" || first.Options.Duration != 7 {
		t.Fatalf("options were not persisted: %#v", first.Options)
	}
	input.Options.Duration = 8
	if _, err := service.CreateMediaRequest(context.Background(), input, "options-key"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("options must participate in idempotency hash, got %v", err)
	}
}

func TestTaskServiceRejectsUnsupportedVideoOptions(t *testing.T) {
	repo := &memoryRepository{}
	service := NewTaskServiceWithProviders(
		repo, &memoryQueue{}, map[string]MediaProvider{"mock": nil}, map[string]string{"mock": "mock-video-v1"}, "mock",
	)
	draft := true
	_, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{
		Prompt: "sunrise", Model: "mock-video-v1",
		Options: MediaOptions{Resolution: "4k", Ratio: "16:9", Duration: 5, Draft: &draft},
	}, "")
	if !errors.Is(err, ErrOptionsInvalid) {
		t.Fatalf("got %v, want unsupported options", err)
	}
}

func TestTaskServiceCreatesImageThroughGenericContract(t *testing.T) {
	repo := &memoryRepository{}
	provider := catalogProvider{models: []MediaModel{{
		ID: "image-model", MediaType: "image", Resolutions: []string{"1024*1024"},
		SupportsImageInput: true, MaxImageInputs: 2,
	}}}
	service := NewTaskServiceWithProviders(repo, &memoryQueue{}, map[string]MediaProvider{"catalog": provider}, map[string]string{"catalog": "image-model"}, "catalog")
	task, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{
		Prompt: "sunrise", MediaType: "image", Model: "image-model",
		Options: MediaOptions{ImageURLs: []string{"https://example.com/reference.png"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if task.MediaType != "image" || task.Options.Resolution != "1024*1024" || len(task.Options.ImageURLs) != 1 {
		t.Fatalf("unexpected image task: %#v", task)
	}
}

func TestTaskServiceAllowsLyricsOnlyMusic(t *testing.T) {
	repo := &memoryRepository{}
	provider := catalogProvider{models: []MediaModel{{
		ID: "music-model", MediaType: "music", SupportsLyrics: true, SupportsGender: true, Formats: []string{"mp3", "wav"},
	}}}
	service := NewTaskServiceWithProviders(repo, &memoryQueue{}, map[string]MediaProvider{"catalog": provider}, map[string]string{"catalog": "music-model"}, "catalog")
	task, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{
		MediaType: "music", Model: "music-model", Options: MediaOptions{Lyrics: "[verse] Hello"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if task.Options.Format != "mp3" || task.Options.Gender != "female" {
		t.Fatalf("music defaults were not normalized: %#v", task.Options)
	}
}

func TestTaskServiceHidesAndRejectsDisabledProvider(t *testing.T) {
	provider := availableCatalogProvider{
		catalogProvider: catalogProvider{models: []MediaModel{{
			ID: "image-model", MediaType: "image", Resolutions: []string{"1024*1024"},
		}}},
		enabled: true, configured: true,
	}
	service := NewTaskServiceWithProviders(
		&memoryRepository{}, &memoryQueue{}, map[string]MediaProvider{"catalog": provider},
		map[string]string{"catalog": "image-model"}, "catalog",
	)
	if got := service.ListProviders(context.Background()); len(got) != 1 {
		t.Fatalf("enabled provider list length = %d, want 1", len(got))
	}
	provider.enabled = false
	service = NewTaskServiceWithProviders(
		&memoryRepository{}, &memoryQueue{}, map[string]MediaProvider{"catalog": provider},
		map[string]string{"catalog": "image-model"}, "catalog",
	)
	if got := service.ListProviders(context.Background()); len(got) != 0 {
		t.Fatalf("disabled provider was returned: %#v", got)
	}
	_, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{
		Prompt: "sunrise", Provider: "catalog", Model: "image-model", MediaType: "image",
	}, "")
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("create error = %v, want provider unavailable", err)
	}
}

func TestTaskAttributionSurvivesReplayWithAnotherCredential(t *testing.T) {
	repo := &memoryRepository{}
	service := NewTaskService(repo, &memoryQueue{}, "mock", "mock-video-v1")
	owner := identity.WithOwnerID(context.Background(), "11111111-1111-1111-1111-111111111111")
	first, err := service.CreateMediaRequest(identity.WithAPIKeyID(owner, "key-one"), CreateMediaInput{Prompt: "sunrise", Provider: "", Model: "", MediaType: "video", WebhookURL: ""}, "one-generation")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.CreateMediaRequest(identity.WithAPIKeyID(owner, "key-two"), CreateMediaInput{Prompt: "sunrise", Provider: "", Model: "", MediaType: "video", WebhookURL: ""}, "one-generation")
	if err != nil {
		t.Fatal(err)
	}
	if first.APIKeyID == nil || *first.APIKeyID != "key-one" || replay.APIKeyID == nil || *replay.APIKeyID != "key-one" || first.ID != replay.ID {
		t.Fatal("idempotent replay changed originating key")
	}
	sessionTask, err := service.CreateMediaRequest(owner, CreateMediaInput{Prompt: "sunrise", Provider: "", Model: "", MediaType: "video", WebhookURL: ""}, "another-generation")
	if err != nil {
		t.Fatal(err)
	}
	if sessionTask.APIKeyID != nil {
		t.Fatal("session task attributed to a personal key")
	}
}

type rejectingContractProvider struct {
	catalogProvider
	quotes int
}

func (p *rejectingContractProvider) ValidateRequest(context.Context, MediaRequest) error {
	return ErrOptionsInvalid
}
func (p *rejectingContractProvider) Quote(context.Context, MediaModel, MediaRequest) (TaskCost, string, error) {
	p.quotes++
	return TaskCost{}, "", nil
}

func TestProviderContractCheckedBeforeQuoteAndAdmission(t *testing.T) {
	repo := &memoryRepository{}
	queue := &memoryQueue{}
	provider := &rejectingContractProvider{catalogProvider: catalogProvider{models: []MediaModel{{ID: "model", MediaType: "image", Resolutions: []string{"1024*1024"}}}}}
	service := NewTaskServiceWithProviders(repo, queue, map[string]MediaProvider{"catalog": provider}, map[string]string{"catalog": "model"}, "catalog")
	service.EnableAccounting()
	input := CreateMediaInput{Prompt: "test", Model: "model", MediaType: "image"}
	if _, err := service.Estimate(context.Background(), input); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatalf("estimate: %v", err)
	}
	if _, err := service.CreateMediaRequest(context.Background(), input, ""); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatalf("generation: %v", err)
	}
	if provider.quotes != 0 || repo.createCount != 0 || queue.id != "" {
		t.Fatal("invalid provider request reached pricing/admission")
	}
}

func TestModelSpecificControlsAreValidated(t *testing.T) {
	service := &TaskService{}
	model := MediaModel{MediaType: "video", Resolutions: []string{"720p"}, Ratios: []string{"adaptive"}, MinDuration: 5, MaxDuration: 10, DefaultDuration: 5, DurationStep: 5, MaxSeed: 2147483647}
	if _, err := service.normalizeOptions(model, MediaOptions{Duration: 7}); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatal("7 seconds accepted for a 5/10-only model")
	}
	highSeed := int64(2147483648)
	if _, err := service.normalizeOptions(model, MediaOptions{Seed: &highSeed}); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatal("out-of-range seed accepted")
	}
	model.DurationFromInput = true
	model.DurationStep = 0
	if _, err := service.normalizeOptions(model, MediaOptions{Duration: 5}); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatal("input-driven model accepted a duration override")
	}
	if options, err := service.normalizeOptions(model, MediaOptions{}); err != nil || options.Duration != 0 {
		t.Fatal("input duration was replaced with a made-up default")
	}
	model.AudioAlwaysOn = true
	model.SupportsAudio = true
	disabled := false
	if _, err := service.normalizeOptions(model, MediaOptions{GenerateAudio: &disabled}); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatal("silent generation accepted for an always-audio model")
	}
	if options, err := service.normalizeOptions(model, MediaOptions{}); err != nil || options.GenerateAudio == nil || !*options.GenerateAudio {
		t.Fatal("audio default does not reflect provider behavior")
	}
}

func TestMediaDrivenPromptAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, prompt       string
		optional, noPrompt bool
		want               error
	}{
		{"required", "", false, false, ErrPromptRequired},
		{"optional", "", true, false, nil},
		{"unused", "", false, true, nil},
		{"unused with text", "ignored direction", false, true, ErrOptionsInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, queue := &memoryRepository{}, &memoryQueue{}
			provider := catalogProvider{models: []MediaModel{{ID: "source-model", MediaType: "audio", PromptOptional: tc.optional, NoPrompt: tc.noPrompt}}}
			service := NewTaskServiceWithProviders(repo, queue, map[string]MediaProvider{"catalog": provider}, map[string]string{"catalog": "source-model"}, "catalog")
			_, err := service.CreateMediaRequest(context.Background(), CreateMediaInput{Prompt: tc.prompt, Model: "source-model"}, "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.want != nil && (repo.createCount != 0 || queue.id != "") {
				t.Fatal("invalid prompt reached admission")
			}
		})
	}
}

func TestAudioDurationAndVideoSeedContracts(t *testing.T) {
	s := &TaskService{}
	for _, kind := range []string{"music", "audio"} {
		m := MediaModel{MediaType: kind, MinDuration: 5, MaxDuration: 30, DefaultDuration: 10, DurationStep: 5, Formats: []string{"wav"}}
		if o, err := s.normalizeOptions(m, MediaOptions{}); err != nil || o.Duration != 10 || o.Format != "wav" {
			t.Fatalf("%s defaults: %+v, %v", kind, o, err)
		}
		for _, o := range []MediaOptions{{Duration: 7}, {Duration: 35}, {Format: "mp3"}} {
			if _, err := s.normalizeOptions(m, o); !errors.Is(err, ErrOptionsInvalid) {
				t.Fatalf("%s accepted %+v", kind, o)
			}
		}
		m.DurationFromInput = true
		if _, err := s.normalizeOptions(m, MediaOptions{Duration: 10}); !errors.Is(err, ErrOptionsInvalid) {
			t.Fatalf("%s accepted source duration override", kind)
		}
	}
	seed := int64(4294967295)
	m := MediaModel{MediaType: "video", DurationFromInput: true, Formats: []string{"mp4"}}
	if o, err := s.normalizeOptions(m, MediaOptions{Seed: &seed}); err != nil || o.Format != "mp4" {
		t.Fatalf("valid video seed/default format: %+v, %v", o, err)
	}
	if _, err := s.normalizeOptions(m, MediaOptions{Format: "wav"}); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatal("unsupported video format accepted")
	}
}
