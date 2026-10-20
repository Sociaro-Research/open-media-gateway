package providerkit

import (
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

// Header values are write-only in management responses. Nil preserves a saved value.
type Header struct {
	Name      string  `json:"name"`
	Value     *string `json:"value,omitempty"`
	Hint      string  `json:"hint,omitempty"`
	Operation string  `json:"operation,omitempty"` // empty, models, generate, poll
}

type ManualModel struct {
	Model     core.MediaModel `json:"model"`
	Profile   string          `json:"profile"`
	PriceUSD  *float64        `json:"price_usd,omitempty"`
	ExtraBody map[string]any  `json:"extra_body,omitempty"`
}

type Connection struct {
	TimeoutSeconds *int64            `json:"timeout_seconds,omitempty"`
	ManualCatalog  bool              `json:"manual_catalog"`
	Headers        []Header          `json:"headers"`
	Endpoints      map[string]string `json:"endpoints"`
	Models         []ManualModel     `json:"models"`
	ExtraBody      map[string]any    `json:"extra_body,omitempty"`
}
