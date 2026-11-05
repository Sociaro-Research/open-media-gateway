package openai

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
		"OpenAI API key is required",
		"OpenAI audio response is missing, refused, or incomplete",
		"OpenAI base URL must be https://api.openai.com/v1",
		"OpenAI image response is malformed or has no single result",
		"OpenAI media endpoints return immediate results and cannot be polled",
		"OpenAI model has been retired",
		"OpenAI models response is malformed",
		"OpenAI request exceeds 64 MiB",
		"OpenAI request failed or timed out; request was not retried",
		"OpenAI response is empty or exceeds 64 MiB",
		"OpenAI returned a non-JSON response",
		"OpenAI returned a non-audio speech response",
		"OpenAI returned empty, oversized, or invalid base64 media",
		"could not build OpenAI request",
		"could not read OpenAI response",
		"unsupported OpenAI model",
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
			message = "access denied; check project permissions and organization verification"
		case 404:
			message = "model or endpoint is unavailable"
		case 429:
			message = "rate limit or quota exceeded"
		}
		add(fmt.Sprintf("OpenAI %s (HTTP %d)", message, status))
	}
	diagnostics.RegisterExact("legacy/openai", table)
}
