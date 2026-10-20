package sonilo

import "github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"

func catalogSources() []catalogsync.Source {
	return []catalogsync.Source{
		{ID: "api", URL: "https://platform.sonilo.com/docs/api", Parser: "document"},
		{ID: "openapi", URL: "https://sonilo.com/openapi.json", Parser: "document"},
		{ID: "pricing", URL: "https://platform.sonilo.com/pricing", Parser: "document"},
	}
}
