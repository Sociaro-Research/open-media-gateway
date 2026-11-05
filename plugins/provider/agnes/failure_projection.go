package agnes

import (
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
)

func init() {
	table := map[string]string{}
	add := func(raw string) {
		table[raw] = raw + ". Check the original generation and provider history; ask an operator to verify the provider connection and adapter compatibility before creating another generation."
	}
	for _, message := range []string{
		"Agnes API key is required",
		"Agnes authentication query returned an unsuccessful or malformed response; API key remains unverified",
		"Agnes authentication query returned malformed model metadata; API key remains unverified",
		"Agnes base URL must be https://apihub.agnes-ai.com/v1",
		"Agnes completed video has no valid result URL",
		"Agnes image response is malformed or has no single result",
		"Agnes models response is malformed",
		"Agnes request failed or timed out; request was not retried",
		"Agnes request is invalid or exceeds 64 MiB",
		"Agnes response is unreadable, empty or exceeds 64 MiB",
		"Agnes returned a non-JSON response",
		"Agnes returned an invalid image URL",
		"Agnes returned an unknown task status",
		"Agnes returned invalid image Base64",
		"Agnes returned unsupported image bytes",
		"Agnes task response is malformed",
		"Agnes video generation failed; check provider task details",
		"Agnes video response has no valid video_id",
		"Agnes video task failed during creation",
		"could not build Agnes request",
		"invalid Agnes task handle",
	} {
		add(message)
	}

	for status := 100; status <= 599; status++ {
		message := "request failed"
		switch status {
		case 400, 422:
			message = "request rejected; check model options and content"
		case 401:
			message = "API key is invalid"
		case 403:
			message = "access denied"
		case 404:
			message = "model, task or endpoint is unavailable"
		case 429:
			message = "rate limit or quota exceeded"
		case 500:
			message = "internal provider error; try again later"
		case 502:
			message = "upstream service returned an invalid response; try again later"
		case 503:
			message = "service temporarily unavailable; try again later"
		case 504:
			message = "upstream service timed out"
		}
		messages := []string{message}
		for _, code := range []string{"no_available_channel", "model_unavailable", "service_unavailable", "server_overloaded", "insufficient_quota", "insufficient_balance", "rate_limit_exceeded", "invalid_api_key"} {
			messages = append(messages, knownFailure([]byte(fmt.Sprintf("{\"code\":%q}", code))))
		}
		for _, message := range messages {
			add(fmt.Sprintf("Agnes %s (HTTP %d)", message, status))
			if status >= 500 {
				add(fmt.Sprintf("Agnes %s (HTTP %d); submission was not retried; check provider history before resubmitting", message, status))
			}
		}
	}
	diagnostics.RegisterExact("legacy/agnes", table)
}
