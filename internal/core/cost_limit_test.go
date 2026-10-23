package core

import (
	"context"
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestNegativeCostLimitRejectedBeforeDependencies(t *testing.T) {
	for _, amount := range []money.USD{-1000000, -1} {
		// Nil storage/queue and a missing provider/model ensure early rejection
		// does not depend on admission, provider lookup or cost quotation.
		service := NewTaskService(nil, nil, "unconfigured", "missing")
		service.EnableAccounting()
		input := CreateMediaInput{Prompt: "test", MaxExpectedUSD: &amount}
		if _, err := service.CreateMediaRequest(context.Background(), input, ""); !errors.Is(err, ErrCostLimitInvalid) {
			t.Fatalf("create: %v", err)
		}
		if _, err := service.Estimate(context.Background(), input); !errors.Is(err, ErrCostLimitInvalid) {
			t.Fatalf("estimate: %v", err)
		}
	}
}
