package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"time"
)

type ReportFilter struct {
	AllOrganizations bool
	UserID           string
	UserEmail        string
	From, Until      time.Time
	GenerationID     string
	KeyID            string
	Limit, Offset    int
}
type CostReportRow struct {
	UserPicture        string          `json:"user_picture,omitempty"`
	ProfitUSD          *money.USD      `json:"profit_usd,omitempty"`
	ProfitStatus       string          `json:"profit_status,omitempty"`
	ProfitRecognizedAt *time.Time      `json:"profit_recognized_at,omitempty"`
	IdentityHistorical bool            `json:"identity_historical"`
	IdentitySnapshot   json.RawMessage `json:"identity_snapshot,omitempty"`
	ProviderName       string          `json:"provider_name,omitempty"`
	PublicModel        string          `json:"public_model"`
	OrganizationID     string          `json:"organization_id,omitempty"`
	OrganizationName   string          `json:"organization_name,omitempty"`
	UserID             string          `json:"user_id"`
	UserEmail          string          `json:"user_email"`
	ID                 string          `json:"id"`
	CreatedAt          time.Time       `json:"created_at"`
	KeyID              *string         `json:"api_key_id"`
	KeyName            string          `json:"api_key_name"`
	Provider           string          `json:"provider"`
	Model              string          `json:"model"`
	Status             string          `json:"status"`
	Usage              *ProviderUsage  `json:"usage,omitempty"`
	Cost               *TaskCost       `json:"cost"`
	ConsumedUSD        *money.USD      `json:"consumed_usd"`
}

func (s *TaskService) CostReport(ctx context.Context, f ReportFilter) ([]CostReportRow, error) {
	if reader, ok := s.repo.(interface {
		CostReport(context.Context, ReportFilter) ([]CostReportRow, error)
	}); ok {
		return reader.CostReport(ctx, f)
	}
	return nil, errors.New("cost reports are unavailable")
}

// WalkCostReport streams every filtered row without the UI pagination limit.
func (s *TaskService) WalkCostReport(ctx context.Context, f ReportFilter, visit func(CostReportRow) error) error {
	reader, ok := s.repo.(interface {
		WalkCostReport(context.Context, ReportFilter, func(CostReportRow) error) error
	})
	if !ok {
		return errors.New("cost export is unavailable")
	}
	return reader.WalkCostReport(ctx, f, visit)
}
