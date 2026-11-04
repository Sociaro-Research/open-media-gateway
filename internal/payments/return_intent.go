package payments

import "time"

// ReturnIntentInput is supplied by the admitted central checkout operation,
// never decoded directly from browser JSON. Authority comes from the verified
// delegated context; destinations, actors and epochs are not caller fields.
type ReturnIntentInput struct {
	OperationID     string `json:"-"`
	PaymentID       string `json:"-"`
	CheckoutAttempt int64  `json:"-"`
}

// ReturnIntent is server-private receipt material. Only its typed callback URL
// can be passed to the payment adapter. It conveys no settlement authority.
type ReturnIntent struct {
	Token           string    `json:"-"`
	PaymentID       string    `json:"-"`
	CheckoutAttempt int64     `json:"-"`
	ExpiresAt       time.Time `json:"-"`
}
