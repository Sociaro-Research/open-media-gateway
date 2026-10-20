package modelcatalog

// CanonicalID maps only reviewed aliases of the same published model version.
// Never normalize punctuation or prefixes for an unknown model by inference.
func CanonicalID(provider, id string) string {
	if provider == "bytedance" {
		switch id {
		case "seedance-1.0-pro-fast-251015":
			return "seedance-1-0-pro-fast-251015"
		case "seedream-3.0-t2i-250415":
			return "seedream-3-0-t2i-250415"
		case "seedream-5-0-lite-260128":
			return "seedream-5-0-260128"
		}
	}
	return id
}
