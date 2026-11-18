package bytedance

import (
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/internal/catalogdoc"
	"maps"
	"regexp"
	"strings"
)

var videoAPISource = catalogsync.Source{ID: "video-task-api", URL: "https://ai.byteplus.com/ark/region:ap-southeast-1/docs/create-video-generation-task-api.md", Parser: "document"}

// Exact existing adapter ID -> documented model name, reviewed in GW-172 and GW-380.
// No future snapshot, model family or generation mode is enabled by this source.
var videoAPIModels = []struct{ id, label string }{{"dreamina-seedance-2-5-260628", "Dreamina Seedance 2.5"}, {"dreamina-seedance-2-5-flash-261115", "Dreamina Seedance 2.5 flash"}}

// ownValue reads one model's "Name:" entry up to the next model name.
func ownValue(section, label string) string {
	return catalogdoc.Between(section, regexp.QuoteMeta(label)+`:`, `(?:Dreamina )?Seedance [0-9]|$`)
}

// seriesValue falls back to the shared 2.5 series entry, never to another member's value.
func seriesValue(section, label string) string {
	if v := ownValue(section, label); v != "" {
		return v
	}
	return ownValue(section, "Dreamina Seedance 2.5 series")
}

func interpretVideoAPI(source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	if source.URL != videoAPISource.URL {
		return nil, nil
	}
	text = catalogdoc.Plain(text)
	if !strings.Contains(text, "contents/generations/tasks") || !catalogdoc.HasModel(text, "Dreamina Seedance 2.5") || !strings.Contains(text, "Request parameters") {
		return nil, errors.New("video task API contract not found")
	}
	shared := catalogdoc.Fields{}
	shared.Put("media_type", "video")
	ratio := catalogdoc.Between(text, `ratio\s+string`, `duration\s+integer`)
	supported := catalogdoc.Between(ratio, `Supported values:`, `Value restrictions`)
	if values := catalogdoc.Tokens(supported, `\b(?:[1-9][0-9]*:[1-9][0-9]*|adaptive)\b`); len(values) > 0 {
		shared.Put("output.ratios", values)
	}
	if strings.Contains(ratio, "First-frame or first-and-last-frame image-to-video") && strings.Contains(ratio, "only supports adaptive") {
		shared.Put("workflow.first_frame.ratios", []string{"adaptive"})
		shared.Put("workflow.first_last_frames.ratios", []string{"adaptive"})
	}
	caps := catalogdoc.Between(text, `Dreamina Seedance 2\.5(?: series)?\s*\(video with audio / silent video\)`, `Dreamina Seedance 2\.0 series\s*\(video with audio / silent video\)`)
	if strings.Contains(caps, "Text-to-video") && strings.Contains(caps, "first and last frames") && strings.Contains(caps, "Omni reference-to-video") {
		shared.Put("reference_modes", []string{"none", "first_frame", "first_last_frames", "references"})
		catalogdoc.Count(shared, "first_frame", "image", 1, 1)
		catalogdoc.Count(shared, "first_last_frames", "image", 2, 2)
	}
	if strings.Contains(text, "Text (optional) + image") && strings.Contains(text, "Text (optional) + video") {
		shared.Put("prompt_policy", map[string]bool{"no_prompt": false, "optional": true})
	}
	if (strings.Contains(text, "Image1") || strings.Contains(text, "Image 1")) && (strings.Contains(text, "Video1") || strings.Contains(text, "Video 1")) && (strings.Contains(text, "reference_audio") && strings.Contains(text, "reference_video") && strings.Contains(text, "reference_image")) {
		shared.Put("reference_prompt", map[string]any{"syntax": "numbered", "types": []string{"image", "video", "audio"}})
	}
	image := catalogdoc.Between(text, `Requirements for uploading a single image`, `role\s+string`)
	catalogdoc.Rule(shared, []string{"references", "first_frame", "first_last_frames"}, "image", image)
	video := catalogdoc.Between(text, `Video input requirements`, `role\s+string`)
	catalogdoc.Rule(shared, []string{"references"}, "video", video)
	audio := catalogdoc.Between(text, `Audio input requirements`, `role\s+string`)
	catalogdoc.Rule(shared, []string{"references"}, "audio", audio)

	resolution := catalogdoc.Between(text, `resolution\s+string`, `ratio\s+string`)
	duration := catalogdoc.Between(text, `duration\s+integer`, `frames\s+integer`)
	var out []catalogsync.Entry
	for _, m := range videoAPIModels {
		if !catalogdoc.HasModel(text, m.label) {
			continue
		}
		fields, own := maps.Clone(shared), false
		if values := catalogdoc.Tokens(ownValue(resolution, m.label), `\b(?:480p|720p|1080p|4k)\b`); len(values) > 0 {
			fields.Put("output.resolutions", values)
			own = true
		}
		spec := seriesValue(duration, m.label)
		if min, max, ok := catalogdoc.Range(spec, `supports\s*\[([0-9]+),\s*([0-9]+)\]`); ok {
			if def, found := catalogdoc.Int(spec, `^\s*Default\s*(-?[0-9]+)`); found {
				catalogdoc.Duration(fields, min, max, def)
			}
		}
		limits := ownValue(caps, m.label)
		if limits == "" && m.id == "dreamina-seedance-2-5-260628" {
			// The October layout named only Dreamina Seedance 2.5 in this heading.
			limits = catalogdoc.Between(text, `Dreamina Seedance 2\.5\s*\(video with audio / silent video\)`, `Dreamina Seedance 2\.0 series\s*\(video with audio / silent video\)`)
		}
		counts := map[string]int{}
		for _, item := range []struct{ kind, label string }{{"image", "images"}, {"video", "videos"}, {"audio", "audio clips"}} {
			min, max, ok := catalogdoc.Range(limits, `([0-9]+)\s*[-–]\s*([0-9]+)\s+reference\s+`+item.label)
			if !ok {
				min, max, ok = catalogdoc.Range(limits, `reference\s+`+item.label+`\s*\(\s*([0-9]+)\s*[-–]\s*([0-9]+)\s*\)`)
			}
			if ok {
				catalogdoc.Count(fields, "references", item.kind, min, max)
				counts[item.kind] = max
				own = true
			}
		}
		if len(counts) == 3 {
			fields.Put("reference_media_limits.max_files", counts["image"]+counts["video"]+counts["audio"])
		}
		for _, item := range []struct{ kind, section, unit string }{{"video", video, "video"}, {"audio", audio, "audio clip"}} {
			spec := seriesValue(catalogdoc.Between(item.section, `Duration:`, `Quantity limit`), m.label)
			if min, max, ok := catalogdoc.Range(spec, `Each `+item.unit+` must be\s*([0-9]+)\s*[-–]\s*([0-9]+)\s*seconds`); ok {
				if total, found := catalogdoc.Int(spec, `total duration of[^.]{0,30}?(?:no more than|not exceed)\s*([0-9]+)`); found {
					fields.Put("workflow.references."+item.kind+".duration", map[string]int{"min": min, "max": max, "min_total": 0, "max_total": total})
				}
			}
		}
		// Shared series text alone never verifies a model without its own entries.
		if own {
			out = append(out, catalogsync.Entry{ID: m.id, OperationalFields: fields})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("video task fields not recognized")
	}
	return out, nil
}
