// Package referencevalidation checks byte-inspected inputs against authorized
// model contracts. Missing provider limits add no universal restrictions.
package referencevalidation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
)

var ErrInvalid = errors.New("invalid reference")

type Target struct {
	Provider     string            `json:"provider"`
	Model        string            `json:"model"`
	Options      core.MediaOptions `json:"options"`
	ReferenceIDs []string          `json:"reference_ids,omitempty"`
	Role         string            `json:"role,omitempty"`
}

// Per-visual voices are a native workflow capability, not free Audio N slots.
func ValidateVoiceFiles(model core.MediaModel, options core.MediaOptions, files []File) error {
	allowed := false
	for _, mode := range core.ReferenceModesFor(model) {
		if mode.ID == options.ReferenceMode && mode.SupportsReferenceVoice {
			allowed = true
		}
	}
	if !allowed {
		return Invalid("Voice reference", "The selected workflow does not support per-reference voices.")
	}
	for _, file := range files {
		if file.Type != "audio" {
			return Invalid(file.Name, "A voice reference must be an audio file.")
		}
	}
	copy := model
	copy.ReferenceMediaLimits = nil
	if model.ReferenceMediaLimits != nil && model.ReferenceMediaLimits.ReferenceVoice != nil {
		copy.ReferenceMediaLimits = &core.ReferenceMediaLimits{Audio: *model.ReferenceMediaLimits.ReferenceVoice}
	}
	return ValidateFiles(copy, files, false)
}

type File struct {
	ReferenceIndex int
	SHA256         string
	Type           string
	Name           string
	Info           mediafile.Info
}
type Validator func(context.Context, *Target, []File, bool) error

func Invalid(name, message string) error {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if name == "" {
		name = "Reference file"
	}
	if len(name) > 255 {
		name = name[:255]
	}
	return fmt.Errorf("%w: %s: %s", ErrInvalid, name, message)
}
func number(v float64) string { return strconv.FormatFloat(v, 'g', 12, 64) }
func modelName(m core.MediaModel) string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	if m.ID != "" {
		return m.ID
	}
	return "this model"
}
func outside(v, min, max float64) bool { return min > 0 && v < min || max > 0 && v > max }
func interval(min, max float64, unit string) string {
	if min > 0 && max > 0 {
		return number(min) + "–" + number(max) + " " + unit
	}
	if min > 0 {
		return "at least " + number(min) + " " + unit
	}
	return "at most " + number(max) + " " + unit
}

