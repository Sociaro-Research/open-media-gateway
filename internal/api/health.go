package api

import (
	"context"
	"fmt"
)

type Pinger interface {
	PingContext(context.Context) error
}

type RedisPinger interface {
	Ping(context.Context) error
}

type DependenciesHealth struct {
	database Pinger
	redis    RedisPinger
}

func NewDependenciesHealth(database Pinger, redis RedisPinger) *DependenciesHealth {
	return &DependenciesHealth{database: database, redis: redis}
}

func (h *DependenciesHealth) Check(ctx context.Context) error {
	if err := h.database.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if err := h.redis.Ping(ctx); err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	return nil
}
