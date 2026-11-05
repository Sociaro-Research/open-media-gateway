package storage

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"
	"github.com/Sociaro-Research/open-media-gateway/internal/soundoverride"
	"strconv"
	"strings"
)

func init() {
	table := map[string]string{}
	for _, message := range []string{
		"create S3 upload staging directory; ask an operator to check worker temporary-storage permissions and free space",
		"invalid task storage key; ask an operator to check provider output and adapter stream compatibility",
		"provider artifact stream contains invalid JSON; ask an operator to check provider output and adapter stream compatibility",
		"provider artifact stream could not be read; ask an operator to check provider output and adapter stream compatibility",
		"provider artifact stream does not match its declared content type; ask an operator to check provider output and adapter stream compatibility",
		"provider artifact stream has an unsupported content type; ask an operator to check provider output and adapter stream compatibility",
		"provider artifact stream is missing; ask an operator to check provider output and adapter stream compatibility",
		"provider artifact stream size does not match its declared size; ask an operator to check adapter compatibility",
		"provider artifact stream size is invalid; ask an operator to check provider output and adapter stream compatibility",
		"provider artifact validation failed; ask an operator to check provider output and adapter stream compatibility",
		"provider result has an unsupported content type; ask an operator to check the provider output format",
		"provider returned an empty media file; ask an operator to check the original provider output",
		"result archival failed; ask an operator to check result storage, provider download availability and worker health",
		"result storage endpoint was blocked by outbound policy; ask an operator to review the exact trusted endpoint allowlist",
		"result storage hostname could not be resolved; ask an operator to verify DNS and connectivity from Gateway and worker",
		"result storage is full; ask an operator to free space or expand the configured storage",
		"result storage permission denied; ask an operator to check worker storage ownership and write permissions",
		"trusted artifact stream storage is unavailable; ask an operator to check trusted-stream support in the selected storage backend",
	} {
		table[message] = message + archiveRecoveryAction
		table[message+archiveRecoveryAction] = message + archiveRecoveryAction
	}
	for _, message := range []string{"provider artifact stream could not be read", "provider artifact stream is missing", "provider artifact stream has an unsupported content type", "provider artifact stream size is invalid", "provider artifact validation failed", "provider artifact stream contains invalid JSON", "provider artifact stream does not match its declared content type", "provider artifact stream size does not match its declared size", "provider returned an empty media file"} {
		table[message] = message + "; ask an operator to check provider output and adapter stream compatibility" + archiveRecoveryAction
	}
	table[soundoverride.ErrProcessing.Error()] = PublicArchiveError(soundoverride.ErrProcessing)
	for _, message := range []string{"provider result URL is invalid", "provider result request could not be created", "download provider result failed", "download provider result returned an unsuccessful HTTP status"} {
		table[message] = message + "; ask an operator to check provider download availability and outbound connectivity" + archiveRecoveryAction
		table[table[message]] = table[message]
	}
	for _, operation := range []string{"upload", "open", "read", "store template media"} {
		table["S3 "+operation+" failed: storage endpoint blocked by outbound private-address policy"] = PublicArchiveError(ErrPrivateEndpoint)
		table["S3 "+operation+" failed: storage endpoint hostname could not be resolved"] = PublicArchiveError(ErrEndpointDNS)
		for _, message := range []string{"S3 " + operation + " denied; check credentials and bucket permissions", "S3 " + operation + " failed; check endpoint, region, bucket and network connectivity"} {
			table[message] = message + archiveRecoveryAction
			table[table[message]] = table[message]
		}
	}
	// Preserve only constants and canonical positive numeric size bounds.
	diagnostics.RegisterExact("storage/constants", table)
	diagnostics.Register("storage/size", func(raw string) (string, bool) {
		rest, ok := strings.CutPrefix(raw, "provider result exceeds STORAGE_MAX_BYTES (")
		if !ok {
			return "", false
		}
		number, _, ok := strings.Cut(rest, ")")
		if !ok {
			return "", false
		}
		number = strings.TrimSuffix(number, " bytes")
		n, err := strconv.ParseInt(number, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != number {
			return "", false
		}
		old := "provider result exceeds STORAGE_MAX_BYTES (" + number + ")"
		current := oversizedResult(n).Error()
		if raw != old && raw != current && raw != current+archiveRecoveryAction {
			return "", false
		}
		return current + archiveRecoveryAction, true
	})
}
