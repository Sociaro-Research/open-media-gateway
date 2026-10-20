package payments

import (
	"context"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

// Settlement records provider settlement amounts, not the cardholder's bank currency.
type Settlement struct {
	Currency      string `json:"currency"`
	AmountMinor   int64  `json:"amount_minor"`
	FeeMinor      int64  `json:"fee_minor"`
	DecimalPlaces int    `json:"decimal_places"`
}

type BalanceEntry struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Amount    money.USD `json:"amount_usd"`
	Kind      string    `json:"kind"`
	Reason    string    `json:"reason"`
	Provider  string    `json:"provider"`
	Payment   *Payment  `json:"payment,omitempty"`
}
type HistoryRepository interface {
	BalanceHistory(context.Context, string, string, int) ([]BalanceEntry, error)
}
