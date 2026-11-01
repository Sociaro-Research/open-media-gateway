package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"strings"
	"time"
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
)

var (
	ErrTaskNotFound            = errors.New("task not found")
	ErrWebhookDeliveryNotFound = errors.New("webhook delivery not found")
)

type Task struct {
	StoredResourceReferences []ResourceReference `json:"-"`
	StoredArtifacts          []StoredArtifact    `json:"-"`
	// Checked only for NEW admissions after idempotency resolution. Never stored.
	AdmissionError         error           `json:"-"`
	TemplateSnapshot       json.RawMessage `json:"template_snapshot,omitempty"`
	ProjectVersion         int64           `json:"project_version,omitempty"`
	ProjectID              string          `json:"project_id,omitempty"`
	IdentitySnapshot       json.RawMessage `json:"-"`
	MinimizeRequestLogging bool            `json:"-"`
	HadImageInput          bool            `json:"-"`
	PublicModel            string          `json:"-"`
	WorkerStartedAt        *time.Time      `json:"-"`
	WorkerPollErrors       int             `json:"-"`
	OrganizationID         string          `json:"organization_id,omitempty"`
	ActorID                string          `json:"actor_id,omitempty"`
	HasStoredResult        bool            `json:"has_stored_result"`
	ResultDeletedAt        *time.Time      `json:"result_deleted_at,omitempty"`
	ProviderOwnerID        string          `json:"-"`
	StorageOwnerID         string          `json:"-"`
	RequireCost            bool            `json:"-"`
	Cost                   *TaskCost       `json:"cost,omitempty"`
	MaxExpectedUSD         *money.USD      `json:"-"`
	ProviderIdentity       string          `json:"-"`
	APIKeyID               *string         `json:"api_key_id,omitempty"`
	ID                     string          `json:"id"`
	OwnerID                string          `json:"-"`
	Status                 TaskStatus      `json:"status"`
	Provider               string          `json:"provider"`
	Model                  string          `json:"model"`
	MediaType              string          `json:"media_type"`
	Prompt                 string          `json:"-"`
	Options                MediaOptions    `json:"options"`
	WebhookURL             *string         `json:"webhook_url,omitempty"`
	ProviderTaskID         *string         `json:"provider_task_id,omitempty"`
	ResultURL              *string         `json:"result_url,omitempty"`
	ProviderResultURL      *string         `json:"-"`
	LastFrameURL           *string         `json:"last_frame_url,omitempty"`
	LastFrameStorageKey    *string         `json:"-"`
	StorageKey             *string         `json:"-"`
	ResultType             *string         `json:"result_content_type,omitempty"`
	ResultSize             *int64          `json:"result_size_bytes,omitempty"`
	ResultSHA256           *string         `json:"result_sha256,omitempty"`
	StoredAt               *time.Time      `json:"stored_at,omitempty"`
	Error                  *string         `json:"error,omitempty"`
	Usage                  *ProviderUsage  `json:"usage,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

// StoredMedia describes a provider result after it has been copied to durable
// storage owned by the gateway. SourceURL is retained for diagnostics but is
// never returned from the public task API.
type StoredMedia struct {
	Resources           []ProviderResource
	Artifacts           []StoredArtifact
	LastFrameStorageKey string
	SourceURL           string
	StorageKey          string
	ContentType         string
	SizeBytes           int64
	SHA256              string
}

// MediaOptions contains provider-neutral media-generation controls. Provider
// adapters translate only the supported values into vendor-specific payloads.
// SoundOverride is an immutable, server-resolved source snapshot. Public requests
// supply only OverrideSoundID; storage identities are never accepted from clients.
type SoundOverride struct {
	DataOwnerID    string `json:"data_owner_id"`
	ReferenceID    string `json:"reference_id"`
	OwnerID        string `json:"owner_id"`
	StorageOwnerID string `json:"storage_owner_id"`
	StorageKey     string `json:"storage_key"`
	SHA256         string `json:"sha256"`
	SizeBytes      int64  `json:"size_bytes"`
}

type MediaOptions struct {
	ResourceIntent         *ResourceIntent               `json:"resource_intent,omitempty"`
	ResourceCreation       *ResourceCreateRequest        `json:"_resource_creation,omitempty"`
	ResourceReferences     []ResourceReference           `json:"resource_references,omitempty"`
	ResourceSnapshots      []ResourceSnapshot            `json:"_reference_resources,omitempty"`
	OutputCount            int                           `json:"output_count,omitempty"`
	ReferenceRoles         map[string]string             `json:"reference_roles,omitempty"`
	ReferenceMediaEvidence map[string]InputMediaEvidence `json:"_reference_media,omitempty"`
	OverrideSoundID        string                        `json:"override_sound_id,omitempty"`
	OverrideSound          *SoundOverride                `json:"_override_sound,omitempty"`
	TimeoutSeconds         *int64                        `json:"timeout_seconds,omitempty"`
	Parameters             map[string]any                `json:"parameters,omitempty"`
	Quality                string                        `json:"quality,omitempty"`
	Background             string                        `json:"background,omitempty"`
	ReferenceVoices        map[string]string             `json:"reference_voices,omitempty"`
	ReferenceMode          string                        `json:"reference_mode,omitempty"`
	VideoURLs              []string                      `json:"video_urls,omitempty"`
	AudioURLs              []string                      `json:"audio_urls,omitempty"`
	Resolution             string                        `json:"resolution"`
	Ratio                  string                        `json:"ratio"`
	Duration               int                           `json:"duration"`
	GenerateAudio          *bool                         `json:"generate_audio,omitempty"`
	Watermark              *bool                         `json:"watermark,omitempty"`
	CameraFixed            *bool                         `json:"camera_fixed,omitempty"`
	Draft                  *bool                         `json:"draft,omitempty"`
	Seed                   *int64                        `json:"seed,omitempty"`
	ReturnLastFrame        *bool                         `json:"return_last_frame,omitempty"`
	NegativePrompt         string                        `json:"negative_prompt,omitempty"`
	ImageURLs              []string                      `json:"image_urls,omitempty"`
	AudioURL               string                        `json:"audio_url,omitempty"`
	VideoURL               string                        `json:"video_url,omitempty"`
	Voice                  string                        `json:"voice,omitempty"`
	Language               string                        `json:"language,omitempty"`
	Instructions           string                        `json:"instructions,omitempty"`
	Lyrics                 string                        `json:"lyrics,omitempty"`
	Gender                 string                        `json:"gender,omitempty"`
	Format                 string                        `json:"format,omitempty"`
}

type CreateMediaInput struct {
	ResourceCreation  *ResourceCreateRequest        `json:"-"`
	ReferenceEvidence map[string]InputMediaEvidence `json:"-"`
	AdmissionError    error                         `json:"-"`
	TemplateSnapshot  json.RawMessage               `json:"-"`
	ProjectID         string                        `json:"project_id,omitempty"`
	PublicModel       string                        `json:"-"`
	MaxExpectedUSD    *money.USD                    `json:"max_expected_cost_usd,omitempty"`
	Prompt            string                        `json:"prompt"`
	Provider          string                        `json:"provider,omitempty"`
	Model             string                        `json:"model"`
	MediaType         string                        `json:"media_type,omitempty"`
	WebhookURL        string                        `json:"webhook_url,omitempty"`
	Options           MediaOptions                  `json:"options"`
}

type ServiceInfo struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Live     bool   `json:"live"`
	AuthMode string `json:"auth_mode"`
}

type ProviderDescriptor struct {
	IconURL               string       `json:"icon_url,omitempty"`
	IconMonochrome        bool         `json:"icon_monochrome,omitempty"`
	ID                    string       `json:"id"`
	Name                  string       `json:"name"`
	Live                  bool         `json:"live"`
	CredentialsConfigured bool         `json:"credentials_configured"`
	DefaultModel          string       `json:"default_model"`
	Models                []MediaModel `json:"models"`
}

// ReferencePromptRecommendation is informational API evidence, never admission
// grammar or permission to rewrite a prompt. Omission means no reviewed advice.
type ReferencePromptRecommendation struct {
	Text      string   `json:"text"`
	Kind      string   `json:"kind"`
	SourceURL string   `json:"source_url"`
	Modes     []string `json:"modes,omitempty"`
}

// AcceptsObservation restricts parsed advice to the reviewed endpoint/workflows.
// It does not grant capabilities or alter mandatory operational completeness.
func (r *ReferencePromptRecommendation) AcceptsObservation(p ReferencePromptRecommendation) bool {
	if r == nil || p.SourceURL != r.SourceURL || p.Kind != "example" || strings.TrimSpace(p.Text) == "" || len(p.Text) > 2048 || len(p.Modes) != len(r.Modes) {
		return false
	}
	for i, mode := range r.Modes {
		if p.Modes[i] != mode {
			return false
		}
	}
	return true
}

func (r *ReferencePromptRecommendation) Clone() *ReferencePromptRecommendation {
	if r == nil {
		return nil
	}
	copy := *r
	copy.Modes = append([]string(nil), r.Modes...)
	return &copy
}

type MediaModel struct {
	ResourceOutputs       []ResourceOutputSchema   `json:"resource_outputs,omitempty"`
	ReferenceRoleLimits   []MediaRoleLimits        `json:"reference_role_limits,omitempty"`
	ResourceOperation     *ResourceOperationSchema `json:"resource_operation,omitempty"`
	ResourceInputSchemas  []ResourceInputSchema    `json:"resource_input_schemas,omitempty"`
	MaxReferenceInputs    int                      `json:"max_reference_inputs,omitempty"`
	MaxOutputs            int                      `json:"max_outputs,omitempty"`
	PromptPolicy          *PromptPolicy            `json:"prompt_policy,omitempty"`
	ReferenceRoleSchemas  []ReferenceRoleSchema    `json:"reference_role_schemas,omitempty"`
	DurationMayBeInferred bool                     `json:"duration_may_be_inferred,omitempty"`
	RatioMayBeInferred    bool                     `json:"ratio_may_be_inferred,omitempty"`
	// MaxPromptCharacters is an enforced character ceiling, not an upstream limit claim.
	// Runtime API catalogues combine it with the gateway cap; zero is unspecified.
	MaxPromptCharacters           int                            `json:"max_prompt_characters,omitempty"`
	ReferencePromptRecommendation *ReferencePromptRecommendation `json:"reference_prompt_recommendation,omitempty"`
	ReferenceMediaLimits          *ReferenceMediaLimits          `json:"reference_media_limits,omitempty"`
	ReferencePromptTypes          []string                       `json:"reference_prompt_types,omitempty"`
	ReferencePromptSyntax         string                         `json:"reference_prompt_syntax,omitempty"`
	SupportsAssetReferences       bool                           `json:"supports_asset_references,omitempty"`

	NoPrompt               bool              `json:"no_prompt,omitempty"`
	PromptOptional         bool              `json:"prompt_optional,omitempty"`
	Manual                 bool              `json:"manual,omitempty"`
	PublicID               string            `json:"public_id,omitempty"`
	Metadata               *ModelMetadata    `json:"metadata,omitempty"`
	Parameters             []ParameterField  `json:"parameters,omitempty"`
	NoLanguage             bool              `json:"no_language,omitempty"`
	NoInstructions         bool              `json:"no_instructions,omitempty"`
	Qualities              []string          `json:"qualities,omitempty"`
	Backgrounds            []string          `json:"backgrounds,omitempty"`
	ReferenceModes         []ReferenceMode   `json:"reference_modes,omitempty"`
	DurationStep           int               `json:"duration_step,omitempty"`
	DurationFromInput      bool              `json:"duration_from_input,omitempty"`
	MaxSeed                int64             `json:"max_seed,omitempty"`
	NoSeed                 bool              `json:"no_seed,omitempty"`
	NoWatermark            bool              `json:"no_watermark,omitempty"`
	AudioAlwaysOn          bool              `json:"audio_always_on,omitempty"`
	ID                     string            `json:"id"`
	DisplayName            string            `json:"display_name"`
	MediaType              string            `json:"media_type"`
	Resolutions            []string          `json:"resolutions"`
	Ratios                 []string          `json:"ratios"`
	MinDuration            int               `json:"min_duration"`
	MaxDuration            int               `json:"max_duration"`
	DefaultDuration        int               `json:"default_duration"`
	SupportsAudio          bool              `json:"supports_audio"`
	SupportsDraft          bool              `json:"supports_draft"`
	DraftResolutions       []string          `json:"draft_resolutions,omitempty"`
	SupportsCameraFixed    bool              `json:"supports_camera_fixed"`
	SupportsLastFrame      bool              `json:"supports_last_frame"`
	SupportsImageInput     bool              `json:"supports_image_input,omitempty"`
	RequiresImageInput     bool              `json:"requires_image_input,omitempty"`
	MinImageInputs         int               `json:"min_image_inputs,omitempty"`
	MaxImageInputs         int               `json:"max_image_inputs,omitempty"`
	SupportsAudioInput     bool              `json:"supports_audio_input,omitempty"`
	RequiresAudioInput     bool              `json:"requires_audio_input,omitempty"`
	SupportsVideoInput     bool              `json:"supports_video_input,omitempty"`
	RequiresVideoInput     bool              `json:"requires_video_input,omitempty"`
	SupportsNegativePrompt bool              `json:"supports_negative_prompt,omitempty"`
	SupportsLyrics         bool              `json:"supports_lyrics,omitempty"`
	SupportsGender         bool              `json:"supports_gender,omitempty"`
	SupportsVoice          bool              `json:"supports_voice,omitempty"`
	RequiresVoice          bool              `json:"requires_voice,omitempty"`
	VoiceChoices           []VoiceChoice     `json:"voice_choices,omitempty"`
	Languages              []string          `json:"languages,omitempty"`
	Voices                 []string          `json:"voices,omitempty"`
	Formats                []string          `json:"formats,omitempty"`
	Pricing                MediaModelPricing `json:"pricing"`
}

type MediaModelPricing struct {
	ReferenceQuote            *TaskCost          `json:"reference_quote,omitempty"`
	Known                     bool               `json:"known"`
	USDPerMillionTokens       float64            `json:"usd_per_million_tokens,omitempty"`
	USDPerMillionAudioTokens  float64            `json:"usd_per_million_audio_tokens,omitempty"`
	USDPerMillionByResolution map[string]float64 `json:"usd_per_million_by_resolution,omitempty"`
	ReferencePriceUSD         float64            `json:"reference_price_usd"`
	ReferenceDescription      string             `json:"reference_description"`
	DraftSilentFactor         float64            `json:"draft_silent_factor,omitempty"`
	DraftAudioFactor          float64            `json:"draft_audio_factor,omitempty"`
}

type TaskRepository interface {
	// Resolve matching/conflicting idempotent requests first. Before inserting
	// any NEW task, reject task.AdmissionError without charging or enqueueing.
	CreateOrGet(context.Context, Task, string, string) (Task, bool, error)
	Get(context.Context, string) (Task, error)
	ListRecent(context.Context, int) ([]Task, error)
	MarkEnqueued(context.Context, string) error
	MarkRunning(context.Context, string) error
	SetProviderTaskID(context.Context, string, string) error
	SetProviderUsage(context.Context, string, ProviderUsage) error
	SetProviderResultURL(context.Context, string, string) error
	MarkSucceeded(context.Context, string, StoredMedia, string) error
	MarkFailed(context.Context, string, string) error
	ListRecoverable(context.Context) ([]string, error)
}

type TaskEventType string

const (
	EventTaskCreated   TaskEventType = "task.created"
	EventTaskStarted   TaskEventType = "task.started"
	EventTaskSucceeded TaskEventType = "task.succeeded"
	EventTaskFailed    TaskEventType = "task.failed"
)

type WebhookDeliveryStatus string

const (
	WebhookPending   WebhookDeliveryStatus = "pending"
	WebhookDelivered WebhookDeliveryStatus = "delivered"
	WebhookFailed    WebhookDeliveryStatus = "failed"
)

type WebhookDelivery struct {
	ID            string
	TaskID        string
	EventType     TaskEventType
	URL           string
	Payload       []byte
	Status        WebhookDeliveryStatus
	Attempts      int
	NextAttemptAt time.Time
	CreatedAt     time.Time
}

type WebhookRepository interface {
	ListDueWebhookDeliveries(context.Context, int) ([]string, error)
	GetWebhookDelivery(context.Context, string) (WebhookDelivery, error)
	MarkWebhookDelivered(context.Context, string) error
	MarkWebhookRetry(context.Context, string, int, time.Time, string, bool) error
}

type TaskQueue interface {
	Enqueue(context.Context, string) error
	Dequeue(context.Context) (string, error)
	Ack(context.Context, string) error
	Requeue(context.Context, string) error
	Recover(context.Context) error
}

type MediaRequest struct {
	ProviderIdentity string
	TaskID           string
	Prompt           string
	Model            string
	Options          MediaOptions
}

type ProviderTask struct {
	Resources    []ProviderResource
	Artifacts    []ProviderArtifact
	ID           string
	ResultURL    string
	LastFrameURL string
	Usage        *ProviderUsage
}

type ProviderStatus string

const (
	ProviderQueued    ProviderStatus = "queued"
	ProviderRunning   ProviderStatus = "running"
	ProviderSucceeded ProviderStatus = "succeeded"
	ProviderFailed    ProviderStatus = "failed"
)

type ProviderResult struct {
	Resources    []ProviderResource
	Artifacts    []ProviderArtifact
	Status       ProviderStatus
	ResultURL    string
	LastFrameURL string
	Error        string
	Usage        *ProviderUsage
}

// ProviderUsage keeps normalized billable units together with the original
// provider payload. Monetary cost remains an estimate until reconciliation.
type ProviderUsage struct {
	Metrics       []UsageMetric   `json:"metrics,omitempty"`
	Raw           json.RawMessage `json:"raw,omitempty"`
	EstimatedCost *float64        `json:"estimated_cost,omitempty"`
	Currency      string          `json:"currency,omitempty"`
	CostStatus    string          `json:"cost_status,omitempty"`
	CostSource    string          `json:"cost_source,omitempty"`
}

type UsageMetric struct {
	Name     string  `json:"name"`
	Unit     string  `json:"unit"`
	Quantity float64 `json:"quantity"`
}

// MediaProvider is the only provider contract known by the core.
// Vendor-specific payloads and status values belong in adapter packages.
type MediaProvider interface {
	Name() string
	Generate(context.Context, MediaRequest) (ProviderTask, error)
	GetTask(context.Context, string) (ProviderResult, error)
}

type ModelCatalog interface {
	ListModels(context.Context) ([]MediaModel, error)
}

// ProviderAvailability lets a dynamic provider registry hide disabled or
// unconfigured adapters without exposing administrative details to the core.
type ProviderAvailability interface {
	Availability(context.Context) (enabled bool, configured bool, err error)
}

// RequestValidator checks provider-specific combinations before quoting or accepting a task.
type RequestValidator interface {
	ValidateRequest(context.Context, MediaRequest) error
}

// ProviderOptions excludes gateway-only post-processing source identities.
func (o MediaOptions) ProviderOptions() MediaOptions {
	o.OverrideSoundID = ""
	o.OverrideSound = nil
	return o
}
