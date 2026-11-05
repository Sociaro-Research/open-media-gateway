package providerassets

import "github.com/Sociaro-Research/open-media-gateway/internal/diagnostics"

const unknownFailure = "Stored asset diagnostics could not be safely verified. Review this registration and provider inventory; ask an operator to check the asset library connection before registering the file again."

// PublicFailure never changes the repository record or trusts free-form text.
func PublicFailure(raw string) string {
	if raw == "" {
		return ""
	}
	if raw == unknownFailure {
		return unknownFailure
	}
	if raw == "Reconnect the asset library to refresh this registration." {
		return "Reconnect the asset library to refresh this registration."
	}
	if safe, ok := diagnostics.Project(raw); ok {
		return safe
	}
	return unknownFailure
}
func PublicRecord(record Record) Record {
	record.Failure = PublicFailure(record.Failure)
	return record
}
func PublicAsset(asset Asset) Asset { asset.Failure = PublicFailure(asset.Failure); return asset }
