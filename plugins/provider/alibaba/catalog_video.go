package alibaba

import (
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/internal/catalogdoc"
	"strings"
)

var videoAPISources = []catalogsync.Source{
	{ID: "wan3-api", URL: "https://help.aliyun.com/en/model-studio/wan3-video-generation-api-reference", Parser: "document"},
	{ID: "wan27-r2v-api", URL: "https://help.aliyun.com/en/model-studio/wan-video-to-video-api-reference", Parser: "document"},
	{ID: "happyhorse-r2v-api", URL: "https://help.aliyun.com/en/model-studio/happyhorse-reference-to-video-api-reference", Parser: "document"},
}

func interpretVideoAPI(source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	kind := ""
	for _, s := range videoAPISources {
		if source.URL == s.URL {
			kind = s.ID
		}
	}
	if kind == "" {
		return nil, nil
	}
	text = catalogdoc.Plain(text)
	if !strings.Contains(text, "video-synthesis") || !strings.Contains(text, "Request body") {
		return nil, errors.New("video API contract not found")
	}
	fields := catalogdoc.Fields{}
	fields.Put("media_type", "video")
	catalogdoc.Selectors(fields, text)
	duration := catalogdoc.Between(text, `duration\s+integer`, `(?:watermark|audio|prompt_extend)\s+boolean`)
	def, hasDefault := catalogdoc.Int(duration, `default value(?: is|:)?\s*([0-9]+)`)
	var ids []string
	switch kind {
	case "happyhorse-r2v-api":
		if !catalogdoc.HasModel(text, "happyhorse-1.1-r2v") {
			return nil, errors.New("exact model not found")
		}
		ids = []string{"happyhorse-1.1-r2v"}
		if len(catalogdoc.First(text, `prompt\s+string\s*\(Required\)`)) > 0 {
			fields.Put("prompt_policy", map[string]bool{"no_prompt": false, "optional": false})
		}
		fields.Put("reference_modes", []string{"references"})
		if min, max, ok := catalogdoc.Range(duration, `integer from\s*([0-9]+)\s*to\s*([0-9]+)`); ok && hasDefault {
			catalogdoc.Duration(fields, min, max, def)
		}
		if min, max, ok := catalogdoc.Range(text, `number of reference images:\s*([0-9]+)\s*to\s*([0-9]+)`); ok {
			catalogdoc.Count(fields, "references", "image", min, max)
			fields.Put("reference_media_limits.max_files", max)
		}
		if strings.Contains(text, "[Image 1]") {
			fields.Put("reference_prompt", map[string]any{"syntax": "bracket", "types": []string{"image"}})
		}
	case "wan3-api":
		// Keep an explicit unknown marker when this required relation disappears;
		// other recognizable parameters must not make the whole contract look known.
		fields.Put("workflow.references.video.input_output_duration", nil)
		if maximum, ok := catalogdoc.Int(duration, `with video input:\s*the total input video duration \+ output video duration must not exceed\s*([0-9]+)\s*seconds`); ok && maximum > 0 {
			fields.Put("workflow.references.video.input_output_duration", catalogsync.VideoDurationSumLimit{MaxSeconds: float64(maximum)})
		}

		for _, id := range []string{"wan3.0-video", "wan3.0-video-prime"} {
			if catalogdoc.HasModel(text, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return nil, errors.New("exact models not found")
		}
		if strings.Contains(text, "Either prompt or media must be provided") {
			fields.Put("prompt_policy", map[string]bool{"no_prompt": false, "optional": true})
		}
		if min, max, ok := catalogdoc.Range(duration, `range\s*\[([0-9]+),\s*([0-9]+)\]`); ok && hasDefault {
			catalogdoc.Duration(fields, min, max, def)
		}
		if strings.Contains(text, "first_frame") && strings.Contains(text, "last_frame") && strings.Contains(text, "reference_image") {
			fields.Put("reference_modes", []string{"none", "first_frame", "first_last_frames", "references"})
			catalogdoc.Count(fields, "first_frame", "image", 1, 1)
			catalogdoc.Count(fields, "first_last_frames", "image", 2, 2)
		}
		for _, item := range []struct{ kind, token string }{{"image", "reference_image"}, {"video", "reference_video"}, {"audio", "reference_audio"}} {
			if max, ok := catalogdoc.Int(text, item.token+`\s*:[^:]{0,100}?maximum\s*([0-9]+)`); ok {
				catalogdoc.Count(fields, "references", item.kind, 0, max)
			}
		}
		if strings.Contains(text, `"Image 1"`) && strings.Contains(text, `"Video 1"`) {
			fields.Put("reference_prompt", map[string]any{"syntax": "numbered", "types": []string{"image", "video", "audio"}})
		}
	case "wan27-r2v-api":
		for _, id := range []string{"wan2.7-r2v", "wan2.7-r2v-2026-06-12"} {
			if catalogdoc.HasModel(text, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return nil, errors.New("exact models not found")
		}
		if len(catalogdoc.First(text, `prompt\s+string\s*\(Required\)`)) > 0 {
			fields.Put("prompt_policy", map[string]bool{"no_prompt": false, "optional": false})
		}
		if min, max, ok := catalogdoc.Range(duration, `does not include a video[^.]{0,100}?integer from\s*([0-9]+)\s*to\s*([0-9]+)`); ok && hasDefault {
			catalogdoc.Duration(fields, min, max, def)
		}
		if strings.Contains(text, "first_frame") && strings.Contains(text, "reference_video") {
			fields.Put("reference_modes", []string{"references", "first_frame_references"})
		}
		if max, ok := catalogdoc.Int(text, `Reference images\s*\+\s*Reference videos\s*≤\s*([0-9]+)`); ok {
			catalogdoc.Count(fields, "references", "image", 0, max)
			catalogdoc.Count(fields, "references", "video", 0, max)
			fields.Put("workflow.references.visual_count", map[string]int{"min": 1, "max": max})
			if strings.Contains(text, "maximum of 1 first frame") {
				catalogdoc.Count(fields, "first_frame_references", "image", 1, max+1)
				catalogdoc.Count(fields, "first_frame_references", "video", 0, max)
				fields.Put("reference_media_limits.max_files", max+1)
				fields.Put("workflow.first_frame_references.visual_count", map[string]int{"min": 2, "max": max + 1})
			}
		}
		if strings.Contains(text, "Image 1") && strings.Contains(text, "Video 1") {
			fields.Put("reference_prompt", map[string]any{"syntax": "numbered", "types": []string{"image", "video"}})
		}
	}
	modes := []string{"references"}
	if kind == "wan3-api" {
		modes = append(modes, "first_frame", "first_last_frames")
	}
	if kind == "wan27-r2v-api" {
		modes = append(modes, "first_frame_references")
	}
	image := catalogdoc.Between(text, `(?:Image requirements:|Image limits:|Input image \(type[^)]*\))`, `(?:Supported input formats:|Input video|Pass a reference video)`)
	catalogdoc.Rule(fields, modes, "image", image)
	video := catalogdoc.Between(text, `(?:Input video \(type[^)]*\)|Pass a reference video)`, `(?:Input audio|reference_voice)`)
	catalogdoc.Rule(fields, []string{"references", "first_frame_references"}, "video", video)
	if min, max, ok := catalogdoc.Range(video, `duration:\s*\[?([0-9]+)\s*(?:to|[-–]|,)\s*([0-9]+)\]?\s*s`); ok {
		total := 0
		if kind == "wan3-api" {
			total, _ = catalogdoc.Int(text, `reference_video\s*:[^:]{0,160}?total duration of no more than\s*([0-9]+)`)
		}
		for _, mode := range []string{"references", "first_frame_references"} {
			fields.Put("workflow."+mode+".video.duration", map[string]any{"min": min, "max": max, "min_total": 0, "max_total": total})
		}
	}
	if kind == "wan3-api" {
		audio := catalogdoc.Between(text, `Input audio \(type[^)]*\)`, `Input file`)
		catalogdoc.Rule(fields, []string{"references"}, "audio", audio)
		if min, max, ok := catalogdoc.Range(audio, `duration:\s*\[?([0-9]+)\s*(?:to|[-–]|,)\s*([0-9]+)\]?\s*s`); ok {
			total, _ := catalogdoc.Int(text, `reference_audio\s*:[^:]{0,160}?total duration of no more than\s*([0-9]+)`)
			fields.Put("workflow.references.audio.duration", map[string]any{"min": min, "max": max, "min_total": 0, "max_total": total})
		}
		i, iok := catalogdoc.Int(text, `reference_image\s*:[^:]{0,100}?maximum\s*([0-9]+)`)
		v, vok := catalogdoc.Int(text, `reference_video\s*:[^:]{0,100}?maximum\s*([0-9]+)`)
		a, aok := catalogdoc.Int(text, `reference_audio\s*:[^:]{0,100}?maximum\s*([0-9]+)`)
		if iok && vok && aok {
			fields.Put("reference_media_limits.max_files", i+v+a)
		}
	}
	if kind == "wan27-r2v-api" {
		if _, max, ok := catalogdoc.Range(duration, `includes a video[^.]{0,100}?integer from\s*([0-9]+)\s*to\s*([0-9]+)`); ok {
			for _, mode := range []string{"references", "first_frame_references"} {
				fields.Put("workflow."+mode+".max_duration_with_video", max)
			}
		}
	}
	if len(fields) < 3 {
		return nil, errors.New("operational fields not recognized")
	}
	out := []catalogsync.Entry{}
	for _, id := range ids {
		out = append(out, catalogsync.Entry{ID: id, OperationalFields: fields})
	}
	return out, nil
}

func videoVerificationSources() map[string][]catalogsync.Source {
	return map[string][]catalogsync.Source{
		"wan3.0-video": {videoAPISources[0]}, "wan3.0-video-prime": {videoAPISources[0]},
		"wan2.7-r2v": {videoAPISources[1]}, "wan2.7-r2v-2026-06-12": {videoAPISources[1]},
		"happyhorse-1.1-r2v": {videoAPISources[2]},
	}
}
