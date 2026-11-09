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
	rows := false
	ids := regexp.MustCompile("`([a-z][a-z0-9._-]+)`")
	for _, line := range strings.Split(text, "\n") {
		cells := catalogdoc.Row(line)
		mediaRow := len(cells) > 1 && mediaLifecycleID(cells[1])
		if len(cells) != 3 {
			if mediaRow {
				return nil, errors.New("media shutdown table columns changed")
			}
			rows = false
			continue
		}
		if cells[0] == "Shutdown date" && (strings.HasPrefix(cells[1], "Model") || cells[1] == "Model family / snapshot") {
			rows = true
			continue
		}
		if !rows && mediaRow {
			return nil, errors.New("media shutdown table header not recognized")
		}
		if !rows || strings.HasPrefix(cells[0], "---") {
			continue
		}
		// Inspect the model cell only: recommended replacements are never retired.
		raw := strings.ReplaceAll(line, `\|`, "\x00")
		rawCells := strings.Split(strings.Trim(strings.TrimSpace(raw), "|"), "|")
		for _, match := range ids.FindAllStringSubmatch(rawCells[1], -1) {
			id := match[1]
			// Only file-generating media families are in this adapter's scope.
			if !mediaLifecycleID(id) {
				continue
			}
			date := catalogdoc.Date(cells[0])
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
