// Package providerresources persists owner- and connection-bound provider resources.
package providerresources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("provider resource not found")
	ErrConflict = errors.New("provider resource intent or lifecycle conflict")
	ErrInvalid  = errors.New("invalid provider resource metadata")
)

// ErrInUse means an admitted task or active element retains the binding.
var ErrInUse = errors.New("provider resource is retained by active work")

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Deleting  Status = "deleting"
	Deleted   Status = "deleted"
)

type Operation string

const (
	Create Operation = "create"
	Delete Operation = "delete"
)

// Intent is an immutable typed metadata snapshot. It contains local identifiers
// and inspected evidence, never expiring URLs, credentials or adapter responses.
type Intent struct {
	Kind                string                        `json:"kind"`
	Subtype             string                        `json:"subtype,omitempty"`
	Name                string                        `json:"name"`
	Description         string                        `json:"description,omitempty"`
	IsPreset            bool                          `json:"is_preset,omitempty"`
	Sources             []core.ResourceSourceEvidence `json:"sources,omitempty"`
	BoundVoiceBindingID string                        `json:"bound_voice_binding_id,omitempty"`
	GeneratedTaskID     string                        `json:"generated_task_id,omitempty"`
	GeneratedArtifactID string                        `json:"generated_artifact_id,omitempty"`
	Tags                []string                      `json:"tags,omitempty"`
}

type Binding struct {
	OriginTaskID        string                        `json:"-"`
	Session             *core.MediaSessionEvidence    `json:"-"`
	ID                  string                        `json:"id"`
	OwnerID             string                        `json:"-"`
	ActorID             string                        `json:"-"`
	ProviderID          string                        `json:"provider"`
	ConnectionID        string                        `json:"-"`
	ProviderOwnerID     string                        `json:"-"`
	Revision            string                        `json:"-"`
	RemoteID            string                        `json:"-"`
	Kind                string                        `json:"kind"`
	Subtype             string                        `json:"subtype,omitempty"`
	Name                string                        `json:"name"`
	Description         string                        `json:"description,omitempty"`
	Status              Status                        `json:"status"`
	IsPreset            bool                          `json:"is_preset,omitempty"`
	Sources             []core.ResourceSourceEvidence `json:"-"`
	BoundVoiceBindingID string                        `json:"bound_voice_binding_id,omitempty"`
	CreatedAt           time.Time                     `json:"created_at"`
	UpdatedAt           time.Time                     `json:"updated_at"`
	DeletedAt           *time.Time                    `json:"deleted_at,omitempty"`
}

