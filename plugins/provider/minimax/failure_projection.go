package minimax

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
)

func init() {
	table := map[string]string{}
	add := func(raw string) {
		table[raw] = raw + ". Check the original generation and provider history; ask an operator to verify the provider connection and adapter compatibility before creating another generation."
	}
	for _, message := range []string{
		"MiniMax API key is required and must be a single line",
		"MiniMax Context-IR task ID is invalid",
		"MiniMax H3 price rows are incomplete; retain last confirmed tariffs",
		"MiniMax Video Agent returned an invalid task ID",
		"MiniMax cancellation result is malformed; outcome is uncertain",
		"MiniMax context task has no enhanced prompt",
		"MiniMax context/regeneration prices, units or recharge rule changed; retain last confirmed tariffs",
		"MiniMax endpoint must be https://api.minimax.io; regional products require separately reviewed contracts",
		"MiniMax file content cannot be read",
		"MiniMax file content does not match its owned media kind",
		"MiniMax file content is empty",
		"MiniMax file deletion result is malformed; outcome is uncertain",
		"MiniMax file download exceeds the limit",
		"MiniMax file download has no supported media signature",
		"MiniMax file download transport failed",
		"MiniMax file listing returned an invalid file",
		"MiniMax file purpose changed",
		"MiniMax file response does not match the requested resource",
		"MiniMax generation returned a file with an unexpected purpose",
		"MiniMax image allowances or free-audio rule changed; retain last confirmed tariffs",
		"MiniMax image failure count is invalid",
		"MiniMax image price is invalid",
		"MiniMax image result set or safety counters are incomplete",
		"MiniMax image success count is invalid",
		"MiniMax image-01 price is missing or ambiguous",
		"MiniMax legacy duration price rows are duplicated",
		"MiniMax legacy duration price rows are incomplete; retain last confirmed tariffs",
		"MiniMax legacy price has unsupported selectors",
		"MiniMax legacy price is invalid",
		"MiniMax legacy result file ID is invalid",
		"MiniMax legacy task identity does not match its handle",
		"MiniMax model-list schema is malformed; authentication is unverified",
		"MiniMax output file has an unexpected purpose",
		"MiniMax price document exceeds reviewed size",
		"MiniMax price is invalid",
		"MiniMax price or USD unit is missing",
		"MiniMax price row has an ambiguous model label",
		"MiniMax price row introduces an unreviewed resolution",
		"MiniMax price row names an unreviewed model",
		"MiniMax price row shape changed",
		"MiniMax price rows are duplicated",
		"MiniMax provider connection identity is not bound",
		"MiniMax regeneration task ID is invalid",
		"MiniMax request could not be created",
		"MiniMax request exceeds 64 MB or cannot be encoded",
		"MiniMax resource is not a video-generation handle",
		"MiniMax response is unreadable or exceeds the response limit",
		"MiniMax response status code is missing",
		"MiniMax response status envelope is missing",
		"MiniMax returned an invalid image result URL",
		"MiniMax returned an invalid response schema",
		"MiniMax returned an invalid video result URL",
		"MiniMax returned an unknown legacy task state",
		"MiniMax returned invalid Base64 image content",
		"MiniMax returned malformed JSON",
		"MiniMax returned mixed image encodings",
		"MiniMax returned no image artifacts",
		"MiniMax source output usage is invalid",
		"MiniMax submission returned an invalid task ID",
		"MiniMax task identity does not match its handle",
		"MiniMax task identity, type or state does not match its handle",
		"MiniMax task list contains an unrequested resource",
		"MiniMax task list duplicates a resource",
		"MiniMax task list exceeds the requested owner scope",
		"MiniMax task list ignored the requested filter",
		"MiniMax task model does not match its operation",
		"MiniMax transport failed; submission outcome may be unknown",
		"MiniMax upload returned an invalid file; outcome may be unknown",
		"MiniMax usage counter is not a non-negative integer",
		"MiniMax usage counters are inconsistent",
		"MiniMax usage is malformed",
		"MiniMax video task has an invalid output URL",
		"MiniMax video task has no final output or recognized state",
		"MiniMax video task has no output URL",
		"invalid MiniMax context/regeneration tariff",
		"invalid MiniMax file ID",
		"invalid MiniMax regeneration task ID",
		"invalid MiniMax resource handle or resource owner",
	} {
		add(message)
	}

	for status := 0; status <= 599; status++ {
		if status != 0 && status < 100 {
			continue
		}
		for _, code := range []int{0, 1000, 1013, 1001, 1002, 1039, 1004, 2049, 1008, 1026, 1027, 2013} {
			add((&APIError{Status: status, Code: code}).Error())
		}
	}
	add("MiniMax task failed")
	add("MiniMax task cancelled")
	diagnostics.RegisterExact("legacy/minimax", table)
}
