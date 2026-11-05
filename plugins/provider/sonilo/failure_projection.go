package sonilo

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
		"Sonilo ",
		"Sonilo API key is required",
		"Sonilo account services response is malformed",
		"Sonilo base URL must be https://api.sonilo.com/v1",
		"Sonilo request failed or timed out; submission was not retried",
		"Sonilo response is empty or exceeds 1 MiB",
		"Sonilo returned a non-JSON response; async mode is required",
		"Sonilo returned an invalid media result",
		"Sonilo returned an unknown task status",
		"Sonilo returned no audio result",
		"Sonilo returned no primary audio stream or requested speech mix",
		"Sonilo returned the wrong output type",
		"Sonilo submission response is malformed",
		"Sonilo task response is malformed or belongs to another operation",
		"could not build Sonilo request",
		"could not read Sonilo response",
		"invalid Sonilo task operation",
		"invalid Sonilo task reference",
	} {
		add(message)
	}

	for status := 100; status <= 599; status++ {
		message := "request failed"
		switch status {
		case 400, 422:
			message = "request rejected; check inputs, timeline and content"
		case 401:
			message = "API key is invalid"
		case 402:
			message = "insufficient balance or credits"
		case 403:
			message = "service disabled or permission denied"
		case 404:
			message = "endpoint or task is unavailable"
		case 413:
			message = "input exceeds account upload limits"
		case 429:
			message = "rate or concurrency limit exceeded; retry later"
		}
		add(fmt.Sprintf("Sonilo %s (HTTP %d)", message, status))
	}
	for _, code := range []string{"", "auth_invalid", "payment_required", "insufficient_balance", "forbidden", "rate_limit_exceeded", "invalid_request", "validation_error"} {
		add("Sonilo " + safeError(code))
	}
	diagnostics.RegisterExact("legacy/sonilo", table)
}
