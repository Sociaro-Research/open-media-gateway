package bytedance

import (
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/internal/catalogdoc"
	"strings"
)

var videoAPISource = catalogsync.Source{ID: "video-task-api", URL: "https://ai.byteplus.com/ark/region:ap-southeast-1/docs/create-video-generation-task-api.md", Parser: "document"}

func interpretVideoAPI(source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	if source.URL != videoAPISource.URL {
		return nil, nil
	}
	text = catalogdoc.Plain(text)
	if !strings.Contains(text, "contents/generations/tasks") || !catalogdoc.HasModel(text, "Dreamina Seedance 2.5") || !strings.Contains(text, "Request parameters") {
		return nil, errors.New("video task API contract not found")
	}
	fields := catalogdoc.Fields{}
	fields.Put("media_type", "video")
	resolution := catalogdoc.Between(text, `resolution\s+string`, `ratio\s+string`)
	modelRes := catalogdoc.Between(resolution, `Dreamina Seedance 2\.5:`, `Dreamina Seedance 2\.0:`)
	if values := catalogdoc.Tokens(modelRes, `\b(?:480p|720p|1080p|4k)\b`); len(values) > 0 {
		fields.Put("output.resolutions", values)
	}
	ratio := catalogdoc.Between(text, `ratio\s+string`, `duration\s+integer`)
	supported := catalogdoc.Between(ratio, `Supported values:`, `Value restrictions`)
	if values := catalogdoc.Tokens(supported, `\b(?:[1-9][0-9]*:[1-9][0-9]*|adaptive)\b`); len(values) > 0 {
		fields.Put("output.ratios", values)
	}
	if strings.Contains(ratio, "First-frame or first-and-last-frame image-to-video") && strings.Contains(ratio, "only supports adaptive") {
		fields.Put("workflow.first_frame.ratios", []string{"adaptive"})
		fields.Put("workflow.first_last_frames.ratios", []string{"adaptive"})
	}
	duration := catalogdoc.Between(text, `duration\s+integer`, `frames\s+integer`)
	spec := catalogdoc.Between(duration, `Dreamina Seedance 2\.5:\s*Default`, `Dreamina Seedance 2\.0 series:`)
	if min, max, ok := catalogdoc.Range(spec, `supports\s*\[([0-9]+),\s*([0-9]+)\]`); ok {
		if def, found := catalogdoc.Int(spec, `^\s*(-?[0-9]+)`); found {
			catalogdoc.Duration(fields, min, max, def)
		}
	}
	caps := catalogdoc.Between(text, `Dreamina Seedance 2\.5\s*\(video with audio / silent video\)`, `Dreamina Seedance 2\.0 series\s*\(video with audio / silent video\)`)
	counts := map[string]int{}
	for _, item := range []struct{ kind, label string }{{"image", "images"}, {"video", "videos"}, {"audio", "audio clips"}} {
		if min, max, ok := catalogdoc.Range(caps, `reference\s+`+item.label+`\s*\(\s*([0-9]+)\s*[-–]\s*([0-9]+)\s*\)`); ok {
			catalogdoc.Count(fields, "references", item.kind, min, max)
			counts[item.kind] = max
		}
	}
	if strings.Contains(caps, "Text-to-video") && strings.Contains(caps, "first and last frames") && strings.Contains(caps, "Omni reference-to-video") {
		fields.Put("reference_modes", []string{"none", "first_frame", "first_last_frames", "references"})
		catalogdoc.Count(fields, "first_frame", "image", 1, 1)
		catalogdoc.Count(fields, "first_last_frames", "image", 2, 2)
	}
	if strings.Contains(text, "Text (optional) + image") && strings.Contains(text, "Text (optional) + video") {
		fields.Put("prompt_policy", map[string]bool{"no_prompt": false, "optional": true})
	}
	if (strings.Contains(text, "Image1") || strings.Contains(text, "Image 1")) && (strings.Contains(text, "Video1") || strings.Contains(text, "Video 1")) && (strings.Contains(text, "reference_audio") && strings.Contains(text, "reference_video") && strings.Contains(text, "reference_image")) {
		fields.Put("reference_prompt", map[string]any{"syntax": "numbered", "types": []string{"image", "video", "audio"}})
	}
	if len(counts) == 3 {
		fields.Put("reference_media_limits.max_files", counts["image"]+counts["video"]+counts["audio"])
	}
	image := catalogdoc.Between(text, `Requirements for uploading a single image`, `role\s+string`)
	catalogdoc.Rule(fields, []string{"references", "first_frame", "first_last_frames"}, "image", image)
	video := catalogdoc.Between(text, `Video input requirements`, `role\s+string`)
	catalogdoc.Rule(fields, []string{"references"}, "video", video)
	videoDuration := catalogdoc.Between(video, `Dreamina Seedance 2\.5:`, `Dreamina Seedance 2\.0 series:`)
	if min, max, ok := catalogdoc.Range(videoDuration, `Each video must be\s*([0-9]+)\s*[-–]\s*([0-9]+)\s*seconds`); ok {
		if total, found := catalogdoc.Int(videoDuration, `total duration of no more than\s*([0-9]+)`); found {
			fields.Put("workflow.references.video.duration", map[string]int{"min": min, "max": max, "min_total": 0, "max_total": total})
		}
	}
	audio := catalogdoc.Between(text, `Audio input requirements`, `role\s+string`)
	catalogdoc.Rule(fields, []string{"references"}, "audio", audio)
	audioDuration := catalogdoc.Between(audio, `Dreamina Seedance 2\.5:`, `Dreamina Seedance 2\.0 series:`)
	if min, max, ok := catalogdoc.Range(audioDuration, `Each audio clip must be\s*([0-9]+)\s*[-–]\s*([0-9]+)\s*seconds`); ok {
		if total, found := catalogdoc.Int(audioDuration, `total duration of no more than\s*([0-9]+)`); found {
			fields.Put("workflow.references.audio.duration", map[string]int{"min": min, "max": max, "min_total": 0, "max_total": total})
		}
	}
	if len(fields) < 3 {
		return nil, errors.New("video task fields not recognized")
	}
	// Exact existing adapter ID -> documented family mapping, reviewed in GW-172.
	// No future snapshot, model family or generation mode is enabled by this source.
	return []catalogsync.Entry{{ID: "dreamina-seedance-2-5-260628", OperationalFields: fields}}, nil
}