// complete enables aggregate minima. Known files always obey maxima and
// individual bounds, even while other inputs have yet to be supplied.
func ValidateFiles(model core.MediaModel, files []File, complete bool) error {
	limits := model.ReferenceMediaLimits
	for _, file := range files {
		if file.Type == "video" && file.Info.Size > ReviewedVideoMaxBytes(model) {
			return Invalid(file.Name, fmt.Sprintf("size %d bytes; %s accepts video at most %d bytes. Reduce the file size.", file.Info.Size, modelName(model), ReviewedVideoMaxBytes(model)))
		}
	}
	if limits == nil || len(files) == 0 {
		return nil
	}
	if limits.MaxFiles > 0 && len(files) > limits.MaxFiles {
		return Invalid("References", fmt.Sprintf("%d files; %s accepts at most %d. Remove extra files.", len(files), modelName(model), limits.MaxFiles))
	}
	for _, kind := range []string{"image", "video", "audio"} {
		rule := limits.Image
		if kind == "video" {
			rule = limits.Video
		}
		if kind == "audio" {
			rule = limits.Audio
		}
		var totalBytes int64
		var totalDuration float64
		count := 0
		for _, file := range files {
			if file.Type != kind {
				continue
			}
			count++
			info := file.Info
			totalBytes += info.Size
			totalDuration += info.Duration
			name := file.Name
			if name == "" {
				name = fmt.Sprintf("%s %d", strings.ToUpper(kind[:1])+kind[1:], count)
			}
			bad := func(s string) error { return Invalid(name, s) }
			accepted := len(rule.ContentTypes) == 0
			for _, t := range rule.ContentTypes {
				accepted = accepted || mediafile.CanonicalType(t) == mediafile.CanonicalType(info.ContentType)
			}
			if !accepted {
				return bad(fmt.Sprintf("format %s; %s accepts %s. Export in a supported format.", info.ContentType, modelName(model), strings.Join(rule.ContentTypes, ", ")))
			}
			if rule.MaxBytes > 0 && (info.Size > rule.MaxBytes || info.Size == rule.MaxBytes && !rule.MaxBytesInclusive) {
				bound := "less than"
				if rule.MaxBytesInclusive {
					bound = "at most"
				}
				return bad(fmt.Sprintf("size %d bytes; %s requires %s %d bytes. Reduce the file size.", info.Size, modelName(model), bound, rule.MaxBytes))
			}
			if len(rule.Codecs) > 0 && !slices.Contains(rule.Codecs, info.Codec) {
				return bad(fmt.Sprintf("codec %q; %s accepts %s. Re-export with a supported codec.", info.Codec, modelName(model), strings.Join(rule.Codecs, ", ")))
			}
			if !core.AudioCodecsAllowed(info.AudioStreams, info.AudioCodecs, rule.AllowedAudioCodecs) {
				return bad(fmt.Sprintf("embedded audio codecs are unknown or unsupported; %s accepts %s. Re-export with supported audio or remove the audio track.", modelName(model), strings.Join(rule.AllowedAudioCodecs, ", ")))
			}
			if rule.NoAlpha && info.AlphaChannel {
				return bad(modelName(model) + " does not accept transparency. Export an opaque image.")
			}
			if outside(float64(info.Width), float64(rule.MinDimension), float64(rule.MaxDimension)) || outside(float64(info.Height), float64(rule.MinDimension), float64(rule.MaxDimension)) {
				return bad(fmt.Sprintf("dimensions %d×%d pixels; %s requires each side to be %s. Resize the file.", info.Width, info.Height, modelName(model), interval(float64(rule.MinDimension), float64(rule.MaxDimension), "pixels")))
			}
			pixels := float64(info.Width) * float64(info.Height)
			if outside(pixels, float64(rule.MinPixels), float64(rule.MaxPixels)) {
				return bad(fmt.Sprintf("dimensions %d×%d (%s pixels); %s requires %s in total. Resize the file.", info.Width, info.Height, number(pixels), modelName(model), interval(float64(rule.MinPixels), float64(rule.MaxPixels), "pixels")))
			}
			ratio := 0.0
			if info.Height > 0 {
				ratio = float64(info.Width) / float64(info.Height)
			}
			if outside(ratio, rule.MinRatio, rule.MaxRatio) {
				return bad(fmt.Sprintf("width/height ratio %s; %s requires %s. Crop or resize the file.", number(ratio), modelName(model), interval(rule.MinRatio, rule.MaxRatio, "width/height")))
			}
			if outside(info.Duration, rule.MinDuration, rule.MaxDuration) {
				return bad(fmt.Sprintf("duration %s seconds; %s accepts %s. Trim or re-export the file.", number(info.Duration), modelName(model), interval(rule.MinDuration, rule.MaxDuration, "seconds")))
			}
			if outside(info.FPS, rule.MinFPS, rule.MaxFPS) {
				return bad(fmt.Sprintf("average frame rate %s FPS; %s accepts %s. Re-export at a supported constant frame rate.", number(info.FPS), modelName(model), interval(rule.MinFPS, rule.MaxFPS, "FPS")))
			}
		}
		if count == 0 {
			continue
		}
		name := strings.ToUpper(kind[:1]) + kind[1:] + " references"
		if rule.MaxTotalBytes > 0 && totalBytes >= rule.MaxTotalBytes {
			return Invalid(name, fmt.Sprintf("combined size %d bytes; %s requires less than %d bytes. Remove or reduce files.", totalBytes, modelName(model), rule.MaxTotalBytes))
		}
		min := 0.0
		if complete {
			min = rule.MinTotalDuration
		}
		if outside(totalDuration, min, rule.MaxTotalDuration) {
			return Invalid(name, fmt.Sprintf("combined duration %s seconds; %s accepts %s. Adjust reference files.", number(totalDuration), modelName(model), interval(min, rule.MaxTotalDuration, "seconds")))
		}
	}
	return nil
}

