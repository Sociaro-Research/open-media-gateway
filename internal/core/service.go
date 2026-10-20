package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/markup"
	"github.com/Sociaro-Research/media-gateway-dev/internal/webhookurl"
)

var (
	ErrPromptRequired      = errors.New("prompt is required")
	ErrPromptTooLong       = errors.New("prompt must not exceed 10000 characters")
	ErrModelRequired       = errors.New("model is required")
	ErrModelUnavailable    = errors.New("selected model is not available for this provider")
	ErrOptionsInvalid      = errors.New("media options are not supported by the selected model")
	ErrProviderUnavailable = errors.New("selected provider is not configured")
	ErrWebhookInvalid      = webhookurl.ErrDestination
	ErrIdempotencyKey      = errors.New("Idempotency-Key must not exceed 200 characters")
	ErrIdempotencyConflict = errors.New("Idempotency-Key was already used with a different request")
)

type TaskService struct {
	directory        ProviderDirectory
	repo             TaskRepository
	queue            TaskQueue
	provider         string
	model            string
	providers        map[string]MediaProvider
	providerDefaults map[string]string
	now              func() time.Time
	accounting       bool
}

func NewTaskService(repo TaskRepository, queue TaskQueue, provider, defaultModel string) *TaskService {
	return &TaskService{
		repo: repo, queue: queue, provider: provider, model: defaultModel,
		providers:        map[string]MediaProvider{provider: nil},
		providerDefaults: map[string]string{provider: defaultModel}, now: time.Now,
	}
}

func NewTaskServiceWithProviders(repo TaskRepository, queue TaskQueue, available map[string]MediaProvider, defaults map[string]string, defaultProvider string) *TaskService {
	return &TaskService{
		repo: repo, queue: queue, provider: defaultProvider, model: defaults[defaultProvider],
		providers: available, providerDefaults: defaults, now: time.Now,
	}
}

// EnableAccounting requires a server quote for every new generation.
func (s *TaskService) EnableAccounting() { s.accounting = true }

func (s *TaskService) CreateMediaRequest(ctx context.Context, input CreateMediaInput, idempotencyKey string) (Task, error) {
	task, err := s.prepare(ctx, input, idempotencyKey)
	if err != nil {
		return Task{}, err
	}
	encodedOptions, _ := json.Marshal(task.Options)
	webhookURL := ""
	if task.WebhookURL != nil {
		webhookURL = *task.WebhookURL
	}
	hash := requestHash(task.Provider, task.Model, task.MediaType, task.Prompt, webhookURL, string(encodedOptions))
	hash = requestHash(hash, identity.ActorID(ctx))
	if task.ProviderOwnerID != task.OwnerID || task.StorageOwnerID != task.OwnerID {
		hash = requestHash(hash, task.ProviderOwnerID, task.StorageOwnerID)
	}
	stored, _, err := s.repo.CreateOrGet(ctx, task, idempotencyKey, hash)
	if err != nil {
		return Task{}, fmt.Errorf("store task: %w", err)
	}
	if stored.Status == TaskQueued || stored.Status == TaskRunning {
		if err := s.queue.Enqueue(ctx, stored.ID); err == nil {
			_ = s.repo.MarkEnqueued(ctx, stored.ID)
		}
	}
	return stored, nil
}

