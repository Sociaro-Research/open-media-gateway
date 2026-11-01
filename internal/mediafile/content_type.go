package mediafile

// CanonicalContentType identifies equivalent WAV, AAC, M4A and HEVC still-image MIME labels.
// Use it on both detected/stored types and model allowlist entries;
// callers must still inspect the bytes and enforce the selected model's limits.
func CanonicalContentType(kind string) string {
	switch kind {
	case "image/heif":
		return "image/heic"
	case "audio/x-m4a", "audio/m4a":
		return "audio/mp4"
	case "audio/x-aac":
		return "audio/aac"
	case "audio/wav", "audio/x-wav":
		return "audio/wave"
	default:
		return kind
	}
}
