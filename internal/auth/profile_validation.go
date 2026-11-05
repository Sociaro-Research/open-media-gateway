package auth

// ProfileValidationError contains only a locally checked field path and a fixed
// correction. It never retains names, contact links or rejected column values.
type ProfileValidationError struct {
	field string
	hint  string
}

func (e *ProfileValidationError) Error() string                { return e.field + ": " + e.hint }
func (e *ProfileValidationError) Unwrap() error                { return ErrInvalidProfile }
func (e *ProfileValidationError) Diagnostic() (string, string) { return e.field, e.Error() }
func invalidProfile(field, hint string) error {
	return &ProfileValidationError{field: field, hint: hint}
}

// ErrProfileColorPreset is also used by the storage check under its branding lock.
var ErrProfileColorPreset = &ProfileValidationError{field: "color_preset", hint: "choose a color theme published by an administrator, or use an empty string for the default"}
