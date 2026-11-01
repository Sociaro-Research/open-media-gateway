package modelcatalog

import (
	_ "embed"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

//go:embed reference_recommendations.json
var recommendationData []byte
var recommendations = func() map[string]map[string]*core.ReferencePromptRecommendation {
	var r map[string]map[string]*core.ReferencePromptRecommendation
	if err := json.Unmarshal(recommendationData, &r); err != nil {
		panic(err)
	}
	return r
}()

func ReferenceRecommendation(provider, id string) *core.ReferencePromptRecommendation {
	return recommendations[provider][id].Clone()
}

// ParsedReferenceRecommendations extracts only literal mentions in a single
// model-bound API request example. It never reconstitutes a larger prose claim
// from an ID elsewhere on a multi-model page.
func ParsedReferenceRecommendations(provider, source, text string) map[string]*core.ReferencePromptRecommendation {
	out := map[string]*core.ReferencePromptRecommendation{}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return out
	}
	raw := html.UnescapeString(text)
	blocks := regexp.MustCompile("(?s)```[^\n]*\n(.*?)```").FindAllStringSubmatch(raw, -1)
	modelField := regexp.MustCompile(`"model"\s*:\s*"([^"\n]+)"`)
	promptField := regexp.MustCompile(`"(?:prompt|text)"\s*:\s*("(?:[^"\\]|\\.)*")`)
	mention := regexp.MustCompile(`@(?:Image|Video|Audio)_? ?[1-9][0-9]*|\[Image [1-9][0-9]*\]|<(?:Picture|Audio|Video) [1-9][0-9]*>|character[1-9][0-9]*|(?:Image|Video|Audio) [1-9][0-9]*|@image_[1-9][0-9]*`)
	for id, r := range recommendations[provider] {
		v, err := url.Parse(r.SourceURL)
		if err != nil {
			continue
		}
		same := strings.TrimSuffix(u.Path, ".md") == strings.TrimSuffix(v.Path, ".md") && u.Host == v.Host
		if !same {
			continue
		}
		literals := []string{}
		seen := map[string]bool{}
		for _, block := range blocks {
			models := modelField.FindAllStringSubmatch(block[1], -1)
			if len(models) != 1 || models[0][1] != id {
				continue
			}
			for _, field := range promptField.FindAllStringSubmatch(block[1], -1) {
				var prompt string
				if json.Unmarshal([]byte(field[1]), &prompt) != nil {
					continue
				}
				for _, token := range mention.FindAllString(prompt, -1) {
					if !seen[token] {
						seen[token] = true
						literals = append(literals, token)
					}
				}
			}
		}
		if len(literals) > 0 {
			out[id] = &core.ReferencePromptRecommendation{Text: "The endpoint request example uses " + strings.Join(literals, ", ") + ". These are examples, not an exclusive grammar.", Kind: "example", SourceURL: r.SourceURL, Modes: append([]string(nil), r.Modes...)}
		}
	}
	return out
}
