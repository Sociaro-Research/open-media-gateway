package core

import (
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"time"
)

type BillRecord struct {
	MediaType     string `json:"media_type,omitempty"`
	GenerateAudio *bool  `json:"generate_audio,omitempty"`
	// Usage fields describe one provider billing window, not a request ID.
	Model          string          `json:"model,omitempty"`
	UsageStart     *time.Time      `json:"usage_start,omitempty"`
	UsageEnd       *time.Time      `json:"usage_end,omitempty"`
	Units          *float64        `json:"units,omitempty"`
	PackageUnits   *float64        `json:"package_units,omitempty"`
	TariffUSD      *money.USD      `json:"tariff_usd,omitempty"`
	ImageOperation string          `json:"image_operation,omitempty"`
	ID             string          `json:"id"`
	InstanceID     string          `json:"instance_id"`
	Currency       string          `json:"currency"`
	PaidUSD        *money.USD      `json:"paid_usd,omitempty"`
	Settled        bool            `json:"settled"`
	Raw            json.RawMessage `json:"raw"`
}

type BillingStatus struct {
	Provider  string    `json:"provider"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
}
