package branding

import "maps"

// Danger roles are optional in legacy palettes and intentionally independent of
// their customized accent and error colors. Keep them out of raw Defaults maps:
// JSON decoding merges maps, which would otherwise erase omission information.
func dangerDefaults(light bool) map[string]string {
	if light {
		return map[string]string{"danger": "#b42336", "on-danger": "#ffffff"}
	}
	return map[string]string{"danger": "#f08088", "on-danger": "#171717"}
}

// PreserveDangerColors retains roles unknown to older clients. Call it after
// version validation and legacy catalog conversion, before normalization. Only
// an absent key is inherited; explicit invalid values remain invalid. Inputs
// are cloned so the stored before snapshot remains accurate for audit logging.
func PreserveDangerColors(next, before Settings) Settings {
	previous := make(map[string]Preset, len(before.Presets))
	for _, preset := range before.Presets {
		previous[preset.ID] = preset
	}
	if next.Presets == nil {
		return next
	}
	copyPalette := func(palette, stored map[string]string) map[string]string {
		result := maps.Clone(palette)
		if result == nil {
			return nil
		}
		for _, role := range []string{"danger", "on-danger"} {
			if _, supplied := result[role]; !supplied {
				if value, exists := stored[role]; exists {
					result[role] = value
				}
			}
		}
		return result
	}
	presets := make([]Preset, len(next.Presets))
	for i, preset := range next.Presets {
		stored := previous[preset.ID]
		preset.Dark = copyPalette(preset.Dark, stored.Dark)
		preset.Light = copyPalette(preset.Light, stored.Light)
		presets[i] = preset
	}
	next.Presets = presets
	return next
}
