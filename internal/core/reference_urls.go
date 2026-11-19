package core

import "fmt"

// IsAssetReference recognizes an opaque provider asset ID, never a fetchable URL.
// Paths, query strings, fragments, credentials and encoded characters are invalid.
func IsAssetReference(value string) bool {
	const prefix = "asset://"
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return false
	}
	for _, c := range value[len(prefix):] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// ValidateReferenceURLs gates provider-managed references on the model capability.
// Reference modes separately enforce media counts and roles.
func ValidateReferenceURLs(model MediaModel, options MediaOptions) error {
	// Name the rejected reference; transports map the field to their request name.
	check := func(field, ref string) error {
		if IsAssetReference(ref) {
			if !model.SupportsAssetReferences {
				return optionError(field, "is a registered asset reference, which this model does not support")
			}
			return nil
		}
		if validateOptionalURL(ref) != nil {
			return optionError(field, "must be an absolute http or https URL without credentials")
		}
		return nil
	}
	for _, list := range []struct {
		field string
		refs  []string
	}{{"image_urls", options.ImageURLs}, {"video_urls", options.VideoURLs}, {"audio_urls", options.AudioURLs}} {
		for i, ref := range list.refs {
			if err := check(fmt.Sprintf("%s.%d", list.field, i), ref); err != nil {
				return err
			}
		}
	}
	if err := check("video_url", options.VideoURL); err != nil {
		return err
	}
	return check("audio_url", options.AudioURL)
}
