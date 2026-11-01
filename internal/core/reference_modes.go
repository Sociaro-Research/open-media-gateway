package core

import (
	"fmt"
)

// ReferenceMode is shared by API validation and the console. Array order assigns frame roles.
type ReferenceMode struct {
	Ratios               []string `json:"ratios,omitempty"`
	DefaultImageRole     string   `json:"default_image_role,omitempty"`
	LastImageRole        string   `json:"last_image_role,omitempty"`
	MinVisuals           int      `json:"min_visuals,omitempty"`
	MaxDurationWithVideo int      `json:"max_duration_with_video,omitempty"`
	VoiceImageStart      int      `json:"voice_image_start,omitempty"`

	SupportsReferenceVoice bool     `json:"supports_reference_voice,omitempty"`
	ID                     string   `json:"id"`
	Label                  string   `json:"label"`
	MinImages              int      `json:"min_images"`
	MaxImages              int      `json:"max_images"`
	MinVideos              int      `json:"min_videos"`
	MaxVideos              int      `json:"max_videos"`
	MinAudios              int      `json:"min_audios"`
	MaxAudios              int      `json:"max_audios"`
	MaxVisuals             int      `json:"max_visuals,omitempty"`
	ImageRoles             []string `json:"image_roles,omitempty"`
	RequiresVisual         bool     `json:"requires_visual,omitempty"`
}

// ReferenceImageRole resolves both fixed keyframes and ordered first/middle/last
// sequences without depending on provider or model names.
func ReferenceImageRole(mode ReferenceMode, index, count int) string {
	if index <= len(mode.ImageRoles) && index > 0 {
		return mode.ImageRoles[index-1]
	}
	if index == count && mode.LastImageRole != "" {
		return mode.LastImageRole
	}
	if mode.DefaultImageRole != "" {
		return mode.DefaultImageRole
	}
	return "reference"
}

func (o MediaOptions) Videos() []string {
	if len(o.VideoURLs) > 0 {
		return o.VideoURLs
	}
	if o.VideoURL != "" {
		return []string{o.VideoURL}
	}
	return nil
}
func (o MediaOptions) Audios() []string {
	if len(o.AudioURLs) > 0 {
		return o.AudioURLs
	}
	if o.AudioURL != "" {
		return []string{o.AudioURL}
	}
	return nil
}

func ReferenceModesFor(m MediaModel) []ReferenceMode {
	if m.ReferenceModes != nil {
		return m.ReferenceModes
	}
	if !m.SupportsImageInput && !m.SupportsVideoInput && !m.SupportsAudioInput {
		return nil
	}
	base := ReferenceMode{ID: "references", Label: "References", MinImages: m.MinImageInputs, MaxImages: m.MaxImageInputs}
	if m.RequiresImageInput && base.MinImages == 0 {
		base.MinImages = 1
	}
	if m.SupportsImageInput && base.MaxImages == 0 {
		base.MaxImages = 14
	}
	if m.SupportsVideoInput {
		base.MaxVideos = 1
	}
	if m.RequiresVideoInput {
		base.MinVideos = 1
	}
	if m.SupportsAudioInput {
		base.MaxAudios = 1
	}
	if m.RequiresAudioInput {
		base.MinAudios = 1
	}
	if m.MediaType != "video" {
		return []ReferenceMode{base}
	}
	return []ReferenceMode{base}
}

func referenceFits(p ReferenceMode, counts MediaInputCounts) bool {
	i, v, a := counts.Images, counts.Videos, counts.Audios
	return i >= p.MinImages && i <= p.MaxImages && v >= p.MinVideos && v <= p.MaxVideos && a >= p.MinAudios && a <= p.MaxAudios && (p.MaxVisuals == 0 || i+v <= p.MaxVisuals) && i+v >= p.MinVisuals && (!p.RequiresVisual || i+v > 0 || a == 0)
}
func ValidateReferenceMode(m MediaModel, o MediaOptions) error {
	counts, err := CountMediaInputs(m, o)
	if err != nil {
		return err
	}
	modes := ReferenceModesFor(m)
	if len(modes) == 0 {
		if len(o.ReferenceVoices) > 0 || o.ReferenceMode != "" && o.ReferenceMode != "none" {
			return fmt.Errorf("%w: model has no reference modes", ErrOptionsInvalid)
		}
		return nil
	}
	for _, p := range modes {
		if o.ReferenceMode != "" && p.ID != o.ReferenceMode {
			continue
		}
		if !referenceFits(p, counts) {
			if o.ReferenceMode == "" {
				continue
			}
			return fmt.Errorf("%w: references do not match %s", ErrOptionsInvalid, p.Label)
		}
		if counts.Videos > 0 && p.MaxDurationWithVideo > 0 && o.Duration > p.MaxDurationWithVideo {
			return fmt.Errorf("%w: video references limit output to %d seconds", ErrOptionsInvalid, p.MaxDurationWithVideo)
		}
		if len(o.ReferenceVoices) > 0 {
			if !p.SupportsReferenceVoice {
				return fmt.Errorf("%w: this mode does not support per-reference voices", ErrOptionsInvalid)
			}
			visual := map[string]bool{}
			for i, u := range o.ImageURLs {
				if i >= p.VoiceImageStart {
					visual[u] = true
				}
			}
			for _, u := range o.Videos() {
				visual[u] = true
			}
			for u, voice := range o.ReferenceVoices {
				if !visual[u] || voice == "" {
					return fmt.Errorf("%w: voice must belong to an ordinary image or video reference", ErrOptionsInvalid)
				}
				if err := validateOptionalURL(voice); err != nil {
					return ErrOptionsInvalid
				}
			}
		}
		return nil
	}
	return fmt.Errorf("%w: unsupported reference mode or reference counts", ErrOptionsInvalid)
}
