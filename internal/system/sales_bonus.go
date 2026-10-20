package system

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"time"
)

var ErrBonusInput = errors.New("enter valid bonus percentages, payment amount, date, method and operation ID")

type BonusSettings struct {
	ManagerPercent    money.Percent `json:"manager_percent"`
	SupervisorPercent money.Percent `json:"supervisor_percent"`
	Version           int64         `json:"version"`
	StartsAt          time.Time     `json:"starts_at"`
}
type BonusPerson struct {
	Picture string `json:"picture,omitempty"`

	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Email   string    `json:"email"`
	Role    string    `json:"role"`
	Accrued money.USD `json:"accrued_usd"`
	Paid    money.USD `json:"paid_usd"`
	Balance money.USD `json:"balance_usd"`
	CanPay  bool      `json:"can_pay"`
}
type BonusDashboard struct {
	ActorID       string        `json:"actor_id"`
	Administrator bool          `json:"administrator"`
	Settings      BonusSettings `json:"settings"`
	People        []BonusPerson `json:"people"`
}
type BonusEntry struct {
	ActorID      string `json:"actor_id,omitempty"`
	ActorPicture string `json:"actor_picture,omitempty"`

	ID          int64     `json:"id"`
	EmployeeID  string    `json:"employee_id"`
	TaskID      string    `json:"task_id,omitempty"`
	Kind        string    `json:"kind"`
	Beneficiary string    `json:"beneficiary,omitempty"`
	Amount      money.USD `json:"amount_usd"`
	CreatedAt   time.Time `json:"created_at"`
	PaidAt      string    `json:"paid_at,omitempty"`
	Method      string    `json:"method,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	ActorName   string    `json:"actor_name,omitempty"`
}
type BonusPayout struct {
	Amount     money.USD `json:"amount_usd"`
	PaidAt     string    `json:"paid_at"`
	Method     string    `json:"method"`
	Reason     string    `json:"reason"`
	ExternalID string    `json:"external_id"`
}
type SalesBonusRepository interface {
	SalesBonuses(context.Context, string) (BonusDashboard, error)
	BonusEntries(context.Context, string, string, int) ([]BonusEntry, error)
	RecordBonusPayout(context.Context, string, string, BonusPayout) (int64, error)
	SaveBonusSettings(context.Context, string, BonusSettings) (BonusSettings, error)
}
