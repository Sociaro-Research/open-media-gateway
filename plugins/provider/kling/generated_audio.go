package kling

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

const BindGeneratedAudio = "kling-bind-generated-audio"
const generatedAudioOperation = "generated-audio-v1:"
const generatedAudioLifetime = 30 * 24 * time.Hour

func generatedAudioModel() core.MediaModel {
	return core.MediaModel{ID: BindGeneratedAudio, DisplayName: "Kling · Bind generated TTS audio", MediaType: "resource", NoPrompt: true, NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true, ReferenceModes: []core.ReferenceMode{{ID: "none", Label: "Owned TTS output"}},
		ResourceOperation: &core.ResourceOperationSchema{Kind: "file_resource", Subtypes: []string{"audio"}, NameMaxCharacters: 256, Variants: []core.ResourceVariantSchema{{Subtype: "audio", MinSources: 1, MaxSources: 1, GeneratedSources: []core.GeneratedResourceRule{{Model: TextToSpeech, MediaType: "audio", RequiresInspectedOutput: true, MinDuration: 2, MaxDuration: 300, MaxAgeSeconds: 2592000}}, Help: "Select an archived, byte-inspected Kling TTS audio output generated within 30 days. Binding is local and makes no provider request."}}},
		ResourceOutputs:   []core.ResourceOutputSchema{{Kind: "file_resource", GatewayRetentionSeconds: 2592000, RequiresInspectedInput: true, ProviderExpiryKnown: true}}, Pricing: core.MediaModelPricing{Known: true, ReferenceDescription: "Gateway-local binding of an already generated TTS output; no provider operation or charge."}}
}
func generatedAudioScope(scopes []context.Context) context.Context {
	if len(scopes) > 0 {
		return scopes[0]
	}
	return nil
}
func (a *Adapter) prepareGeneratedAudio(ctx context.Context, r core.MediaRequest) (*core.GeneratedResourceSource, error) {
	if _, e := legacyScope(ctx); e != nil {
		return nil, e
	}
	m := generatedAudioModel()
	o := r.Options
	if r.Model != BindGeneratedAudio || r.Prompt != "" || o.SessionAction != nil || o.OutputCount != 0 || o.Duration != 0 || o.Ratio != "" || o.Resolution != "" || o.GenerateAudio != nil || o.Seed != nil || o.Watermark != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Quality != "" || o.Background != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.OverrideSoundID != "" || o.OverrideSound != nil || len(o.ImageURLs) > 0 || len(o.Videos()) > 0 || len(o.Audios()) > 0 || len(o.ReferenceVoices) > 0 || len(o.ReferenceRoles) > 0 || len(o.ReferenceMediaEvidence) > 0 || len(o.ResourceReferences) > 0 || len(o.ResourceSnapshots) > 0 || len(o.Parameters) > 0 || (o.ReferenceMode != "" && o.ReferenceMode != "none") {
		return nil, resourceInvalid("generated-audio binding accepts only local generated task/artifact intent and host evidence")
	}
	if e := core.ValidateResourceCreationIntent(m, o, false); e != nil {
		return nil, e
	}
	creation := o.ResourceCreation
	if creation.GeneratedMedia == nil || creation.GeneratedVideo != nil || creation.ExternalTaskID != "" || len(creation.Images)+len(creation.Videos)+len(creation.VoiceSources)+len(creation.FileSources) > 0 || creation.BoundVoice != nil || creation.Description != "" || len(creation.Tags) > 0 {
		return nil, resourceInvalid("generated-audio binding requires exactly one archived TTS output")
	}
	g := core.CloneGeneratedResourceSource(creation.GeneratedMedia)
	if e := core.ValidateGeneratedMediaSource(m, "audio", g); e != nil {
		return nil, e
	}
	if !validTaskID(g.OutputID) || g.TaskID != o.ResourceIntent.GeneratedTaskID || o.ResourceIntent.GeneratedArtifactID != "" && g.ArtifactID != o.ResourceIntent.GeneratedArtifactID || g.CreatedAt.After(time.Now()) || !time.Now().Before(g.ExpiresAt) {
		return nil, resourceInvalid("generated TTS audio is expired or does not match the accepted local output")
	}
	if id, e := uuid.Parse(g.TaskID); e != nil || id.String() != g.TaskID {
		return nil, resourceInvalid("generated TTS audio requires a canonical local task identity")
	}
	return g, nil
}

