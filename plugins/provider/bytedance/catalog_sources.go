package bytedance

import "github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"

func catalogSources() []catalogsync.Source {
	return []catalogsync.Source{
		{ID: "models", URL: "https://docs.byteplus.com/en/docs/modelark/1330310", Parser: "byteplus-models"},
		{ID: "pricing", URL: "https://docs.byteplus.com/en/docs/modelark/1544106", Parser: "byteplus-pricing"},
		{ID: "releases", URL: "https://docs.byteplus.com/en/docs/modelark/1159178", Parser: "byteplus-releases"},
		{ID: "releases-archive", URL: "https://docs.byteplus.com/en/docs/modelark/2172655", Parser: "byteplus-releases"},
	}
}
