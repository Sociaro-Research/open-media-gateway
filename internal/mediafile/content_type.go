package mediafile

// CanonicalContentType identifies equivalent WAV labels without admitting new
// formats. Use it on both detected/stored types and model allowlist entries;
// callers must still inspect the bytes and enforce the selected model's limits.
func CanonicalContentType(kind string) string {
	switch kind {
	case "audio/wav", "audio/x-wav":
		return "audio/wave"
	default:
		return kind
	}
}
