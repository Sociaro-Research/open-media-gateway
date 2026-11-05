package organization

// ValidationError is an allowlisted local constraint, never a database error or
// rejected request value. Unknown reasons fail closed to the generic error.
type ValidationError uint8

const (
	InvalidName ValidationError = iota + 1
	InvalidNameLength
	InvalidCreditLimit
	InvalidDefaultWeeklyLimit
	InvalidWeeklyLimit
	InvalidMarkup
	InvalidCreditAmount
	InvalidCreditKind
	InvalidNegativeCredit
	InvalidCreditReason
	InvalidCreditExternalID
	InvalidManagedCreditAmount
	InvalidManagedCreditKind
	InvalidManagedCreditReason
)

func (e ValidationError) Diagnostic() (string, string) {
	switch e {
	case InvalidName:
		return "name", "name: provide a nonblank organization name"
	case InvalidNameLength:
		return "name", "name: use at most 160 bytes for a new or changed organization name"
	case InvalidCreditLimit:
		return "credit_limit_usd", "credit_limit_usd: use a number from 0 through 1000000 USD"
	case InvalidDefaultWeeklyLimit:
		return "default_weekly_limit_usd", "default_weekly_limit_usd: use a number from 0 through 1000000 USD, or null for unlimited"
	case InvalidWeeklyLimit:
		return "weekly_limit_usd", "weekly_limit_usd: use a number from 0 through 1000000 USD, or null for unlimited"
	case InvalidMarkup:
		return "markup_percent", "markup_percent: provide one value from 0 through 1000 percent with at most two decimals, or null to inherit"
	case InvalidCreditAmount:
		return "amount_usd", "amount_usd: use a nonzero credit amount; negative adjustments must be at least -1000000 USD"
	case InvalidCreditKind:
		return "kind", "kind: choose payment, promo or adjustment"
	case InvalidNegativeCredit:
		return "amount_usd", "amount_usd: use a positive amount for payment or promo; negative amounts require kind adjustment"
	case InvalidCreditReason:
		return "reason", "reason: shorten the credit reason to at most 1000 bytes"
	case InvalidCreditExternalID:
		return "external_id", "external_id: provide a nonblank credit reference of at most 200 bytes"
	case InvalidManagedCreditAmount:
		return "amount_usd", "amount_usd: use a positive amount for a managed client payment"
	case InvalidManagedCreditKind:
		return "kind", "kind: choose payment for a managed client top-up"
	case InvalidManagedCreditReason:
		return "reason", "reason: provide a nonblank reason for a managed client payment"
	default:
		return "", ErrInvalid.Error()
	}
}
func (e ValidationError) Error() string { _, message := e.Diagnostic(); return message }
func (e ValidationError) Unwrap() error { return ErrInvalid }
