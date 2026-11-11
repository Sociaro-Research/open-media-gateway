package minimax

import "github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"

func catalogSources() []catalogsync.Source {
	sources := []catalogsync.Source{}
	for _, p := range []string{
		"guides/models-intro",
		"release-notes/models",
		"release-notes/apis",
		"faq/models",
		"guides/video-generation",
		"api-reference/api-overview",
		"api-reference/video-generation-v2-create",
		"api-reference/video-generation-v2-query",
		"api-reference/video-generation-v2-list",
		"api-reference/video-generation-v2-delete",
		"api-reference/video-generation-v2-h3-context-ir",
		"api-reference/video-generation-v2-regeneration",
		"api-reference/video-generation-t2v",
		"api-reference/video-generation-i2v",
		"api-reference/video-generation-fl2v",
		"api-reference/video-generation-s2v",
		"api-reference/image-generation-t2i",
		"api-reference/image-generation-i2i",
		"api-reference/video-agent-create",
		"faq/video-agent-templates",
		"api-reference/file-management-upload",
		"api-reference/file-management-list",
		"api-reference/file-management-retrieve",
		"api-reference/file-management-retrieve-content",
		"api-reference/file-management-delete",
		"api-reference/video-generation-query",
		"api-reference/video-generation-download",
		"api-reference/video-agent-query",
		"api-reference/models/openai/list-models",
		"pricing/overview",
	} {
		sources = append(sources, catalogsync.Source{ID: p, URL: docsBase + p + ".md", Parser: "document"})
	}
	return sources
}