func (s *TaskService) prepare(ctx context.Context, input CreateMediaInput, idempotencyKey string) (Task, error) {
	if input.MaxExpectedUSD != nil && *input.MaxExpectedUSD < 0 {
		return Task{}, ErrOptionsInvalid
	}
	prompt := strings.TrimSpace(input.Prompt)
	if len([]rune(prompt)) > 10000 {
		return Task{}, ErrPromptTooLong
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider == "" {
		provider = s.provider
	}
	providerAdapter, ok := s.providerFor(ctx, provider)
	if !ok {
		return Task{}, ErrProviderUnavailable
	}
	if availability, ok := providerAdapter.(ProviderAvailability); ok {
		enabled, configured, err := availability.Availability(ctx)
		if err != nil || !enabled || !configured {
			return Task{}, ErrProviderUnavailable
		}
	}
	model := strings.TrimSpace(input.Model)
	if model == "" {
		model = s.providerDefaults[provider]
	}
	if strings.TrimSpace(model) == "" {
		return Task{}, ErrModelRequired
	}
	descriptor, found := s.findModel(ctx, provider, model)
	if !found {
		return Task{}, ErrModelUnavailable
	}
	mediaType := strings.ToLower(strings.TrimSpace(input.MediaType))
	if mediaType == "" {
		mediaType = descriptor.MediaType
	}
	if mediaType == "" {
		mediaType = "video"
	}
	if descriptor.MediaType != "" && mediaType != descriptor.MediaType {
		return Task{}, ErrModelUnavailable
	}
	if prompt == "" && !descriptor.NoPrompt && !descriptor.PromptOptional && !(mediaType == "music" && descriptor.SupportsLyrics && strings.TrimSpace(input.Options.Lyrics) != "") {
		return Task{}, ErrPromptRequired
	}
	if descriptor.NoPrompt && prompt != "" {
		return Task{}, fmt.Errorf("%w: this model does not accept a prompt", ErrOptionsInvalid)
	}
	options, err := s.normalizeOptions(descriptor, input.Options)
	if err != nil {
		return Task{}, err
	}
	if validator, ok := providerAdapter.(RequestValidator); ok {
		if err := validator.ValidateRequest(ctx, MediaRequest{Prompt: prompt, Model: model, Options: options}); err != nil {
			return Task{}, err
		}
	}
	webhookURL := strings.TrimSpace(input.WebhookURL)
	if webhookURL != "" {
		if webhookurl.Validate(webhookURL) != nil {
			return Task{}, ErrWebhookInvalid
		}
	}
	if len(idempotencyKey) > 200 {
		return Task{}, ErrIdempotencyKey
	}

	now := s.now().UTC()
	task := Task{
		PublicModel:     input.PublicModel,
		ID:              uuid.NewString(),
		OwnerID:         identity.OwnerID(ctx),
		ActorID:         identity.ActorID(ctx),
		OrganizationID:  identity.OrganizationFromContext(ctx).ID,
		ProviderOwnerID: identity.ProviderOwnerID(ctx), StorageOwnerID: identity.StorageOwnerID(ctx),
		APIKeyID:  identity.APIKeyID(ctx),
		Status:    TaskQueued,
		Provider:  provider,
		Model:     model,
		MediaType: mediaType,
		Prompt:    prompt,
		Options:   options,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if webhookURL != "" {
		task.WebhookURL = &webhookURL
	}
	if s.accounting {
		if quoter, ok := providerAdapter.(CostQuoter); ok {
			cost, fingerprint, err := quoter.Quote(ctx, descriptor, MediaRequest{TaskID: task.ID, Prompt: prompt, Model: model, Options: options})
			// A missing quote is rejected transactionally only for a NEW task, so an
			// idempotent retry can still return its original acceptance after a tariff change.
			if err != nil && !errors.Is(err, ErrPricingUnavailable) {
				return Task{}, err
			}
			if err == nil {
				task.Cost, task.ProviderIdentity = &cost, fingerprint
			}
		} else if ProviderIsFree(providerAdapter) {
			cost := FreeCost(now)
			task.Cost = &cost
		}
		task.RequireCost = true
	}
	task.MaxExpectedUSD = input.MaxExpectedUSD
	return task, nil
}

func (s *TaskService) Estimate(ctx context.Context, input CreateMediaInput) (CostEstimate, error) {
	task, err := s.prepare(ctx, input, "")
	if err != nil {
		return CostEstimate{}, err
	}
	if task.Cost == nil {
		return CostEstimate{}, ErrPricingUnavailable
	}
	if rates, ok := s.repo.(MarkupReader); ok {
		if err := rates.ApplyMarkup(ctx, &task); err != nil {
			return CostEstimate{}, err
		}
	}
	estimate := CostEstimate{Cost: *task.Cost, CanGenerate: true}
	if task.APIKeyID != nil {
		if budgets, ok := s.repo.(BudgetReader); ok {
			estimate.WeeklyLimitUSD, estimate.RemainingUSD, err = budgets.Budget(ctx, *task.APIKeyID, task.CreatedAt)
			if err != nil && !(errors.Is(err, ErrPricingUnavailable) && task.Cost.ExpectedUSD == 0) {
				return CostEstimate{}, err
			}
			estimate.CanGenerate = estimate.RemainingUSD == nil || *estimate.RemainingUSD > 0
		}
	}
	return estimate, nil
}

func (s *TaskService) normalizeOptions(descriptor MediaModel, options MediaOptions) (MediaOptions, error) {
	if err := ValidateTimeoutSeconds(options.TimeoutSeconds); err != nil {
		return MediaOptions{}, fmt.Errorf("%w: %v", ErrOptionsInvalid, err)
	}
	var err error
	options.Parameters, err = NormalizeParameters(descriptor.Parameters, options.Parameters)
	if err != nil {
		return MediaOptions{}, err
	}
	for _, control := range []struct {
		value   *string
		choices []string
	}{{&options.Quality, descriptor.Qualities}, {&options.Background, descriptor.Backgrounds}} {
		if *control.value == "" && len(control.choices) > 0 {
			*control.value = control.choices[0]
		}
		if *control.value != "" && !slices.Contains(control.choices, *control.value) {
			return MediaOptions{}, ErrOptionsInvalid
		}
	}
	if len(options.VideoURLs) > 0 {
		if options.VideoURL != "" && options.VideoURL != options.VideoURLs[0] {
			return MediaOptions{}, ErrOptionsInvalid
		}
		options.VideoURL = options.VideoURLs[0]
	}
	if len(options.AudioURLs) > 0 {
		if options.AudioURL != "" && options.AudioURL != options.AudioURLs[0] {
			return MediaOptions{}, ErrOptionsInvalid
		}
		options.AudioURL = options.AudioURLs[0]
	}
	for _, url := range append(options.Videos(), options.Audios()...) {
		if url == "" {
			return MediaOptions{}, ErrOptionsInvalid
		}
	}
	if err := ValidateReferenceURLs(descriptor, options); err != nil {
		return MediaOptions{}, err
	}
	if err := ValidateReferenceMode(descriptor, options); err != nil {
		return MediaOptions{}, err
	}
	if descriptor.NoSeed && options.Seed != nil {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if descriptor.MaxSeed > 0 && options.Seed != nil && *options.Seed > descriptor.MaxSeed {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if descriptor.NoWatermark && options.Watermark != nil {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if descriptor.AudioAlwaysOn {
		if options.GenerateAudio != nil && !*options.GenerateAudio {
			return MediaOptions{}, fmt.Errorf("%w: this model always generates audio", ErrOptionsInvalid)
		}
		enabled := true
		options.GenerateAudio = &enabled
	}
	if descriptor.RequiresAudioInput && strings.TrimSpace(options.AudioURL) == "" {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if !descriptor.SupportsAudioInput && strings.TrimSpace(options.AudioURL) != "" {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if descriptor.RequiresVideoInput && strings.TrimSpace(options.VideoURL) == "" {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if !descriptor.SupportsVideoInput && strings.TrimSpace(options.VideoURL) != "" {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if descriptor.RequiresImageInput && len(options.ImageURLs) == 0 {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if descriptor.MinImageInputs > 0 && len(options.ImageURLs) < descriptor.MinImageInputs {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if !descriptor.SupportsImageInput && len(options.ImageURLs) > 0 {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if descriptor.MaxImageInputs > 0 && len(options.ImageURLs) > descriptor.MaxImageInputs {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if options.NegativePrompt != "" && !descriptor.SupportsNegativePrompt {
		return MediaOptions{}, ErrOptionsInvalid
	}
	switch descriptor.MediaType {
	case "image":
		if len(descriptor.Resolutions) == 0 && options.Resolution != "" {
			return MediaOptions{}, ErrOptionsInvalid
		}
		if options.Resolution == "" && len(descriptor.Resolutions) > 0 {
			options.Resolution = descriptor.Resolutions[0]
		}
		if len(descriptor.Resolutions) > 0 && !slices.Contains(descriptor.Resolutions, options.Resolution) {
			return MediaOptions{}, ErrOptionsInvalid
		}
	case "speech":
		if descriptor.SupportsVoice && strings.TrimSpace(options.Voice) == "" {
			if len(descriptor.Voices) > 0 {
				options.Voice = descriptor.Voices[0]
			}
		}
		if descriptor.RequiresVoice && strings.TrimSpace(options.Voice) == "" {
			return MediaOptions{}, ErrOptionsInvalid
		}
		if len(descriptor.Voices) > 0 && !slices.Contains(descriptor.Voices, options.Voice) {
			return MediaOptions{}, ErrOptionsInvalid
		}
	case "music":
		if descriptor.SupportsGender && options.Gender == "" {
			options.Gender = "female"
		}
		if descriptor.SupportsGender && options.Gender != "female" && options.Gender != "male" {
			return MediaOptions{}, ErrOptionsInvalid
		}
		if !descriptor.SupportsGender && options.Gender != "" {
			return MediaOptions{}, ErrOptionsInvalid
		}
		fallthrough
	case "audio":
		if descriptor.DurationFromInput && options.Duration != 0 {
			return MediaOptions{}, fmt.Errorf("%w: duration follows the input media", ErrOptionsInvalid)
		}
		if !descriptor.DurationFromInput && descriptor.MaxDuration > 0 {
			if options.Duration == 0 {
				options.Duration = descriptor.DefaultDuration
			}
			if options.Duration < descriptor.MinDuration || options.Duration > descriptor.MaxDuration ||
				(descriptor.DurationStep > 0 && (options.Duration-descriptor.MinDuration)%descriptor.DurationStep != 0) {
				return MediaOptions{}, ErrOptionsInvalid
			}
		}
	default:
		options, err = s.normalizeVideoOptions(descriptor, options)
		if err != nil {
			return MediaOptions{}, err
		}
	}
	if options.Format == "" && len(descriptor.Formats) > 0 {
		options.Format = descriptor.Formats[0]
	}
	if len(descriptor.Formats) > 0 && !slices.Contains(descriptor.Formats, options.Format) {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if descriptor.MediaType != "video" && options.Seed != nil && (*options.Seed < 0 || *options.Seed > 2147483647) {
		return MediaOptions{}, ErrOptionsInvalid
	}
	return options, nil
}

func (s *TaskService) normalizeVideoOptions(descriptor MediaModel, options MediaOptions) (MediaOptions, error) {
	if options.Resolution == "" {
		if len(descriptor.Resolutions) > 0 {
			options.Resolution = descriptor.Resolutions[0]
		}
	}
	if options.Ratio == "" {
		if slices.Contains(descriptor.Ratios, "16:9") {
			options.Ratio = "16:9"
		} else if len(descriptor.Ratios) > 0 {
			options.Ratio = descriptor.Ratios[0]
		}
	}
	if descriptor.DurationFromInput {
		if options.Duration != 0 {
			return MediaOptions{}, fmt.Errorf("%w: duration follows the input video", ErrOptionsInvalid)
		}
	} else if options.Duration == 0 {
		options.Duration = descriptor.DefaultDuration
	}
	if (len(descriptor.Resolutions) > 0 && !slices.Contains(descriptor.Resolutions, options.Resolution)) ||
		(len(descriptor.Ratios) > 0 && !slices.Contains(descriptor.Ratios, options.Ratio)) ||
		(len(descriptor.Ratios) == 0 && options.Ratio != "") ||
		(!descriptor.DurationFromInput && (options.Duration < descriptor.MinDuration || options.Duration > descriptor.MaxDuration)) ||
		(descriptor.DurationStep > 0 && (options.Duration-descriptor.MinDuration)%descriptor.DurationStep != 0) ||
		(options.GenerateAudio != nil && *options.GenerateAudio && !descriptor.SupportsAudio) ||
		(options.Draft != nil && *options.Draft && (!descriptor.SupportsDraft ||
			(len(descriptor.DraftResolutions) > 0 && !slices.Contains(descriptor.DraftResolutions, options.Resolution)))) ||
		(options.CameraFixed != nil && !descriptor.SupportsCameraFixed) ||
		(options.ReturnLastFrame != nil && *options.ReturnLastFrame && !descriptor.SupportsLastFrame) {
		return MediaOptions{}, ErrOptionsInvalid
	}
	if options.Seed != nil && (*options.Seed < 0 || *options.Seed > 4294967295) {
		return MediaOptions{}, ErrOptionsInvalid
	}
	return options, nil
}

func validateOptionalURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "data") || (parsed.Scheme != "data" && parsed.Host == "") || parsed.User != nil {
		return ErrOptionsInvalid
	}
	return nil
}

func (s *TaskService) findModel(ctx context.Context, provider, model string) (MediaModel, bool) {
	providerAdapter, _ := s.providerFor(ctx, provider)
	if providerAdapter == nil {
		return fallbackMediaModel(model), true
	}
	if catalog, ok := providerAdapter.(ModelCatalog); ok {
		models, err := catalog.ListModels(ctx)
		if err != nil {
			return MediaModel{}, false
		}
		for _, item := range models {
			if item.ID == model {
				return item, true
			}
		}
		return MediaModel{}, false
	}
	if model == s.providerDefaults[provider] {
		return fallbackMediaModel(model), true
	}
	return MediaModel{}, false
}

func (s *TaskService) ListProviders(ctx context.Context) []ProviderDescriptor {
	available := s.providers
	if s.directory != nil {
		var err error
		available, err = s.directory.Providers(ctx)
		if err != nil {
			return nil
		}
	}
	// Load the complete policy once; model-specific resolution stays in memory.
	var policy markup.Policy
	var markupErr error
	reader, hasPolicy := s.repo.(markup.Reader)
	shared := identity.ProviderOwnerID(ctx) == identity.SharedOwnerID
	if shared && hasPolicy {
		policy, markupErr = reader.MarkupPolicy(ctx, identity.OwnerID(ctx))
	}
	ids := make([]string, 0, len(available))
	for id := range available {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	result := make([]ProviderDescriptor, 0, len(ids))
	for _, id := range ids {
		configured := true
		if availability, ok := available[id].(ProviderAvailability); ok {
			enabled, available, err := availability.Availability(ctx)
			if err != nil || !enabled || !available {
				continue
			}
			configured = available
		}
		defaultModel := s.providerDefaults[id]
		models := []MediaModel{fallbackMediaModel(defaultModel)}
		if catalog, ok := available[id].(ModelCatalog); ok {
			listed, err := catalog.ListModels(ctx)
			if err != nil || len(listed) == 0 {
				continue
			}
			models = listed
		}
		models = uniqueModels(models, defaultModel)
		for i := range models {
			if models[i].Pricing.ReferenceQuote == nil {
				if identity.ProviderOwnerID(ctx) == identity.SharedOwnerID && !ProviderIsFree(available[id]) {
					models[i].Pricing = MediaModelPricing{}
				}
				continue
			}
			cost := *models[i].Pricing.ReferenceQuote
			cost.MarkupPercent = 0
			priceMarkupErr := markupErr
			if shared {
				if hasPolicy {
					if policy.Company.Version > 0 {
						cost.MarkupPercent = policy.Resolve(id, models[i].ID).Percent
					}
				} else if rates, ok := s.repo.(MarkupReader); ok {
					quote := Task{OwnerID: identity.OwnerID(ctx), ProviderOwnerID: identity.ProviderOwnerID(ctx), Provider: id, Model: models[i].ID, Cost: &cost}
					priceMarkupErr = rates.ApplyMarkup(ctx, &quote)
				} else {
					priceMarkupErr = ErrPricingUnavailable
				}
			}
			amount, priceErr := cost.CustomerExpected()
			if priceMarkupErr != nil || priceErr != nil {
				models[i].Pricing = MediaModelPricing{}
				continue
			}
			models[i].Pricing.ReferenceQuote = &cost
			models[i].Pricing.ReferencePriceUSD = float64(amount) / 1_000_000
		}
		name := id

		if presentation, ok := available[id].(interface{ DisplayName() string }); ok {
			name = presentation.DisplayName()
		}
		descriptor := ProviderDescriptor{
			ID: id, Name: name, Live: !ProviderIsFree(available[id]), CredentialsConfigured: configured,
			DefaultModel: defaultModel, Models: models,
		}
		if artwork, ok := available[id].(interface{ ProviderIcon() (string, bool) }); ok {
			descriptor.IconURL, descriptor.IconMonochrome = artwork.ProviderIcon()
		}
		result = append(result, descriptor)
	}
	return result
}

func uniqueModels(values []MediaModel, preferred string) []MediaModel {
	seen := make(map[string]bool, len(values)+1)
	result := make([]MediaModel, 0, len(values)+1)
	for _, value := range values {
		if value.ID != "" && !seen[value.ID] {
			result = append(result, value)
			seen[value.ID] = true
		}
	}
	if preferred != "" && !seen[preferred] {
		result = append(result, fallbackMediaModel(preferred))
	}
	slices.SortStableFunc(result, func(a, b MediaModel) int {
		if a.CatalogDate() != b.CatalogDate() {
			return strings.Compare(b.CatalogDate(), a.CatalogDate())
		}
		return strings.Compare(a.ID, b.ID)
	})
	return result
}

func fallbackMediaModel(id string) MediaModel {
	return MediaModel{
		ID: id, DisplayName: id, MediaType: "video",
		Resolutions: []string{"480p", "720p", "1080p"},
		Ratios:      []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"},
		MinDuration: 2, MaxDuration: 12, DefaultDuration: 5,
		Pricing: MediaModelPricing{ReferenceDescription: "Price unknown"},
	}
}

func requestHash(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *TaskService) GetTask(ctx context.Context, id string) (Task, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Task{}, ErrTaskNotFound
	}
	return s.repo.Get(ctx, id)
}

func (s *TaskService) ListTasks(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return s.repo.ListRecent(ctx, limit)
}

func (s *TaskService) Info() ServiceInfo {
	return ServiceInfo{Provider: s.provider, Model: s.model, Live: !ProviderIsFree(s.providers[s.provider])}
}

func (s *TaskService) BillingStatus(ctx context.Context) ([]BillingStatus, error) {
	if reader, ok := s.repo.(interface {
		BillingStatus(context.Context) ([]BillingStatus, error)
	}); ok {
		return reader.BillingStatus(ctx)
	}
	return []BillingStatus{}, nil
}

func ProviderIsFree(p MediaProvider) bool {
	v, ok := p.(interface{ IsFree() bool })
	return ok && v.IsFree()
}

// ListTasksPage filters authors in the repository before pagination.
func (s *TaskService) ListTasksPage(ctx context.Context, limit, offset int) ([]Task, error) {
	if repo, ok := s.repo.(interface {
		ListRecentPage(context.Context, int, int) ([]Task, error)
	}); ok {
		return repo.ListRecentPage(ctx, limit, offset)
	}
	tasks, err := s.repo.ListRecent(ctx, limit+offset)
	if err != nil {
		return nil, err
	}
	if offset >= len(tasks) {
		return []Task{}, nil
	}
	return tasks[offset:], nil
}
