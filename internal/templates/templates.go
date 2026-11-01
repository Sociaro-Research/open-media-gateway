// Package templates defines current instance-wide generation recipes. It never
// owns a customer's key, inputs or results.
package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// LibraryOwnerID reserves a separate instance-wide source namespace; it never adopts private media.
const LibraryOwnerID = "00000000-0000-0000-0000-000000000002"

var ErrNotFound = errors.New("template or media is unavailable")
var ErrConflict = errors.New("template changed; reload and review before saving")
var ErrInvalid = errors.New("invalid template")

type Slot struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Role    string `json:"role"`
	Label   string `json:"label"`
	AssetID string `json:"asset_id,omitempty"`
}
type Definition struct {
	OverrideSoundID string            `json:"override_sound_id,omitempty"`
	ReferenceCounts map[string]int    `json:"reference_counts,omitempty"`
	Title           string            `json:"title"`
	Description     string            `json:"description,omitempty"`
	MediaType       string            `json:"media_type"`
	Provider        string            `json:"provider"`
	Model           string            `json:"model"`
	Prompt          string            `json:"prompt"`
	Syntax          string            `json:"syntax"`
	Slots           []Slot            `json:"slots"`
	Options         core.MediaOptions `json:"options"`
	CoverID         string            `json:"cover_id"`
}
type Record struct {
	ID       string `json:"id"`
	Version  int64  `json:"version"`
	Revision int64  `json:"revision"`
	// Draft is the legacy Go name of the ONE current content.
	Draft Definition `json:"draft"`
	// Published is a visibility view only, derived from the same current content.
	Published *Definition `json:"published,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// MarshalJSON keeps legacy clients working without exposing a second definition.
func (r Record) MarshalJSON() ([]byte, error) {
	type legacy Record
	value := legacy(r)
	if value.Published != nil {
		value.Published = &value.Draft
	}
	return json.Marshal(struct {
		legacy
		Content     Definition `json:"content"`
		IsPublished bool       `json:"is_published"`
	}{value, r.Draft, r.Published != nil})
}

type Asset struct {
	StorageOwnerID     string `json:"-"`
	Name               string `json:"name,omitempty"`
	LibraryReferenceID string `json:"library_reference_id,omitempty"`
	ID                 string `json:"id"`
	TemplateID         string `json:"template_id"`
	ContentType        string `json:"content_type"`
	SizeBytes          int64  `json:"size_bytes"`
	StorageKey         string `json:"-"`
	SHA256             string `json:"sha256"`
	Published          bool   `json:"-"`
}
type Repository interface {
	List(context.Context, bool) ([]Record, error)
	Get(context.Context, string) (Record, error)
	Save(context.Context, Record, int64, string) (Record, error)
	Asset(context.Context, string) (Asset, error)
	PutAsset(context.Context, Asset) error
	WithPublished(context.Context, string, int64, func(Record) error) error
}

var kinds = []string{"image", "video", "audio"}

// Keep these grammars in sync with ReferencePrompts. Templates are stricter than
// free-form Playground requests: ambiguous, malformed or gapped bindings cannot
// be published. Repeated valid mentions refer to the same slot.
func Infer(prompt, syntax string, types []string, mode core.ReferenceMode) ([]Slot, error) {
	if len(prompt) > 40000 {
		return nil, fmt.Errorf("%w: prompt is too long", ErrInvalid)
	}
	pattern := ""
	switch syntax {
	case "numbered":
		pattern = `(?i)@?(Image|Video|Audio)[ \t]*([+-]?[0-9]+(?:\.[0-9]+)?)`
	case "picture":
		pattern = `(?i)<(Picture|Video|Audio)[ \t]+([^<>]*)>`
	case "bracket":
		pattern = `(?i)\[(Image|Video|Audio)[ \t]+([^\[\]]*)\]`
	case "kling-id":
		pattern = `(?i)@(image|video|audio)_([+-]?[0-9]+(?:\.[0-9]+)?|[^\s,;!?\]\[<>(){}]+)`
	case "":
		if regexp.MustCompile(`(?i)(@?(image|video|audio)[ _]*[0-9]|<(picture|video|audio)|\[(image|video|audio))`).MatchString(prompt) {
			return nil, fmt.Errorf("%w: model has no reviewed prompt grammar", ErrInvalid)
		}
		return []Slot{}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported prompt grammar", ErrInvalid)
	}
	if len(types) == 0 {
		types = []string{"image"}
	}
	matches := regexp.MustCompile(pattern).FindAllStringSubmatchIndex(prompt, -1)
	maxIndex := map[string]int{}
	seen := map[string]bool{}
	validNumber := regexp.MustCompile(`^[1-9][0-9]*$`)
	for _, match := range matches {
		start := match[0]
		previous, _ := utf8.DecodeLastRuneInString(prompt[:start])
		if start > 0 && !strings.ContainsRune(" \t\r\n([{\"'“«:;,.!?", previous) {
			continue
		}
		if syntax == "numbered" && match[1] < len(prompt) {
			following, _ := utf8.DecodeRuneInString(prompt[match[1]:])
			if unicode.IsLetter(following) || unicode.IsDigit(following) || following == '_' {
				return nil, fmt.Errorf("%w: malformed numeric reference", ErrInvalid)
			}
		}
		raw, kind := prompt[match[4]:match[5]], strings.ToLower(prompt[match[2]:match[3]])
		if kind == "picture" {
			kind = "image"
		}
		n, err := strconv.Atoi(raw)
		if !validNumber.MatchString(raw) || err != nil || n < 1 || n > 64 || !slices.Contains(types, kind) {
			return nil, fmt.Errorf("%w: unsupported or invalid reference %s", ErrInvalid, prompt[match[0]:match[1]])
		}
		if syntax == "kling-id" && prompt[match[0]:match[1]] != "@"+kind+"_"+raw {
			return nil, fmt.Errorf("%w: frame identifiers are case sensitive", ErrInvalid)
		}
		if n > maxIndex[kind] {
			maxIndex[kind] = n
		}
		seen[kind+":"+strconv.Itoa(n)] = true
	}
	// A marker-like opening with no matching closing delimiter is malformed.
	rest := regexp.MustCompile(pattern).ReplaceAllString(prompt, "")
	if syntax == "picture" && regexp.MustCompile(`(?i)<(Picture|Video|Audio)(\s|>)`).MatchString(rest) || syntax == "bracket" && regexp.MustCompile(`(?i)\[(Image|Video|Audio)(\s|\])`).MatchString(rest) {
		return nil, fmt.Errorf("%w: malformed reference marker", ErrInvalid)
	}
	if syntax == "numbered" && regexp.MustCompile(`(?i)@(image|video|audio)(\b|_)`).MatchString(rest) {
		return nil, fmt.Errorf("%w: malformed numbered reference", ErrInvalid)
	}
	slots := []Slot{}
	for _, kind := range kinds {
		extra := 0
		if kind == "image" && mode.ID == "first_frame_references" {
			extra = 1
		}
		for index := 1; index <= maxIndex[kind]+extra; index++ {
			number := index - extra
			if number > 0 && !seen[kind+":"+strconv.Itoa(number)] {
				return nil, fmt.Errorf("%w: reference numbering has a gap", ErrInvalid)
			}
			role := "reference"
			if kind == "image" {
				role = core.ReferenceImageRole(mode, index, maxIndex[kind]+extra)
			}
			id := kind + ":" + strconv.Itoa(index)
			slots = append(slots, Slot{ID: id, Type: kind, Index: index, Role: role, Label: strings.ToUpper(kind[:1]) + kind[1:] + " " + strconv.Itoa(index)})
		}
	}
	return slots, nil
}
func Mode(model core.MediaModel, id string) (core.ReferenceMode, bool) {
	for _, m := range core.ReferenceModesFor(model) {
		if m.ID == id {
			return m, true
		}
	}
	if id == "" || id == "none" {
		return core.ReferenceMode{ID: id}, true
	}
	return core.ReferenceMode{}, false
}

// InferSlots preserves native prompt text. Models without marker syntax use
// publisher-declared ordered inputs and the selected workflow's required roles.
func (d Definition) InferSlots(model core.MediaModel) ([]Slot, error) {
	mode, err := AuthoringMode(model, d.Options.ReferenceMode)
	if err != nil {
		return nil, err
	}
	syntax := PromptSyntax(model, d.Options.ReferenceMode)
	if d.Syntax == UIReferenceSyntax {
		slots, e := inferCommon(d.Prompt, referenceTypes(model, mode), mode)
		if e != nil {
			return nil, e
		}
		if len(slots) > 0 {
			if len(d.ReferenceCounts) > 0 {
				return nil, fmt.Errorf("%w: marker inputs infer their counts from the prompt", ErrInvalid)
			}
			return slots, nil
		}
		syntax = ""
	}
	if syntax != "" {
		if len(d.ReferenceCounts) > 0 {
			return nil, fmt.Errorf("%w: marker workflows infer counts from the prompt", ErrInvalid)
		}
		slots, err := Infer(d.Prompt, syntax, model.ReferencePromptTypes, mode)
		if err != nil {
			return nil, err
		}
		if len(slots) == 0 {
			return nil, fmt.Errorf("%w: declare at least one reference slot", ErrInvalid)
		}
		return slots, nil
	}
	slots := []Slot{}
	for kind := range d.ReferenceCounts {
		if !slices.Contains(kinds, kind) {
			return nil, ErrInvalid
		}
	}
	for _, kind := range kinds {
		n, specified := d.ReferenceCounts[kind]
		minimum := mode.MinImages
		if kind == "video" {
			minimum = mode.MinVideos
		}
		if kind == "audio" {
			minimum = mode.MinAudios
		}
		if !specified {
			n = minimum
		}
		if n < 0 || n > 64 {
			return nil, ErrInvalid
		}
		for index := 1; index <= n; index++ {
			role := "reference"
			if kind == "image" {
				role = core.ReferenceImageRole(mode, index, n)
			}
			slots = append(slots, Slot{ID: kind + ":" + strconv.Itoa(index), Type: kind, Index: index, Role: role, Label: strings.ToUpper(kind[:1]) + kind[1:] + " " + strconv.Itoa(index)})
		}
	}
	// Reuse the complete native count/combined/workflow/settings validation.
	check := d
	check.Syntax, check.Slots = syntax, slots
	if d.Syntax == UIReferenceSyntax {
		check.Syntax = UIReferenceSyntax
	}
	if err := check.Compatible(model); err != nil {
		return nil, err
	}
	return slots, nil
}
func (d Definition) Validate(model core.MediaModel) error {
	if strings.TrimSpace(d.Title) == "" || len([]rune(d.Title)) > 120 || len([]rune(d.Description)) > 500 || len([]rune(d.Prompt)) > 10000 || strings.TrimSpace(d.Prompt) == "" {
		return fmt.Errorf("%w: title and prompt are required (120/10000 characters); description is limited to 500", ErrInvalid)
	}
	if d.Prompt != strings.TrimSpace(d.Prompt) {
		return fmt.Errorf("%w: remove leading/trailing prompt whitespace", ErrInvalid)
	}
	if d.MediaType != "image" && d.MediaType != "video" {
		return fmt.Errorf("%w: choose image or video output", ErrInvalid)
	}
	if len(d.Options.ImageURLs)+len(d.Options.Videos())+len(d.Options.Audios())+len(d.Options.ReferenceVoices) > 0 {
		return fmt.Errorf("%w: generation inputs belong in declared slots", ErrInvalid)
	}
	_, ok := Mode(model, d.Options.ReferenceMode)
	if !ok {
		return ErrInvalid
	}
	if d.Syntax != UIReferenceSyntax && d.Syntax != PromptSyntax(model, d.Options.ReferenceMode) {
		return ErrInvalid
	}
	slots, err := d.InferSlots(model)
	if err != nil {
		return err
	}
	if len(slots) != len(d.Slots) {
		return fmt.Errorf("%w: infer and review the required slots", ErrInvalid)
	}
	for i, slot := range slots {
		current := d.Slots[i]
		if current.ID != slot.ID || current.Type != slot.Type || current.Index != slot.Index || current.Role != slot.Role || len(current.Label) > 120 || strings.TrimSpace(current.Label) == "" {
			return fmt.Errorf("%w: duplicate, reordered or incorrectly bound slots", ErrInvalid)
		}
	}
	return d.Compatible(model)
}

// PromptSyntax follows the reviewed mode-scoped grammar used by Playground.
func PromptSyntax(model core.MediaModel, mode string) string {
	if model.ReferencePromptSyntax == "picture" && mode != "references" {
		return ""
	}
	return model.ReferencePromptSyntax
}

// ValidateGenerationOptions validates explicit author settings without requiring
// completed slots or media. Missing legacy values retain model admission defaults.
func (d Definition) ValidateGenerationOptions(model core.MediaModel) error {
	o := d.Options
	ratios := model.Ratios
	if mode, ok := Mode(model, o.ReferenceMode); ok && len(mode.Ratios) > 0 {
		ratios = mode.Ratios
	}
	invalid := func(name string) error {
		return fmt.Errorf("%w: unsupported %s for the selected model", ErrInvalid, name)
	}
	if o.Resolution != "" && !slices.Contains(model.Resolutions, o.Resolution) {
		return invalid("resolution")
	}
	if o.Ratio != "" && !slices.Contains(ratios, o.Ratio) {
		return invalid("aspect ratio")
	}
	if o.Duration != 0 && (model.MediaType != "video" || model.DurationFromInput || model.MaxDuration <= 0 || o.Duration < model.MinDuration || o.Duration > model.MaxDuration || model.DurationStep > 0 && (o.Duration-model.MinDuration)%model.DurationStep != 0) {
		return invalid("video duration")
	}
	if o.GenerateAudio != nil && (model.MediaType != "video" || !model.SupportsAudio || !*o.GenerateAudio && model.AudioAlwaysOn) {
		return invalid("generated audio")
	}
	return nil
}

// OptionsFor preserves the explicitly reviewed reference workflow and settings.
// Empty and frame-based legacy recipes are never mapped to another meaning.
func (d Definition) OptionsFor(model core.MediaModel) (core.MediaOptions, error) {
	if err := d.ValidateGenerationOptions(model); err != nil {
		return core.MediaOptions{}, err
	}
	if d.Options.OverrideSoundID != "" || d.Options.OverrideSound != nil || d.OverrideSoundID != "" && model.MediaType != "video" {
		return core.MediaOptions{}, fmt.Errorf("%w: Override Sound must be a separate audio asset on a video template", ErrInvalid)
	}
	if err := d.WorkflowError(); err != nil {
		return core.MediaOptions{}, err
	}
	if _, err := AuthoringMode(model, d.Options.ReferenceMode); err != nil {
		return core.MediaOptions{}, err
	}
	if model.MediaType != d.MediaType || len(d.Options.ImageURLs)+len(d.Options.Videos())+len(d.Options.Audios())+len(d.Options.ReferenceVoices) > 0 {
		return core.MediaOptions{}, fmt.Errorf("%w: output differs or reference inputs are undeclared", ErrInvalid)
	}
	return d.Options, nil
}

func (d Definition) Compatible(model core.MediaModel) error {
	options, err := d.OptionsFor(model)
	if err != nil {
		return err
	}
	if limits := model.ReferenceMediaLimits; limits != nil && limits.MaxFiles > 0 && len(d.Slots) > limits.MaxFiles {
		return fmt.Errorf("%w: too many reference files", ErrInvalid)
	}
	if model.MediaType != d.MediaType || model.NoPrompt || d.Syntax != UIReferenceSyntax && PromptSyntax(model, d.Options.ReferenceMode) != d.Syntax {
		return fmt.Errorf("%w: output or prompt grammar differs", ErrInvalid)
	}
	mode, ok := Mode(model, d.Options.ReferenceMode)
	if !ok {
		return fmt.Errorf("%w: reference workflow is unavailable", ErrInvalid)
	}
	opts := options
	types := model.ReferencePromptTypes
	if d.Syntax == "" || d.Syntax == UIReferenceSyntax {
		types = nil
		if model.SupportsImageInput {
			types = append(types, "image")
		}
		if model.SupportsVideoInput {
			types = append(types, "video")
		}
		if model.SupportsAudioInput {
			types = append(types, "audio")
		}
	}
	if len(types) == 0 {
		types = []string{"image"}
	}
	imageCount := 0
	for _, slot := range d.Slots {
		if slot.Type == "image" {
			imageCount++
		}
	}
	for _, slot := range d.Slots {
		if !slices.Contains(types, slot.Type) {
			return fmt.Errorf("%w: reference media type differs", ErrInvalid)
		}
		role := "reference"
		if slot.Type == "image" {
			role = core.ReferenceImageRole(mode, slot.Index, imageCount)
		}
		if role != slot.Role {
			return fmt.Errorf("%w: frame roles differ", ErrInvalid)
		}
		value := "https://template.invalid/" + slot.ID
		switch slot.Type {
		case "image":
			opts.ImageURLs = append(opts.ImageURLs, value)
		case "video":
			opts.VideoURLs = append(opts.VideoURLs, value)
		case "audio":
			opts.AudioURLs = append(opts.AudioURLs, value)
		default:
			return ErrInvalid
		}
	}
	normalized, err := core.NormalizeModelOptions(model, opts)
	if err != nil {
		return err
	}
	return core.ValidateReferenceMode(model, normalized)
}
