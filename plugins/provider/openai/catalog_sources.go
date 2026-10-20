package openai

import "github.com/Sociaro-Research/media-gateway-dev/internal/catalogsync"

func catalogDocumentReviews() map[string]catalogsync.DocumentReview {
	return map[string]catalogsync.DocumentReview{
		"https://developers.openai.com/api/docs/deprecations": {Hash: "883ba9d155f008ae89e928a137b24e4ce960250a67e2b12ddab1e4656ae197e1", Date: "2026-10-14", Reason: "Added GPT-4 text-model shutdowns and shutdown_date discovery guidance; relocated the April legacy table. Media shutdown dates are unchanged and already recorded in the reviewed model catalogue."},
	}
}

func catalogSources() []catalogsync.Source {
	sources := []catalogsync.Source{{ID: "pricing", URL: priceSource, Parser: "openai-pricing"}, {ID: "changelog", URL: "https://developers.openai.com/api/docs/changelog", Parser: "document"}, {ID: "deprecations", URL: "https://developers.openai.com/api/docs/deprecations", Parser: "document"}}
	for _, id := range modelIDs {
		if canonicalModel(id) != id {
			continue
		} // Snapshots use the canonical documentation page.
		sources = append(sources, catalogsync.Source{ID: "model-" + id, URL: "https://developers.openai.com/api/docs/models/" + id, Parser: "openai-model"})
	}
	return sources
}
