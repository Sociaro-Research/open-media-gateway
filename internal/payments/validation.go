package payments

import "errors"

// ValidationError carries only reviewed field names and correction instructions.
// Rejected values, credentials and provider responses are never retained.
type ValidationError struct {
	field string
	hint  string
}

func (e *ValidationError) Error() string                { return e.field + ": " + e.hint }
func (e *ValidationError) Unwrap() error                { return ErrInvalid }
func (e *ValidationError) Diagnostic() (string, string) { return e.field, e.Error() }

func invalid(field, hint string) error { return &ValidationError{field: field, hint: hint} }

func invalidAmount() error {
	return invalid("amount_usd", "use whole cents between $1 and $10,000")
}
func invalidReference(field string) error {
	return invalid(field, "use 1–128 bytes without leading/trailing whitespace, tabs or line breaks")
}
func invalidProviderName() error {
	return invalid("name", "provide a nonblank payment provider name of at most 100 bytes")
}

// ErrCallbackOrigin identifies a local operator setting without retaining its value.
var ErrCallbackOrigin = errors.New("payment callbacks require AUTH_PUBLIC_URL to be an HTTPS origin")
