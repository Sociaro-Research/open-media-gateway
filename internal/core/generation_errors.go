package core

import (
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
)

var knownGenerationFailures = map[string]string{
	"provider task failed":    "Task failed at the provider without a verified diagnostic. Check the original provider task and billing; ask an operator to inspect the provider connection before creating another generation.",
	"provider task timed out": "Provider task status could not be confirmed before its deadline. Check the original provider task and billing; ask an operator to inspect polling and timeout settings before creating another generation.",
	"worker interrupted during provider submission; task was not resubmitted to avoid duplicate generation": "Worker stopped during provider submission. The request was not resubmitted. Check the original provider task and billing before creating another generation.",
	"result storage is not configured":                 "Result storage is not configured. Ask an operator to configure result storage and inspect the original provider task before creating another generation.",
	"result storage configuration could not be loaded": "Result storage configuration could not be loaded. Ask an operator to check the saved storage connection and worker health; check the original provider task before creating another generation.",
	"provider completed without a media URL":           "Provider completed without a media URL. Ask an operator to check adapter compatibility and the original provider task before creating another generation.",
	"provider returned an invalid status":              "Provider returned an invalid task status. Ask an operator to check adapter compatibility and the original provider task before creating another generation.",
	"Override Sound processing is not available":       "Override Sound processing is unavailable. Ask an operator to check the worker's FFmpeg installation and configured storage; check the original provider task before creating another generation.",
}

const missingProviderAction = "Provider is not configured. Ask an operator to enable and configure the provider connection used by this generation; check the original task before creating another generation."

func init() {
	for _, message := range []string{
		"provider returned an invalid artifact count", "provider returned an unknown artifact media type",
		"binary artifacts require a private provider source reference", "provider returned malformed artifact metadata",
		"provider primary artifact does not match its scalar result", "Override Sound requires an archived media URL",
		"stored primary artifact does not match its declared media type", "stored artifact does not match its declared media type",
	} {
		knownGenerationFailures[message] = message + ". Ask an operator to check adapter output, result storage and media-processing compatibility; inspect the original provider task before creating another generation."
	}
}

// PublicGenerationFailure recognizes complete trusted forms, never substrings.
// The returned text is reconstructed; the stored string is not changed.
func PublicGenerationFailure(raw string) string {
	if len(raw) > diagnostics.MaxStoredMessageBytes {
		return diagnostics.UnknownGeneration
	}
	if raw == diagnostics.UnknownGeneration {
		return diagnostics.UnknownGeneration
	}
	if raw == missingProviderAction {
		return missingProviderAction
	}
	if name, ok := strings.CutPrefix(raw, "provider "); ok {
		if name, ok = strings.CutSuffix(name, " is not configured"); ok && len(name) > 0 && len(name) <= 64 {
			valid := true
			for _, c := range name {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
					valid = false
					break
				}
			}
			if valid {
				return missingProviderAction
			}
		}
	}
	if safe, ok := knownGenerationFailures[raw]; ok {
		return safe
	}
	for _, safe := range knownGenerationFailures {
		if raw == safe {
			return safe
		}
	}
	if safe, ok := diagnostics.Project(raw); ok {
		return safe
	}
	prefix, tail := "", ""
	for _, candidate := range []string{"provider submission failed: ", "provider polling failed: ", "store provider result: "} {
		if strings.HasPrefix(raw, candidate) {
			prefix, tail = candidate, strings.TrimPrefix(raw, candidate)
			break
		}
	}
	if prefix == "" && strings.HasPrefix(raw, "store provider artifact ") {
		number, rest, ok := strings.Cut(strings.TrimPrefix(raw, "store provider artifact "), ": ")
		n, err := strconv.Atoi(number)
		if ok && err == nil && n >= 2 && n <= 64 && strconv.Itoa(n) == number {
			prefix = "store provider artifact " + strconv.Itoa(n) + ": "
			tail = rest
		}
	}
	if prefix != "" {
		if safe, ok := diagnostics.Project(tail); ok {
			return prefix + safe
		}
	}
	return diagnostics.UnknownGeneration
}

// PublicGenerationError retains the existing error envelope and drops arbitrary
// extra persisted error properties. Nil remains nil.
func PublicGenerationError(value any) any {
	if value == nil {
		return nil
	}
	raw := ""
	switch e := value.(type) {
	case string:
		raw = e
	case *string:
		if e == nil {
			return nil
		}
		raw = *e
	case map[string]string:
		raw = e["message"]
	case map[string]any:
		raw, _ = e["message"].(string)
	}
	return map[string]string{"code": "generation_failed", "message": PublicGenerationFailure(raw)}
}

// ProjectGenerationRecord changes only the declared error slot of one public
// generation. It deliberately does not walk options, prompts or snapshots.
func ProjectGenerationRecord(fields map[string]any) {
	object, _ := fields["object"].(string)
	if object != "generation" && object != "video" {
		return
	}
	if value, ok := fields["error"]; ok {
		fields["error"] = PublicGenerationError(value)
	}
}

// ProjectGenerationPayload is for callers that already admitted a generation
// response contract: single record, list.data or report.generation only.
func ProjectGenerationPayload(value any) {
	fields, ok := value.(map[string]any)
	if !ok {
		return
	}
	object, _ := fields["object"].(string)
	switch object {
	case "generation", "video":
		ProjectGenerationRecord(fields)
	case "list":
		if rows, ok := fields["data"].([]any); ok {
			for _, row := range rows {
				if item, ok := row.(map[string]any); ok {
					ProjectGenerationRecord(item)
				}
			}
		}
	default:
		if generation, ok := fields["generation"].(map[string]any); ok {
			ProjectGenerationRecord(generation)
		}
	}
}