func ValidateRequestFiles(model core.MediaModel, options core.MediaOptions, files []File, complete bool) error {
	if err := ValidateRoleFiles(model, options, files); err != nil {
		return err
	}
	if err := ValidateFiles(model, files, complete); err != nil {
		return err
	}
	if model.ReferenceMediaLimits == nil || model.ReferenceMediaLimits.MaxVideoAndOutputDuration <= 0 {
		return nil
	}
	duration := options.Duration
	if duration == 0 {
		duration = model.DefaultDuration
	}
	total := 0.0
	hasVideo := false
	for _, file := range files {
		if file.Type == "video" {
			hasVideo = true
			total += file.Info.Duration
		}
	}
	max := model.ReferenceMediaLimits.MaxVideoAndOutputDuration
	if hasVideo && total+float64(duration) > max {
		return Invalid("Video references", fmt.Sprintf("input video duration %s plus output duration %d = %s seconds; %s accepts at most %s seconds combined. Shorten input or output.", number(total), duration, number(total+float64(duration)), modelName(model), number(max)))
	}
	return nil
}

// ValidateMode permits missing inputs while editing, but enforces capability,
// workflow maxima and video-dependent output constraints immediately.
func ValidateMode(model core.MediaModel, options core.MediaOptions, files []File, complete bool) error {
	if len(files) == 0 && options.ReferenceMode == "" {
		return nil
	}
	counts := map[string]int{}
	for _, f := range files {
		counts[f.Type]++
	}
	for _, kind := range []string{"image", "video", "audio"} {
		supported := model.SupportsImageInput
		if kind == "video" {
			supported = model.SupportsVideoInput
		}
		if kind == "audio" {
			supported = model.SupportsAudioInput
		}
		if counts[kind] > 0 && !supported {
			return Invalid("References", modelName(model)+" does not accept "+kind+" input. Choose another file type or model.")
		}
	}
	for _, mode := range core.ReferenceModesFor(model) {
		if options.ReferenceMode != "" && mode.ID != options.ReferenceMode {
			continue
		}
		if options.ReferenceMode == "" && (counts["image"] > mode.MaxImages || counts["video"] > mode.MaxVideos || counts["audio"] > mode.MaxAudios || mode.MaxVisuals > 0 && counts["image"]+counts["video"] > mode.MaxVisuals) {
			continue
		}
		for _, v := range []struct {
			kind     string
			min, max int
		}{{"image", mode.MinImages, mode.MaxImages}, {"video", mode.MinVideos, mode.MaxVideos}, {"audio", mode.MinAudios, mode.MaxAudios}} {
			if counts[v.kind] > v.max || complete && counts[v.kind] < v.min {
				return Invalid("References", fmt.Sprintf("%d %s inputs; workflow %s accepts %d–%d. Adjust inputs or workflow.", counts[v.kind], v.kind, mode.Label, v.min, v.max))
			}
		}
		if mode.MaxVisuals > 0 && counts["image"]+counts["video"] > mode.MaxVisuals {
			return Invalid("References", fmt.Sprintf("%d visual inputs; workflow %s accepts at most %d.", counts["image"]+counts["video"], mode.Label, mode.MaxVisuals))
		}
		duration := options.Duration
		if duration == 0 {
			duration = model.DefaultDuration
		}
		if counts["video"] > 0 && mode.MaxDurationWithVideo > 0 && duration > mode.MaxDurationWithVideo {
			return Invalid("Video references", fmt.Sprintf("output duration %d seconds; workflow %s permits at most %d seconds with video input. Reduce output duration.", duration, mode.Label, mode.MaxDurationWithVideo))
		}
		return nil
	}
	return Invalid("References", fmt.Sprintf("%d image, %d video and %d audio inputs; no available %s workflow accepts these counts. Reduce inputs or choose a matching workflow.", counts["image"], counts["video"], counts["audio"], modelName(model)))
}
