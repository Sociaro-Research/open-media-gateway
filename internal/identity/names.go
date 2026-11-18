package identity

import "unicode"

// UnreadableName reports whether a single-line name shows nothing (only
// whitespace and characters that render nothing on their own: format characters
// such as zero-width spaces or direction marks, variation selectors, the
// combining grapheme joiner, Hangul fillers) or contains a bidi embedding,
// override or isolate control, which visually reorders the text around it.
// Letters of any script, digits and emoji are visible, so normal names in any
// language pass.
func UnreadableName(name string) bool {
	visible := false
	for _, r := range name {
		if r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return true
		}
		// Cf plus the two property tables cover Unicode's default-ignorable characters.
		visible = visible || !(unicode.IsSpace(r) || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector))
	}
	return !visible
}
