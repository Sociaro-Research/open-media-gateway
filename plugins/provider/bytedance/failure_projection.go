package bytedance

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func init() {
	diagnostics.Register("legacy/bytedance-http", func(raw string) (string, bool) {
		for prefix, phase := range map[string]providerkit.FailurePhase{
			"create ByteDance video task: HTTP ": providerkit.FailureSubmit,
			"create ByteDance image: HTTP ":      providerkit.FailureSubmit,
			"get ByteDance video task: HTTP ":    providerkit.FailurePoll,
			"list ByteDance models: HTTP ":       providerkit.FailureCheck,
		} {
			if safe, ok := failurePolicy.ProjectLegacyHTTP(raw, prefix, phase); ok {
				return safe, true
			}
		}
		return "", false
	})
	table := map[string]string{}
	add := func(raw string) {
		table[raw] = raw + ". Check the original generation and provider history; ask an operator to verify the provider connection and adapter compatibility before creating another generation."
	}
	for _, message := range []string{
		"ByteDance API key is required",
		"ByteDance base URL must be an absolute HTTPS URL",
		"provider bill has no stable detail ID",
	} {
		add(message)
	}

	for _, state := range []string{"failed", "cancelled", "expired"} {
		add("ByteDance task ended with status " + state)
	}
	add("BytePlus could not prepare this asset. Check its format, dimensions and project.")
	diagnostics.RegisterExact("legacy/bytedance", table)
}
