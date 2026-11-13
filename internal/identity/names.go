package identity

import "unicode"

// UnreadableName reports whether a single-line name shows nothing (only
// whitespace and invisible format characters such as zero-width spaces or
// direction marks) or contains a bidi embedding, override or isolate control,
// which visually reorders the text around it. Letters of any script, digits and
// emoji are visible, so normal names in any language pass.
func UnreadableName(name string) bool {
	visible := false
	for _, r := range name {
		if r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return true
		}
		visible = visible || !(unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
	}
	return !visible
}
