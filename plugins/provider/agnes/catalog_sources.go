package agnes

import "github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"

func catalogDocumentReviews() map[string]catalogsync.DocumentReview {
	return map[string]catalogsync.DocumentReview{
		priceSource: {Hash: "05436567053a8da5cfcb0a389e0b3fc78487d318fe7c275267d59908792c1a23", Date: "2026-10-14", Reason: "Added Pro Beta text-token prices and removed Flash Max text-token prices. Image/video prices and billing rules are unchanged; text generation is outside this media adapter's scope."},
	}
}

func catalogSources() []catalogsync.Source {
	return []catalogsync.Source{
		{ID: "overview", URL: "https://www.agnes-ai.com/en/docs/overview", Parser: "document"},
		{ID: "pricing", URL: "https://www.agnes-ai.com/en/docs/pricing", Parser: "document"},
		{ID: "image-2.0", URL: "https://www.agnes-ai.com/en/docs/agnes-image-20-flash", Parser: "document"},
		{ID: "image-2.1", URL: "https://www.agnes-ai.com/en/docs/agnes-image-21-flash", Parser: "document"},
		{ID: "image-2.5", URL: "https://www.agnes-ai.com/en/docs/agnes-image-25-flash", Parser: "document"},
		{ID: "video-2.5", URL: "https://www.agnes-ai.com/en/docs/agnes-video-25", Parser: "document"},
		{ID: "video-flash", URL: "https://www.agnes-ai.com/en/docs/agnes-video-25-flash", Parser: "document"},
	}
}