type generatedAudioProof struct {
	TaskID           string    `json:"task"`
	ArtifactID       string    `json:"artifact"`
	SHA256           string    `json:"sha256"`
	InspectionSHA256 string    `json:"inspection_sha256"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	Duration         float64   `json:"duration"`
}

func generatedAudioInspectionHash(e *core.InputMediaEvidence) string {
	raw, _ := json.Marshal(e)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (a *Adapter) generateAudioBinding(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	g, e := a.prepareGeneratedAudio(ctx, r)
	if e != nil {
		return core.ProviderTask{}, e
	}
	if id, e := uuid.Parse(r.TaskID); e != nil || id.String() != r.TaskID {
		return core.ProviderTask{}, resourceInvalid("generated-audio binding requires a canonical Gateway task UUID")
	}
	proof := generatedAudioProof{TaskID: g.TaskID, ArtifactID: g.ArtifactID, SHA256: g.SHA256, InspectionSHA256: generatedAudioInspectionHash(g.Inspection), CreatedAt: g.CreatedAt.UTC(), ExpiresAt: g.ExpiresAt.UTC(), Duration: *g.Duration}
	raw, _ := json.Marshal(proof)
	handle, e := a.signLegacy(ctx, generatedAudioOperation+base64.RawURLEncoding.EncodeToString(raw), g.OutputID)
	if e != nil {
		return core.ProviderTask{}, e
	}
	return core.ProviderTask{Resources: []core.ProviderResource{{RemoteID: handle, Kind: "file_resource", Subtype: "audio", Name: r.Options.ResourceCreation.Name, Status: core.ProviderSucceeded}}, Usage: &core.ProviderUsage{CostStatus: "metered"}}, nil
}
func (a *Adapter) quoteGeneratedAudio(ctx context.Context, r core.MediaRequest) (core.TaskCost, string, error) {
	if _, e := a.prepareGeneratedAudio(ctx, r); e != nil {
		return core.TaskCost{}, "", e
	}
	cost := core.FreeCost(time.Now().UTC())
	cost.Pricing.Source = "Gateway-local generated TTS binding"
	return cost, "", nil
}
func generatedAudioEvidenceDuration(b core.ResourceSnapshot, max float64, allowExpired bool) (float64, error) {
	invalid := func() (float64, error) {
		return 0, resourceInvalid("generated audio requires immutable inspected TTS provenance")
	}
	if b.Kind != "file_resource" || b.Subtype != "audio" || b.IsPreset || b.Session != nil || b.ConnectionIdentity == "" || len(b.Sources) != 1 {
		return invalid()
	}
	s := b.Sources[0]
	p := s.Provenance
	e := s.Inspection
	if p == nil || e == nil || s.Duration == nil || s.MediaType != "audio" || p.Model != TextToSpeech || p.TaskID != s.ReferenceID || !core.ValidGeneratedArtifactID(p.ArtifactID) || e.ReferenceID != s.ReferenceID || e.Type != "audio" || e.SHA256 != s.SHA256 || e.SizeBytes <= 0 || e.ContentType == "" || e.Duration != *s.Duration || !resourceSHA.MatchString(s.SHA256) || *s.Duration < 2 || *s.Duration > max || math.IsNaN(*s.Duration) || math.IsInf(*s.Duration, 0) || p.CreatedAt.IsZero() || p.CreatedAt.After(time.Now()) || !p.ExpiresAt.After(p.CreatedAt) || p.ExpiresAt.After(p.CreatedAt.Add(generatedAudioLifetime)) || !allowExpired && !time.Now().Before(p.ExpiresAt) {
		return invalid()
	}
	if id, err := uuid.Parse(s.ReferenceID); err != nil || id.String() != s.ReferenceID {
		return invalid()
	}
	return *s.Duration, nil
}
func (a *Adapter) generatedAudioID(ctx context.Context, b core.ResourceSnapshot, max float64, allowExpired bool) (string, float64, error) {
	if ctx == nil {
		return "", 0, resourceInvalid("generated audio requires a host-authorized concrete connection scope")
	}
	if id, e := uuid.Parse(b.BindingID); e != nil || id.String() != b.BindingID {
		return "", 0, resourceInvalid("generated audio requires a canonical Gateway binding identity")
	}
	duration, e := generatedAudioEvidenceDuration(b, max, allowExpired)
	if e != nil {
		return "", 0, e
	}
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || b.ConnectionIdentity != scope.Revision {
		return "", 0, core.ErrProviderChanged
	}
	h, e := a.parseLegacy(ctx, b.RemoteID)
	if e != nil || !strings.HasPrefix(h.Operation, generatedAudioOperation) {
		return "", 0, resourceInvalid("invalid generated-audio binding for this owner, connection or revision")
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(h.Operation, generatedAudioOperation))
	var proof generatedAudioProof
	if e != nil || json.Unmarshal(raw, &proof) != nil {
		return "", 0, resourceInvalid("invalid generated-audio binding proof")
	}
	s := b.Sources[0]
	p := s.Provenance
	if proof.TaskID != p.TaskID || proof.ArtifactID != p.ArtifactID || proof.SHA256 != s.SHA256 || proof.InspectionSHA256 != generatedAudioInspectionHash(s.Inspection) || !proof.CreatedAt.Equal(p.CreatedAt) || !proof.ExpiresAt.Equal(p.ExpiresAt) || proof.Duration != duration {
		return "", 0, resourceInvalid("generated-audio binding evidence changed")
	}
	return h.ID, duration, nil
}
func (a *Adapter) validateGeneratedAudioConsumers(ctx context.Context, r core.MediaRequest) error {
	max := 300.0
	if r.Model == LipSync {
		max = 60
	} else if r.Model != AvatarImageToVideo {
		return nil
	}
	for _, b := range r.Options.ResourceSnapshots {
		if b.Kind == "file_resource" {
			if _, _, e := a.generatedAudioID(ctx, b, max, false); e != nil {
				return e
			}
		}
	}
	return nil
}
func (a *Adapter) ValidateResourceDeletion(ctx context.Context, b core.ResourceSnapshot) error {
	if b.Kind == "file_resource" {
		_, _, e := a.generatedAudioID(ctx, b, 300, true)
		return e
	}
	if _, e := legacyScope(ctx); e != nil {
		return e
	}
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	if _, e := uuid.Parse(b.BindingID); e != nil || !slices.Contains([]string{"voice", "element"}, b.Kind) || !validTaskID(b.RemoteID) || b.IsPreset || b.ConnectionIdentity != scope.Revision {
		return resourceInvalid("deletion requires an authorized custom-resource binding on this connection revision")
	}
	return nil
}
