package openai

import (
	"errors"
	"regexp"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/internal/catalogdoc"
)

const deprecationsURL = "https://developers.openai.com/api/docs/deprecations"

func lifecycleSources() []catalogsync.Source {
	return []catalogsync.Source{{ID: "deprecations", URL: deprecationsURL + ".md", Parser: "document"}}
}

func interpretLifecycle(source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	if source.URL != deprecationsURL+".md" {
		return nil, nil
	}
	if !strings.Contains(text, "# Deprecations") {
		return nil, errors.New("deprecation document not recognized")
	}
	var entries []catalogsync.Entry
	var header []string
	dateCol, modelCol, features := -1, -1, false
	ids := regexp.MustCompile("`([a-z][a-z0-9._-]+)`")
	for _, line := range strings.Split(text, "\n") {
		cells := catalogdoc.Row(line)
		if cells == nil {
			header = nil
			continue
		}
		if header == nil {
			// Each table's first row names its columns; a repeated name counts as missing.
			header, dateCol, modelCol, features = cells, -1, -1, false
			featureCol := -1
			for i, name := range cells {
				name = strings.ToLower(name)
				col := &featureCol
				if name == "shutdown date" {
					col = &dateCol
				} else if strings.HasPrefix(name, "model") || strings.HasPrefix(name, "affected model") {
					col = &modelCol
				} else if !strings.HasPrefix(name, "affected feature") {
					continue
				}
				if *col == -1 {
					*col = i
				} else {
					*col = -2
				}
			}
			if modelCol == -1 {
				modelCol, features = featureCol, true
			}
			// A media row without a header above it must not be read as one and skipped.
			if (dateCol < 0 || modelCol < 0) && mediaLifecycleID(line) {
				return nil, errors.New("media shutdown table header not recognized")
			}
			continue
		}
		if strings.HasPrefix(cells[0], "---") {
			continue
		}
		mediaRow := mediaLifecycleID(line)
		if dateCol < 0 || modelCol < 0 {
			if mediaRow {
				return nil, errors.New("media shutdown table header not recognized")
			}
			continue
		}
		if len(cells) != len(header) {
			if mediaRow {
				return nil, errors.New("media shutdown table columns changed")
			}
			continue
		}
		// Inspect the affected cell only: replacements, status and notices are never retired.
		raw := strings.ReplaceAll(line, `\|`, "\x00")
		rawCells := strings.Split(strings.Trim(strings.TrimSpace(raw), "|"), "|")
		for _, match := range ids.FindAllStringSubmatch(rawCells[modelCol], -1) {
			id := match[1]
			// Only file-generating media families are in this adapter's scope.
			if !mediaLifecycleID(id) {
				continue
			}
			// A feature or endpoint shutdown does not say the model itself retires.
			if features {
				return nil, errors.New("media model in feature shutdown table")
			}
			date := catalogdoc.Date(cells[dateCol])
			if date == "" {
				return nil, errors.New("media shutdown date not recognized")
			}
			entries = append(entries, catalogsync.Entry{ID: id, Lifecycle: &catalogsync.Lifecycle{ShutdownDate: date}})
		}
	}
	if len(entries) == 0 {
		return nil, errors.New("media shutdown tables not found")
	}
	// These exact historical IDs share the reviewed parent's lifecycle, not a
	// future snapshot inferred by prefix. Explicit snapshot rows take precedence.
	present := map[string]bool{}
	for _, entry := range entries {
		present[entry.ID] = true
	}
	for _, id := range []string{"tts-1-1106", "tts-1-hd-1106"} {
		if present[id] {
			continue
		}
		for _, entry := range entries {
			if entry.ID == canonicalModel(id) {
				entries = append(entries, catalogsync.Entry{ID: id, Lifecycle: entry.Lifecycle})
				break
			}
		}
	}
	return entries, nil
}

func mediaLifecycleID(text string) bool {
	return regexp.MustCompile(`\b(?:gpt-image-|chatgpt-image-|dall-e-|tts-|gpt-4o-mini-tts|gpt-audio|sora-)[a-z0-9._-]*`).MatchString(text)
}
