package mediafile

// CanonicalType retains the validation API alias for the shared MIME contract.
func CanonicalType(kind string) string { return CanonicalContentType(kind) }
