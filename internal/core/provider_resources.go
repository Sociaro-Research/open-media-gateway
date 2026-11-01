package core

import (
	"context"
	"time"
)

// ResourceSourceInput is resolved by the host from an authorized inspected
// reference. It is never decoded directly from a public request.
type ResourceSourceInput struct {
	AudioCodecs  []string `json:"audio_codecs,omitempty"`
	FPS          float64  `json:"fps,omitempty"`
	FrameCount   int64    `json:"frame_count,omitempty"`
	AudioStreams *int     `json:"audio_streams,omitempty"`
	VideoStreams *int     `json:"video_streams,omitempty"`
	ReferenceID  string
	URL          string
	MediaType    string
	ContentType  string
	SizeBytes    int64
	Width        int
	Height       int
	SHA256       string
	Duration     *float64
}

// GeneratedResourceSource pins a completed owner-authorized generation. Provider
// video-ID eligibility is operation-specific, never inferred from an opaque ID.
type GeneratedResourceSource struct {
	MediaType   string
	ArtifactID  string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Inspection  *InputMediaEvidence
	TaskID      string
	TaskHandle  string
	OutputID    string
	SHA256      string
	RemoteID    string
	Operation   string
	Model       string
	NativeAudio bool
	Duration    *float64
}
type ResourceCreateRequest struct {
	ExternalTaskID string
	Kind           string
	Subtype        string
	Name           string
	Description    string
	FileSources    []ResourceSourceInput
	Images         []ResourceSourceInput
	Videos         []ResourceSourceInput
	VoiceSources   []ResourceSourceInput
	GeneratedVideo *GeneratedResourceSource
	GeneratedMedia *GeneratedResourceSource
	BoundVoice     *ResourceSnapshot
	Tags           []string
}

// ProviderResource IDs and preview sources are private adapter evidence. Public
// resources use a Gateway binding ID and owner-authorized archived references.
type ProviderResource struct {
	Session     *MediaSessionEvidence
	RemoteID    string
	Kind        string
	Subtype     string
	Name        string
	Description string
	Status      ProviderStatus
	IsDeleted   bool
	IsPreset    bool
	Sources     []ResourceSourceInput
	PreviewURL  string
	BoundVoice  *ProviderResource
	Tags        []string
}
type ResourceJob struct {
	Handle    string
	Status    ProviderStatus
	Resources []ProviderResource
	Usage     *ProviderUsage
	Warning   string
	Error     string
}
type ResourcePage struct {
	Resources []ProviderResource
	Page      int
	PageSize  int
	HasMore   bool
}

// ResourceProvider is separate from generation. The host injects the authorized
// data owner and concrete connection revision for every lifecycle action. A
// submitted deletion is not proof of terminal deletion.
type ResourceProvider interface {
	CreateResource(context.Context, ResourceCreateRequest) (ResourceJob, error)
	GetResourceJob(context.Context, string, string) (ResourceJob, error)
	ListResources(context.Context, string, bool, int, int) (ResourcePage, error)
	DeleteResource(context.Context, ResourceSnapshot) (ResourceJob, error)
}

// ResourceIntent is the immutable public request identity for creating a managed
// resource. All source bytes, remote IDs and provenance are resolved by the host.
type ResourceIntent struct {
	Kind                string   `json:"kind"`
	Subtype             string   `json:"subtype,omitempty"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	ReferenceIDs        []string `json:"reference_ids,omitempty"`
	GeneratedTaskID     string   `json:"generated_task_id,omitempty"`
	GeneratedArtifactID string   `json:"generated_artifact_id,omitempty"`
	BoundVoiceBindingID string   `json:"bound_voice_binding_id,omitempty"`
	Tags                []string `json:"tags,omitempty"`
}
type ResourceOperationSchema struct {
	DescriptionRequired      bool                    `json:"description_required,omitempty"`
	MaxTags                  int                     `json:"max_tags,omitempty"`
	NameMaxCharacters        int                     `json:"name_max_characters,omitempty"`
	SupportsDescription      bool                    `json:"supports_description"`
	DescriptionMaxCharacters int                     `json:"description_max_characters,omitempty"`
	Variants                 []ResourceVariantSchema `json:"variants,omitempty"`
	Tags                     []ResourceTagChoice     `json:"tags,omitempty"`
	Kind                     string                  `json:"kind"`
	Subtypes                 []string                `json:"subtypes,omitempty"`
}

type ResourceTagChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type MediaDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}
type GeneratedResourceRule struct {
	MediaType               string  `json:"media_type,omitempty"`
	RequiresInspectedOutput bool    `json:"requires_inspected_output,omitempty"`
	AllowUnknownDuration    bool    `json:"allow_unknown_duration,omitempty"`
	Model                   string  `json:"model"`
	NativeAudioRequired     bool    `json:"native_audio_required,omitempty"`
	MinDuration             float64 `json:"min_duration"`
	MaxDuration             float64 `json:"max_duration"`
	MaxAgeSeconds           int64   `json:"max_age_seconds,omitempty"`
}
type ResourceVariantSchema struct {
	Subtype               string                  `json:"subtype"`
	MinSources            int                     `json:"min_sources"`
	MaxSources            int                     `json:"max_sources"`
	SourceKinds           []string                `json:"source_kinds"`
	Limits                *ReferenceMediaLimits   `json:"limits,omitempty"`
	AllowedDimensions     []MediaDimensions       `json:"allowed_dimensions,omitempty"`
	SupportsBoundVoice    bool                    `json:"supports_bound_voice,omitempty"`
	GeneratedSources      []GeneratedResourceRule `json:"generated_sources,omitempty"`
	FirstSourceLabel      string                  `json:"first_source_label,omitempty"`
	AdditionalSourceLabel string                  `json:"additional_source_label,omitempty"`
	Help                  string                  `json:"help,omitempty"`
}

type ResourceOutputSchema struct {
	Kind                    string `json:"kind"`
	GatewayRetentionSeconds int64  `json:"gateway_retention_seconds,omitempty"`
	RequiresInspectedInput  bool   `json:"requires_inspected_input,omitempty"`
	ProviderExpiryKnown     bool   `json:"provider_expiry_known"`
}

// ResourceDeletionValidator is an optional pure preflight. It must not perform
// network requests or mutate provider state. Known unsupported deletions are
// rejected before the host reserves a job or makes a usable binding unavailable.
type ResourceDeletionValidator interface {
	ValidateResourceDeletion(context.Context, ResourceSnapshot) error
}