type Job struct {
	Kind            string     `json:"kind"`
	ID              string     `json:"id"`
	BindingID       string     `json:"binding_id"`
	OwnerID         string     `json:"-"`
	ActorID         string     `json:"-"`
	ProviderID      string     `json:"provider"`
	ConnectionID    string     `json:"-"`
	ProviderOwnerID string     `json:"-"`
	Revision        string     `json:"-"`
	Operation       Operation  `json:"operation"`
	Status          Status     `json:"status"`
	Intent          Intent     `json:"-"`
	Handle          string     `json:"-"`
	SafeError       string     `json:"error,omitempty"`
	Warning         string     `json:"warning,omitempty"`
	IntentHash      string     `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// Repository transitions are owner-scoped. StartJob is a one-way submission
// gate: a running job with no handle is uncertain and must never be resubmitted.
type Repository interface {
	ReserveCreate(context.Context, Job, string) (Job, bool, error)
	GetBinding(context.Context, string) (Binding, error)
	ListBindings(context.Context, string, int, int) ([]Binding, error)
	GetJob(context.Context, string) (Job, error)
	StartJob(context.Context, string) (Job, bool, error)
	SaveHandle(context.Context, string, string, string) error
	CompleteCreate(context.Context, string, string, ...*core.MediaSessionEvidence) (Binding, error)
	CompleteResource(context.Context, string, core.ProviderResource) (Binding, error)
	RecordUncertain(context.Context, string, string) error
	FailJob(context.Context, string, string, string) error
	BeginDelete(context.Context, string, string) (Job, bool, error)
	CompleteDelete(context.Context, string) error
	// PendingJobs is a trusted worker scan; authenticated callers remain scoped.
	PendingJobs(context.Context, int) ([]Job, error)
}

var resourceReferenceAlias = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type ScopeResolver func(context.Context, string) (core.ProviderConnectionScope, error)
type Service struct {
	repo    Repository
	current ScopeResolver
}

func New(repo Repository, current ScopeResolver) *Service {
	return &Service{repo: repo, current: current}
}

func CloneSources(in []core.ResourceSourceEvidence) []core.ResourceSourceEvidence {
	return core.CloneResourceSources(in)
}
func CloneIntent(in Intent) Intent {
	in.Sources = CloneSources(in.Sources)
	in.Tags = slices.Clone(in.Tags)
	return in
}
func SafeFailure(raw string) string {
	if raw == "" {
		return ""
	}
	return "Provider resource operation failed. Confirm its status before retrying."
}
func SafeWarning(raw string) string {
	if raw == "" {
		return ""
	}
	return "The provider reported a resource warning. Review the resource before use."
}
func ValidRemoteID(id string) bool {
	return len(id) > 0 && len(id) <= 4096 && utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}
func ValidateIntent(in Intent) error {
	if !ValidKind(in.Kind) || (in.Kind == "element" && in.Subtype == "") || len(in.Subtype) > 64 || strings.TrimSpace(in.Name) == "" || len(in.Name) > 256 || len(in.Description) > 4096 || len(in.Sources) > 64 || len(in.Tags) > 32 {
		return ErrInvalid
	}
	for _, id := range []string{in.BoundVoiceBindingID, in.GeneratedTaskID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return ErrInvalid
			}
		}
	}
	if in.GeneratedArtifactID != "" && (in.GeneratedTaskID == "" || !core.ValidGeneratedArtifactID(in.GeneratedArtifactID)) {
		return ErrInvalid
	}
	for _, tag := range in.Tags {
		if len(tag) > 128 || !utf8.ValidString(tag) {
			return ErrInvalid
		}
	}
	for _, source := range in.Sources {
		if _, err := uuid.Parse(source.ReferenceID); err != nil {
			return ErrInvalid
		}
		if source.MediaType != "image" && source.MediaType != "video" && source.MediaType != "audio" {
			return ErrInvalid
		}
		digest, err := hex.DecodeString(source.SHA256)
		if err != nil || len(digest) != sha256.Size {
			return ErrInvalid
		}
		if source.Duration != nil && (*source.Duration <= 0 || math.IsNaN(*source.Duration) || math.IsInf(*source.Duration, 0)) {
			return ErrInvalid
		}
		if p := source.Provenance; p != nil {
			if p.TaskID != source.ReferenceID || !core.ValidGeneratedArtifactID(p.ArtifactID) || p.Model == "" || p.CreatedAt.IsZero() || !p.ExpiresAt.After(p.CreatedAt) || source.Inspection == nil || source.Inspection.SizeBytes <= 0 || source.Duration == nil {
				return ErrInvalid
			}
		}
		if e := source.Inspection; e != nil {
			if e.ReferenceID != source.ReferenceID || e.Type != source.MediaType || e.SHA256 != source.SHA256 || e.SizeBytes < 0 || e.Width < 0 || e.Height < 0 || e.FrameCount < 0 || e.FPS < 0 || math.IsNaN(e.FPS) || math.IsInf(e.FPS, 0) || e.Duration < 0 || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0) || e.AudioStreams != nil && *e.AudioStreams < 0 || e.VideoStreams != nil && *e.VideoStreams < 0 {
				return ErrInvalid
			}
			// Generated-video duration is optional upstream metadata. Retain
			// it independently from the observed archived-container timeline;
			// an inspected duration is not a fabricated native/billing value.
			generatedVideo := in.GeneratedTaskID != "" && source.ReferenceID == in.GeneratedTaskID && source.MediaType == "video"
			if !generatedVideo && (source.Duration == nil && e.Duration != 0 || source.Duration != nil && e.Duration != *source.Duration) {
				return ErrInvalid
			}
		}
	}
	return nil
}

// HashIntent includes the immutable concrete scope, never a random job ID.
func HashIntent(job Job) (string, error) {
	if err := ValidateIntent(job.Intent); err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Provider, Connection, ProviderOwner, Revision string
		Operation                                     Operation
		Binding                                       string
		Intent                                        Intent
	}{job.ProviderID, job.ConnectionID, job.ProviderOwnerID, job.Revision, job.Operation, func() string {
		if job.Operation == Delete {
			return job.BindingID
		}
		return ""
	}(), job.Intent})
	if err != nil {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func ValidKind(kind string) bool {
	return kind == "file_resource" || kind == "element" || kind == "voice" || kind == "face_session" || kind == "editing_session"
}
func CloneSession(in *core.MediaSessionEvidence) *core.MediaSessionEvidence {
	if in == nil {
		return nil
	}
	out := *in
	out.Faces = slices.Clone(in.Faces)
	out.Selections = slices.Clone(in.Selections)
	for i := range out.Selections {
		out.Selections[i].Points = slices.Clone(in.Selections[i].Points)
	}
	return &out
}
func ValidateSession(kind string, in *core.MediaSessionEvidence) error {
	sessionKind := kind == "face_session" || kind == "editing_session"
	if !sessionKind {
		if in != nil {
			return ErrInvalid
		}
		return nil
	}
	if in == nil || in.ExpiresAt.IsZero() || in.SourceVideoDuration <= 0 || math.IsNaN(in.SourceVideoDuration) || math.IsInf(in.SourceVideoDuration, 0) || len(in.Faces) > 256 || in.FrameCount < 0 || in.SelectionRevision < 0 || in.FPS < 0 || math.IsNaN(in.FPS) || math.IsInf(in.FPS, 0) {
		return ErrInvalid
	}
	digest, err := hex.DecodeString(in.SourceSHA256)
	if err != nil || len(digest) != sha256.Size {
		return ErrInvalid
	}
	for _, face := range in.Faces {
		if face.ID == "" || len(face.ID) > 512 || face.StartMS < 0 || face.EndMS < face.StartMS || float64(face.EndMS) > in.SourceVideoDuration*1000 {
			return ErrInvalid
		}
		if face.PreviewArtifactID != "" {
			index, err := strconv.Atoi(face.PreviewArtifactID)
			if err != nil || index < 1 || index > 64 || strconv.Itoa(index) != face.PreviewArtifactID {
				return ErrInvalid
			}
		}
	}
	if len(in.Selections) > 4096 || (len(in.Selections) > 0 && in.FrameCount == 0) {
		return ErrInvalid
	}
	points := 0
	frames := map[int]bool{}
	for _, selection := range in.Selections {
		if selection.FrameIndex < 0 || selection.FrameIndex >= in.FrameCount || frames[selection.FrameIndex] {
			return ErrInvalid
		}
		frames[selection.FrameIndex] = true
		points += len(selection.Points)
		if len(selection.Points) == 0 || points > 65536 {
			return ErrInvalid
		}
		for _, point := range selection.Points {
			if math.IsNaN(point.X) || math.IsNaN(point.Y) || math.IsInf(point.X, 0) || math.IsInf(point.Y, 0) || point.X < 0 || point.X > 1 || point.Y < 0 || point.Y > 1 {
				return ErrInvalid
			}
		}
	}
	return nil
}

// ValidateSessionProvenance ties reusable session metadata to inspected video
// bytes. Provider-reported duration/hash alone is never reusable evidence.
func ValidateSessionProvenance(kind string, session *core.MediaSessionEvidence, sources []core.ResourceSourceEvidence) error {
	if err := ValidateSession(kind, session); err != nil {
		return err
	}
	if session == nil {
		return nil
	}
	for _, source := range sources {
		if source.MediaType == "video" && source.SHA256 == session.SourceSHA256 && source.Duration != nil && *source.Duration == session.SourceVideoDuration {
			return nil
		}
	}
	return ErrInvalid
}
func EqualSources(a, b []core.ResourceSourceEvidence) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if !equalGeneratedProvenance(x.Provenance, y.Provenance) || !reflect.DeepEqual(x.Inspection, y.Inspection) || x.ReferenceID != y.ReferenceID || x.MediaType != y.MediaType || x.SHA256 != y.SHA256 || (x.Duration == nil) != (y.Duration == nil) {
			return false
		}
		if x.Duration != nil && *x.Duration != *y.Duration {
			return false
		}
	}
	return true
}
func equalGeneratedProvenance(a, b *core.GeneratedSourceProvenance) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.TaskID == b.TaskID && a.ArtifactID == b.ArtifactID && a.Model == b.Model && a.CreatedAt.Equal(b.CreatedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}
func EqualSession(a, b *core.MediaSessionEvidence) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if !a.ExpiresAt.Equal(b.ExpiresAt) || a.SourceVideoDuration != b.SourceVideoDuration || a.SourceSHA256 != b.SourceSHA256 || a.FrameCount != b.FrameCount || a.FPS != b.FPS || a.SelectionRevision != b.SelectionRevision || a.HasSelection != b.HasSelection || !slices.Equal(a.Faces, b.Faces) || len(a.Selections) != len(b.Selections) {
		return false
	}
	for i := range a.Selections {
		if a.Selections[i].FrameIndex != b.Selections[i].FrameIndex || !slices.Equal(a.Selections[i].Points, b.Selections[i].Points) {
			return false
		}
	}
	return true
}
func (s *Service) ResolveResourceReferences(ctx context.Context, providerID string, refs []core.ResourceReference) ([]core.ResourceSnapshot, error) {
	return s.resolveResourceReferences(ctx, providerID, refs, nil)
}
func (s *Service) ResolveSessionAction(ctx context.Context, providerID string, action *core.SessionActionIntent, refs []core.ResourceReference) ([]core.ResourceSnapshot, error) {
	if action == nil || len(refs) != 1 || action.BindingID != refs[0].BindingID || action.ExpectedRevision < 0 || action.Recover && action.Action != "clear" {
		return nil, ErrInvalid
	}
	return s.resolveResourceReferences(ctx, providerID, refs, action)
}
func (s *Service) resolveResourceReferences(ctx context.Context, providerID string, refs []core.ResourceReference, action *core.SessionActionIntent) ([]core.ResourceSnapshot, error) {
	if len(refs) == 0 {
		return []core.ResourceSnapshot{}, nil
	}
	if len(refs) > 32 || s.repo == nil || s.current == nil {
		return nil, ErrInvalid
	}
	scope, err := s.current(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if scope.ID == "" || scope.OwnerID != identity.ProviderOwnerID(ctx) || scope.Revision == "" {
		return nil, ErrNotFound
	}
	out := make([]core.ResourceSnapshot, 0, len(refs))
	seen := map[string]bool{}
	aliases := map[string]bool{}
	for _, ref := range refs {
		if _, err := uuid.Parse(ref.BindingID); err != nil || !resourceReferenceAlias.MatchString(ref.ReferenceID) || seen[ref.BindingID] || aliases[ref.ReferenceID] {
			return nil, ErrInvalid
		}
		seen[ref.BindingID] = true
		aliases[ref.ReferenceID] = true
		b, err := s.repo.GetBinding(ctx, ref.BindingID)
		if err != nil {
			return nil, err
		}
		allowedState := b.Status == Succeeded || action != nil && action.Recover && action.Action == "clear" && b.Kind == "editing_session" && b.Status == Failed
		if action != nil && (b.Session == nil || b.Session.SelectionRevision != action.ExpectedRevision) {
			return nil, ErrConflict
		}
		if b.OwnerID != identity.OwnerID(ctx) || b.ProviderOwnerID != scope.OwnerID || b.ProviderID != providerID || b.ConnectionID != scope.ID || b.Revision != scope.Revision || !allowedState || !ValidRemoteID(b.RemoteID) || !ValidKind(b.Kind) || (b.Kind == "element" && b.Subtype == "") {
			return nil, ErrNotFound
		}
		if err := ValidateSessionProvenance(b.Kind, b.Session, b.Sources); err != nil {
			return nil, ErrNotFound
		}
		if core.ResourceSourcesExpired(b.Sources, time.Now()) {
			return nil, ErrNotFound
		}
		if b.Session != nil && !time.Now().Before(b.Session.ExpiresAt) {
			return nil, ErrNotFound
		}
		if b.BoundVoiceBindingID != "" {
			voice, e := s.repo.GetBinding(ctx, b.BoundVoiceBindingID)
			if e != nil || voice.OwnerID != b.OwnerID || voice.ProviderOwnerID != b.ProviderOwnerID || voice.ProviderID != b.ProviderID || voice.ConnectionID != b.ConnectionID || voice.Revision != b.Revision || voice.Kind != "voice" || voice.Status != Succeeded || !ValidRemoteID(voice.RemoteID) {
				return nil, ErrNotFound
			}
		}
		out = append(out, core.ResourceSnapshot{Session: CloneSession(b.Session), BindingID: b.ID, ReferenceID: ref.ReferenceID, Kind: b.Kind, Subtype: b.Subtype, RemoteID: b.RemoteID, IsPreset: b.IsPreset, ConnectionIdentity: b.Revision, Sources: CloneSources(b.Sources)})
	}
	return out, nil
}

var _ core.ReferenceResourceResolver = (*Service)(nil)

var _ core.SessionActionResolver = (*Service)(nil)
