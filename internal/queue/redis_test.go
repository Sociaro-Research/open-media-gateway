package queue

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func testQueue(t *testing.T) *RedisQueue {
	t.Helper()
	url := os.Getenv("GATEWAY_TEST_REDIS_URL")
	if url == "" {
		t.Skip("requires isolated Redis")
	}
	q, err := Open(context.Background(), url, "test-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.client.Del(context.Background(), q.keys...); q.Close() })
	return q
}
func TestOwnedLeaseRecoveryAndFencing(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	a := core.WithClaimToken(ctx, uuid.NewString())
	b := core.WithClaimToken(ctx, uuid.NewString())
	if err := q.Enqueue(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if id, err := q.Dequeue(a); err != nil || id != "one" {
		t.Fatal(id, err)
	}
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if n := q.client.LLen(ctx, q.keys[0]).Val(); n != 0 {
		t.Fatal("live work stolen on startup")
	}
	if err := q.Renew(a, "one"); err != nil {
		t.Fatal(err)
	}
	q.client.ZAdd(ctx, q.keys[3], redis.Z{Score: 0, Member: "one"})
	if err := q.Renew(a, "one"); !errors.Is(err, core.ErrLeaseLost) {
		t.Fatal("expired lease renewed", err)
	}
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if id, err := q.Dequeue(b); err != nil || id != "one" {
		t.Fatal(id, err)
	}
	for _, action := range []func(context.Context, string) error{q.Ack, q.Requeue, q.Renew} {
		if err := action(a, "one"); !errors.Is(err, core.ErrLeaseLost) {
			t.Fatal("stale consumer changed successor", err)
		}
	}
	if err := q.Ack(b, "one"); err != nil {
		t.Fatal(err)
	}
	if q.client.SCard(ctx, q.keys[1]).Val() != 0 {
		t.Fatal("completed ID retained")
	}
}
func TestParallelEnqueueDeduplicatesAndRecoveryIsBounded(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 150; j++ {
				id := string(rune(j + 1000))
				if err := q.Enqueue(ctx, id); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if q.client.LLen(ctx, q.keys[0]).Val() != 150 {
		t.Fatal("duplicate queue entries")
	}
	claim := core.WithClaimToken(ctx, uuid.NewString())
	for i := 0; i < 150; i++ {
		id, err := q.Dequeue(claim)
		if err != nil {
			t.Fatal(err)
		}
		q.client.ZAdd(ctx, q.keys[3], redis.Z{Score: 0, Member: id})
	}
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if q.client.LLen(ctx, q.keys[0]).Val() != 100 {
		t.Fatal("unbounded recovery")
	}
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if q.client.LLen(ctx, q.keys[0]).Val() != 150 {
		t.Fatal("expired work stranded")
	}
}

func TestFailedRequeueRecoversWithoutProcessRestart(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	claim := core.WithClaimToken(ctx, uuid.NewString())
	if err := q.Enqueue(ctx, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Dequeue(claim); err != nil {
		t.Fatal(err)
	}
	// Close this worker's Redis transport: returning work now fails.
	options := q.client.Options()
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.Requeue(claim, "interrupted"); err == nil {
		t.Fatal("expected disconnected requeue")
	}
	recovered := &RedisQueue{client: redis.NewClient(options), keys: q.keys}
	defer recovered.Close()
	defer recovered.client.Del(ctx, q.keys...)
	recovered.client.ZAdd(ctx, q.keys[3], redis.Z{Score: 0, Member: "interrupted"})
	if err := recovered.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	next := core.WithClaimToken(ctx, uuid.NewString())
	if id, err := recovered.Dequeue(next); err != nil || id != "interrupted" {
		t.Fatal("stranded task", id, err)
	}
	if err := recovered.Ack(next, "interrupted"); err != nil {
		t.Fatal(err)
	}
}
