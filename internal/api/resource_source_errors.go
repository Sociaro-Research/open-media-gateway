package api

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

func resourceDimensionsHint(width, height int, choices []core.MediaDimensions) string {
	if len(choices) > 32 {
		return "The reference dimensions are unsupported. Resize the file to dimensions listed in the selected preparation's source schema."
	}
	values := make([]string, 0, len(choices))
	for _, v := range choices {
		values = append(values, fmt.Sprintf("%dx%d", v.Width, v.Height))
	}
	return fmt.Sprintf("Reference dimensions are %dx%d pixels; supported dimensions: %s. Resize the file.", width, height, strings.Join(values, ", "))
}

const resourceFileFallback = "The reference file does not meet this preparation's source constraints. Re-export it using the selected preparation's source schema."

// Render only a fresh diagnostic from our byte-inspected file validator with
// public labels and bounded schema tokens. Original filenames, model labels,
// unexpected codec/MIME text and arbitrary wrapped errors are never promoted.
// This is diagnostic-only: the original ValidateFiles result decides admission.
func resourceFileHint(model core.MediaModel, file referencevalidation.File) string {
	model.ID, model.DisplayName = "selected model", "selected model"
	file.Name = "Reference file"
	if file.Type != "image" && file.Type != "video" && file.Type != "audio" {
		return resourceFileFallback
	}
	if model.ReferenceMediaLimits != nil {
		model.ReferenceMediaLimits = model.ReferenceMediaLimits.Clone()
		for _, rule := range []*core.MediaFileLimits{&model.ReferenceMediaLimits.Image, &model.ReferenceMediaLimits.Video, &model.ReferenceMediaLimits.Audio} {
			for _, values := range [][]string{rule.ContentTypes, rule.Codecs, rule.AllowedAudioCodecs} {
				if !resourceSchemaTokens(values) {
					return resourceFileFallback
				}
			}
		}
		rule := model.ReferenceMediaLimits.Image
		if file.Type == "video" {
			rule = model.ReferenceMediaLimits.Video
		}
		if file.Type == "audio" {
			rule = model.ReferenceMediaLimits.Audio
		}
		if len(rule.ContentTypes) > 0 {
			accepted := false
			for _, mime := range rule.ContentTypes {
				accepted = accepted || mediafile.CanonicalType(mime) == mediafile.CanonicalType(file.Info.ContentType)
			}
			if !accepted {
				file.Info.ContentType = "unsupported"
			}
		}
		if len(rule.Codecs) > 0 && !slices.Contains(rule.Codecs, file.Info.Codec) {
			file.Info.Codec = "unsupported"
		}
	}
	// All text that this trusted validator can render has now been constrained.
	if err := referencevalidation.ValidateFiles(model, []referencevalidation.File{file}, false); err != nil {
		hint := err.Error()
		if len(hint) <= 2048 {
			return hint
		}
	}
	return resourceFileFallback
}

func resourceSchemaTokens(values []string) bool {
	if len(values) > 32 {
		return false
	}
	for _, value := range values {
		if len(value) == 0 || len(value) > 64 {
			return false
		}
		for _, r := range value {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._+-", r)) {
				return false
			}
		}
	}
	return true
}
