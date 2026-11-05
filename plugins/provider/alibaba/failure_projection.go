package alibaba

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func init() {
	diagnostics.Register("legacy/alibaba-http", func(raw string) (string, bool) {
		for _, item := range []struct {
			prefix, outer string
			phase         providerkit.FailurePhase
		}{
			{"provider submission failed: DashScope HTTP ", "provider submission failed: ", providerkit.FailureSubmit},
			{"provider polling failed: DashScope HTTP ", "provider polling failed: ", providerkit.FailurePoll},
			{"DashScope HTTP ", "", providerkit.FailureSubmit},
		} {
			if safe, ok := failurePolicy.ProjectLegacyHTTP(raw, item.prefix, item.phase); ok {
				return item.outer + safe, true
			}
		}
		return "", false
	})
	table := map[string]string{}
	add := func(raw string) {
		table[raw] = raw + ". Check the original generation and provider history; ask an operator to verify the provider connection and adapter compatibility before creating another generation."
	}
	for _, message := range []string{
		"DashScope API key is required",
		"provider bill has no stable detail ID",
	} {
		add(message)
	}

	for _, state := range []string{"FAILED", "CANCELED", "CANCELLED", "UNKNOWN"} {
		add("DashScope task ended with status " + state)
	}
	diagnostics.RegisterExact("legacy/alibaba", table)
}
