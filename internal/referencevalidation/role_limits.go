package referencevalidation

import (
	"fmt"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// ValidateRoleFiles applies reviewed role limits independently of global limits.
// Subsets retain explicit 1-based positions; ordered fallback is safe only when
// every input of that kind is present in the inspected file list.
func ValidateRoleFiles(model core.MediaModel, options core.MediaOptions, files []File) error {
	if len(model.ReferenceRoleLimits) == 0 || len(files) == 0 {
		return nil
	}
	counts := map[string]int{}
	relevant := map[string]bool{}
	for _, limit := range model.ReferenceRoleLimits {
		relevant[limit.Kind] = true
	}
	for _, file := range files {
		counts[file.Type]++
	}
	positions := map[string]int{}
	seen := map[string]bool{}
	grouped := map[string][]File{}
	for _, file := range files {
		ordered := positions[file.Type]
		positions[file.Type]++
		if !relevant[file.Type] {
			continue
		}
		actual := 0
		switch file.Type {
		case "image":
			actual = len(options.ImageURLs)
		case "video":
			actual = len(options.Videos())
		case "audio":
			actual = len(options.Audios())
		default:
			continue
		}
		index := file.ReferenceIndex - 1
		if file.ReferenceIndex == 0 {
			if actual != counts[file.Type] {
				return Invalid(file.Name, "The reference role could not be associated with its original input position.")
			}
			index = ordered
		}
		if index < 0 || index >= actual {
			return Invalid(file.Name, "The inspected reference position does not match an input.")
		}
		address := fmt.Sprintf("%s_%d", file.Type, index+1)
		if seen[address] {
			return Invalid(file.Name, "Multiple inspected files claim the same reference position.")
		}
		seen[address] = true
		role := core.ReferenceRole(model, options, file.Type, index)
		grouped[file.Type+"\x00"+role] = append(grouped[file.Type+"\x00"+role], file)
	}
	for _, role := range model.ReferenceRoleLimits {
		group := grouped[role.Kind+"\x00"+role.Role]
		if len(group) == 0 {
			continue
		}
		scoped := model
		scoped.ReferenceRoleLimits = nil
		scoped.ReferenceMediaLimits = &core.ReferenceMediaLimits{}
		switch role.Kind {
		case "image":
			scoped.ReferenceMediaLimits.Image = role.Limits
		case "video":
			scoped.ReferenceMediaLimits.Video = role.Limits
		case "audio":
			scoped.ReferenceMediaLimits.Audio = role.Limits
		default:
			continue
		}
		if err := ValidateFiles(scoped, group, false); err != nil {
			return err
		}
		for _, file := range group {
			rule, info := role.Limits, file.Info
			if rule.MinFrames > 0 || rule.MaxFrames > 0 {
				if info.FrameCount <= 0 {
					return Invalid(file.Name, "Video frame count could not be established for the selected reference role. Re-export an inspectable file.")
				}
				if rule.MinFrames > 0 && info.FrameCount < rule.MinFrames || rule.MaxFrames > 0 && info.FrameCount > rule.MaxFrames {
					return Invalid(file.Name, fmt.Sprintf("video frame count %d; role %s requires %s.", info.FrameCount, role.Role, frameInterval(rule.MinFrames, rule.MaxFrames)))
				}
			}
			if rule.RequireAudio != nil {
				if info.AudioStreams == nil || *info.AudioStreams < 0 {
					return Invalid(file.Name, "Audio stream presence could not be established for the selected reference role. Re-export an inspectable file.")
				}
				if *rule.RequireAudio && *info.AudioStreams == 0 {
					return Invalid(file.Name, fmt.Sprintf("role %s requires an audio stream.", role.Role))
				}
				if !*rule.RequireAudio && *info.AudioStreams != 0 {
					return Invalid(file.Name, fmt.Sprintf("role %s requires a video without an audio stream.", role.Role))
				}
			}
		}
	}
	return nil
}
func frameInterval(min, max int64) string {
	if min > 0 && max > 0 {
		return fmt.Sprintf("%d–%d frames", min, max)
	}
	if min > 0 {
		return fmt.Sprintf("at least %d frames", min)
	}
	return fmt.Sprintf("at most %d frames", max)
}
