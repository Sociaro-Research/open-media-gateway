package sonilo

import "github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"

func completeOpenAPISource() catalogsync.Source {
	return catalogsync.Source{ID: "openapi-complete", URL: "https://platform.sonilo.com/openapi.json", Parser: "document"}
}

func catalogSources() []catalogsync.Source {
	return []catalogsync.Source{
		{ID: "api", URL: "https://platform.sonilo.com/docs/api", Parser: "document"},
		{ID: "openapi", URL: "https://sonilo.com/openapi.json", Parser: "document"},
		completeOpenAPISource(),
		{ID: "pricing", URL: "https://platform.sonilo.com/pricing", Parser: "document"},
	}
}

// The public core spec omits native video-output and combined-sound paths.
// Keep both documents as independent evidence; a successful fetch alone does
// not interpret their contracts or resolve disagreements between them.
func catalogVerificationSources() map[string][]catalogsync.Source {
	sources := map[string][]catalogsync.Source{}
	for _, op := range operations {
		sources[op.id] = []catalogsync.Source{completeOpenAPISource()}
	}
	return sources
}
