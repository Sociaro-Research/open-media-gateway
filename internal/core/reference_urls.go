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
	refs := append([]string{}, options.ImageURLs...)
	refs = append(refs, options.VideoURLs...)
	refs = append(refs, options.AudioURLs...)
	refs = append(refs, options.VideoURL, options.AudioURL)
	for _, ref := range refs {
		if IsAssetReference(ref) {
			if !model.SupportsAssetReferences {
				return fmt.Errorf("%w: this model does not support registered asset references", ErrOptionsInvalid)
			}
			continue
		}
		if err := validateOptionalURL(ref); err != nil {
			return err
		}
	}
	return nil
}
