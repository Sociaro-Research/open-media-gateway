package system

import (
	"context"
	"errors"
)

var ErrSalesPlacement = errors.New("choose a supervisor outside this employee's branch with a strictly higher sales role, or Administrator")

const (
	ClientManager         = "client_manager"
	LeadClientManager     = "lead_client_manager"
	RegionalClientManager = "regional_client_manager"
)

func SalesRank(role string) int {
	switch role {
	case ClientManager:
		return 1
	case LeadClientManager:
		return 2
	case RegionalClientManager:
		return 3
	default:
		return 0
	}
}

type SalesMember struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Picture      string `json:"picture,omitempty"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	SupervisorID string `json:"supervisor_id"`
	OwnClients   int    `json:"own_clients"`
}

type SalesStructure struct {
	Version int64         `json:"version"`
	Members []SalesMember `json:"members"`
}

type SalesPlacement struct {
	FillVacancy  bool   `json:"fill_vacancy,omitempty"`
	NewTeam      bool   `json:"new_team,omitempty"`
	Role         string `json:"role"`
	SupervisorID string `json:"supervisor_id"`
	Version      int64  `json:"version"`
}

type SalesRepository interface {
	SalesStructure(context.Context, string) (SalesStructure, error)
	PlaceSalesMember(context.Context, string, string, SalesPlacement) (SalesStructure, error)
	SalesTeam(context.Context, string) ([]SalesMember, error)
	SalesRole(context.Context, string) (string, error)
}
