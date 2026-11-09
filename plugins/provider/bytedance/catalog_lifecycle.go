package bytedance

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/internal/catalogdoc"
)

const deprecationsURL = "https://ai.byteplus.com/ark/region:ap-southeast-1/docs/model-deprecation-notice"

func lifecycleSources() []catalogsync.Source {
	return []catalogsync.Source{{ID: "deprecations", URL: deprecationsURL + ".md", Parser: "document"}}
}

func interpretCatalog(source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	if source.URL == deprecationsURL+".md" {
		return interpretLifecycle(text)
	}
	return interpretVideoAPI(source, text)
}

func interpretLifecycle(text string) ([]catalogsync.Entry, error) {
	if !strings.Contains(text, "# Model deprecations") {
		return nil, errors.New("model deprecations not found")
	}
	date, modelRows := "", false
	var entries []catalogsync.Entry
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, "batch of model deprecations") {
			date, modelRows = "", false
		}
		cells := catalogdoc.Row(line)
		if len(cells) == 4 && cells[0] == "First Notification Date" && cells[2] == "Deactivation Date" {
			date, modelRows = "", false
			continue
		}
		if len(cells) == 4 && catalogdoc.Date(cells[0]) != "" {
			date = catalogdoc.Date(cells[2])
			continue
		}
		if len(cells) == 3 && cells[0] == "Model ID" && cells[2] == "Automated Replacement" {
			modelRows = true
			continue
		}
		if !modelRows || len(cells) != 3 || strings.HasPrefix(cells[0], "---") {
			continue
		}
		words := strings.Fields(cells[0])
		if len(words) == 0 {
			continue
		}
		id := words[0]
		// Reviewed spellings of exact old versions; never strip arbitrary prefixes.
		switch id {
		case "bytedance-seedance-1-0-lite-i2v-250428":
			id = "seedance-1-0-lite-i2v-250428"
		case "bytedance-seedance-1-0-lite-t2v-250428":
			id = "seedance-1-0-lite-t2v-250428"
		case "bytedance-seedream-3-0-t2i-250415":
			id = "seedream-3-0-t2i-250415"
		}
		id = modelcatalog.CanonicalID("bytedance", id)
		if !strings.HasPrefix(id, "seedance-") && !strings.HasPrefix(id, "dreamina-seedance-") && !strings.HasPrefix(id, "seedream-") && !strings.HasPrefix(id, "seededit-") && !strings.HasPrefix(id, "dola-seedream-") {
			continue
		}
		if date == "" {
			return nil, errors.New("model deactivation date not recognized")
		}
		lifecycle := &catalogsync.Lifecycle{ShutdownDate: date}
		needsExact := id == "seedance-1-5-pro-251215" || regexp.MustCompile(regexp.QuoteMeta(id)+`[^.]{0,100}(?:[0-9]{1,2}:[0-9]{2}|UTC[+-])`).MatchString(catalogdoc.Plain(text))
		if needsExact {
			match := regexp.MustCompile(regexp.QuoteMeta(id) + ` model will be shut down after ([0-9]{2}:[0-9]{2}) on ([A-Za-z]+ [0-9]+, [0-9]{4}) \(UTC\+8\)`).FindStringSubmatch(catalogdoc.Plain(text))
			if len(match) != 3 {
				return nil, errors.New("exact media shutdown instant not recognized")
			}
			instant, err := time.Parse("January 2, 2006 15:04 -0700", match[2]+" "+match[1]+" +0800")
			if err != nil || instant.UTC().Format("2006-01-02") != date {
				return nil, errors.New("conflicting media shutdown date")
			}
			lifecycle.ShutdownAt = instant.UTC().Format(time.RFC3339)
		}
		entries = append(entries, catalogsync.Entry{ID: id, Lifecycle: lifecycle})
	}
	// Regional notices remain evidence requiring review. Never infer a global
	// shutdown from a region-specific paragraph, including for unknown future IDs.
	regionalIDs := regexp.MustCompile(`\b(?:dreamina-seedance|seedance|dola-seedream|seedream|seededit)-[a-zA-Z0-9._-]+`)
	for _, region := range regexp.MustCompile(`(?s)Region-specific deactivation.*?(?:\n#{1,6} |$)`).FindAllString(text, -1) {
		for _, id := range regionalIDs.FindAllString(region, -1) {
			id = modelcatalog.CanonicalID("bytedance", id)
			entries = append(entries, catalogsync.Entry{ID: id, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Provider: regional availability", Value: catalogdoc.Plain(region)}}}})
		}
	}
	if len(entries) == 0 {
		return nil, errors.New("media deactivation records not found")
	}
	return entries, nil
}
