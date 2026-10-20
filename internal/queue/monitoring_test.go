package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestMonitoringStatsPreserveQueueAndIncludeExpiredClaims(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	for _, id := range []string{"one", "two", "three"} {
		if err := q.Enqueue(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := q.Dequeue(core.WithClaimToken(ctx, uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.client.ZAdd(ctx, q.keys[3], redis.Z{Score: 0, Member: "one"}).Err(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		stats, err := q.Stats(ctx)
		if err != nil || stats != (Stats{Pending: 1, Claimed: 2, Expired: 1}) {
			t.Fatalf("queue snapshot changed work: %+v %v", stats, err)
		}
	}
	if q.client.SCard(ctx, q.keys[1]).Val() != 3 || q.client.HLen(ctx, q.keys[2]).Val() != 2 {
		t.Fatal("snapshot mutated queue ownership")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := q.Stats(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled monitoring read: %v", err)
	}
}
