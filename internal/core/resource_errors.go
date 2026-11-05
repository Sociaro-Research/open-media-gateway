package core

import "errors"

// Closed type: arbitrary provider errors and exported option-error structs must
// never become trusted resource input diagnostics by wrapping ErrOptionsInvalid.
type resourceValidationError struct{ field, hint string }

func (e *resourceValidationError) Error() string {
	return ErrOptionsInvalid.Error() + ": resource_intent " + e.field + " " + e.hint
}
func (e *resourceValidationError) Unwrap() error   { return ErrOptionsInvalid }
func resourceIntentError(field, hint string) error { return &resourceValidationError{field, hint} }

// ResourceValidationDetails exposes only bounded local validation diagnostics.
func ResourceValidationDetails(err error) (field, hint string, ok bool) {
	var detail *resourceValidationError
	if errors.As(err, &detail) {
		return detail.field, detail.hint, true
	}
	return "", "", false
}
