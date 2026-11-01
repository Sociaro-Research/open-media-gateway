// Package catalogdoc contains pure helpers for provider-owned documentation
// interpreters. Missing or ambiguous observations are never replaced by defaults.
package catalogdoc

import (
	"encoding/json"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Fields map[string]json.RawMessage

func (f Fields) Put(key string, value any) { b, _ := json.Marshal(value); f[key] = b }
func Plain(text string) string {
	text = regexp.MustCompile(`(?m)^[ \t]*>[ \t]?`).ReplaceAllString(text, "")
	text = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(strings.NewReplacer("*", "", "`", "", "\u200b", "", "==new==", "", "\\-", "-").Replace(html.UnescapeString(text))), " ")
}
func Between(text, start, end string) string {
	a := regexp.MustCompile(`(?i)` + start).FindStringIndex(text)
	if a == nil {
		return ""
	}
	tail := text[a[1]:]
	b := regexp.MustCompile(`(?i)` + end).FindStringIndex(tail)
	if b == nil {
		return ""
	}
	return tail[:b[0]]
}
func First(text, pattern string) []string {
	return regexp.MustCompile(`(?i)` + pattern).FindStringSubmatch(text)
}
func Int(text, pattern string) (int, bool) {
	m := First(text, pattern)
	if len(m) < 2 {
		return 0, false
	}
	n, e := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	return n, e == nil
}
func Range(text, pattern string) (int, int, bool) {
	m := First(text, pattern)
	if len(m) < 3 {
		return 0, 0, false
	}
	a, e := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	b, e2 := strconv.Atoi(strings.ReplaceAll(m[2], ",", ""))
	return a, b, e == nil && e2 == nil && a <= b
}
func Tokens(text, pattern string) []string {
	set := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?i)`+pattern).FindAllString(text, -1) {
		set[strings.ToLower(m)] = true
	}
	out := []string{}
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
func Selectors(f Fields, text string) {
	resolution := Between(text, `resolution\s+string`, `ratio\s+string`)
	if values := Tokens(resolution, `\b(?:480p|720p|1080p|4k)\b`); len(values) > 0 {
		f.Put("output.resolutions", values)
	}
	ratio := Between(text, `ratio\s+string`, `duration\s+integer`)
	if values := Tokens(ratio, `\b(?:[1-9][0-9]*:[1-9][0-9]*|adaptive)\b`); len(values) > 0 {
		f.Put("output.ratios", values)
	}
}
func Count(f Fields, mode, kind string, min, max int) {
	f.Put("workflow."+mode+"."+kind+".count", map[string]int{"min": min, "max": max})
}
func Duration(f Fields, min, max, def int) {
	f.Put("output.duration", map[string]int{"min": min, "max": max, "default": def, "step": 1})
}
func Mime(text string) []string {
	mappings := map[string][]string{"jpeg": {"image/jpeg"}, "jpg": {"image/jpeg"}, "png": {"image/png"}, "webp": {"image/webp"}, "gif": {"image/gif"}, "bmp": {"image/bmp"}, "tiff": {"image/tiff"}, "heic": {"image/heic"}, "heif": {"image/heif"}, "mp4": {"video/mp4"}, "mov": {"video/quicktime"}, "mp3": {"audio/mpeg"}, "wav": {"audio/wav", "audio/wave", "audio/x-wav"}}
	out := map[string]bool{}
	for _, token := range Tokens(text, `\b(?:jpeg|jpg|png|webp|gif|bmp|tiff|heic|heif|mp4|mov|mp3|wav)\b`) {
		for _, mime := range mappings[token] {
			out[mime] = true
		}
	}
	result := []string{}
	for value := range out {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func Bytes(text string) (int64, bool) {
	// Native MaxBytes is an exclusive admission bound. Preserve the distinction
	// between "less than" and a provider's inclusive "up to / must not exceed".
	pattern := `(?:maximum file size:|file size(?: per clip)?: (?:up to|no more than)|size: (?:each )?(?:video|audio file) must not exceed|single image must be less than|size: no more than|size:)[ ]*([0-9]+)[ ]*MB`
	m := First(text, pattern)
	if len(m) < 2 {
		return 0, false
	}
	n, e := strconv.ParseInt(m[1], 10, 64)
	if e != nil {
		return 0, false
	}
	exclusive := n * 1000000
	if !strings.Contains(strings.ToLower(m[0]), "less than") {
		exclusive++
	}
	return exclusive, true
}

func ImageDimensions(text string) map[string]any {
	min, max, ok := Range(text, `width and height[^.]{0,70}?(?:between|\[)\s*([0-9,]+)\s*(?:and|,)\s*([0-9,]+)`)
	if !ok {
		min, max, ok = Range(text, `resolution:\s*\[([0-9,]+),\s*([0-9,]+)\]`)
	}
	if !ok {
		if n, found := Int(text, `shortest side must be at least\s*([0-9]+)`); found {
			min = n
			max = 0
			ok = true
		}
	}
	if !ok {
		return nil
	}
	low, high := 0.0, 0.0
	if m := First(text, `aspect ratio[^.]{0,40}?1:([0-9]+)\s*to\s*([0-9]+):1`); len(m) == 3 {
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		if a > 0 {
			low, high = 1/a, b
		}
	}
	if low == 0 {
		if ratio, found := Int(text, `aspect ratio: no more than\s*([0-9]+):1`); found && ratio > 0 {
			low, high = 1/float64(ratio), float64(ratio)
		}
	}
	if m := First(text, `aspect ratio[^.]{0,40}?\[([0-9.]+),\s*([0-9.]+)\]`); len(m) == 3 {
		low, _ = strconv.ParseFloat(m[1], 64)
		high, _ = strconv.ParseFloat(m[2], 64)
	}
	minPixels, maxPixels := 0, 0
	pixel := Between(text, `total pixels:`, `size:`)
	if a, b, ok := Range(pixel, `range\s*\[([0-9]+),\s*([0-9]+)\]`); ok {
		minPixels, maxPixels = a, b
	}
	return map[string]any{"min_dimension": min, "max_dimension": max, "min_pixels": minPixels, "max_pixels": maxPixels, "min_ratio": low, "max_ratio": high}
}
func Rule(fields Fields, modes []string, kind, text string) {
	formats := Mime(Between(text, `(?:formats?:|video format:|format:)`, `(?:resolution:|aspect ratio|duration:|size:|supported encoding)`))
	// When no later heading exists, a short explicit format list still suffices.
	if len(formats) == 0 {
		if m := First(text, `(?:formats?:|video format:)\s*([A-Za-z ,/]+)\.`); len(m) == 2 {
			formats = Mime(m[1])
		}
	}
	bytes, hasBytes := Bytes(text)
	dims := ImageDimensions(text)
	for _, mode := range modes {
		prefix := "workflow." + mode + "." + kind
		if len(formats) > 0 {
			fields.Put(prefix+".content_types", formats)
		}
		if hasBytes {
			fields.Put(prefix+".max_bytes", bytes)
		}
		if kind == "image" && (strings.Contains(text, "alpha channel is not supported") || strings.Contains(text, "transparent channel not supported")) {
			fields.Put(prefix+".no_alpha", true)
		}
		if kind == "video" {
			if min, max, ok := Range(text, `frame rate(?:\s*\(FPS\))?:\s*\[([0-9]+),\s*([0-9]+)\]`); ok {
				fields.Put(prefix+".fps", map[string]int{"min": min, "max": max})
			} else if min, ok := Int(text, `frame rate:\s*≥\s*([0-9]+)`); ok {
				fields.Put(prefix+".fps", map[string]int{"min": min, "max": 0})
			}
			codecs := []string{}
			if len(First(text, `\bH\.?264\b`)) > 0 {
				codecs = append(codecs, "avc1", "avc3")
			}
			if len(First(text, `\b(?:H\.?265|HEVC)\b`)) > 0 {
				codecs = append(codecs, "hvc1", "hev1")
			}
			if len(codecs) > 0 {
				fields.Put(prefix+".codecs", codecs)
			}
		}

		if (kind == "image" || kind == "video") && dims != nil {
			fields.Put(prefix+".dimensions", dims)
		}
	}
}

func HasModel(text, id string) bool {
	return id != "" && regexp.MustCompile(`(?:^|[^\p{L}\p{N}._-])`+regexp.QuoteMeta(id)+`(?:[^\p{L}\p{N}._-]|$)`).MatchString(text)
}
