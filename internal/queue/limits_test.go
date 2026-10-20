package queue

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/limits"
	"github.com/redis/go-redis/v9"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuotaAtomicAcrossKeysAndReplicas(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	// A second client of the same logical queue represents another API replica.
	other := &RedisQueue{client: redis.NewClient(q.client.Options()), keys: q.keys}
	defer other.Close()
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := q
			if i%2 == 1 {
				client = other
			}
			err := client.Take(ctx, []limits.Bucket{{ID: "shared-user", Name: "user", Count: 17, Window: time.Minute}, {ID: "key-" + string(rune('a'+i%10)), Name: "key", Count: 10, Window: time.Minute}})
			if err == nil {
				allowed.Add(1)
			} else {
				var e *limits.Exceeded
				if !errors.As(err, &e) || e.RetryAfter <= 0 {
					t.Errorf("unexpected denial: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if allowed.Load() != 17 {
		t.Fatalf("user quota bypassed through keys/replicas: accepted %d", allowed.Load())
	}
	var total int64
	for i := 0; i < 10; i++ {
		total += q.client.ZCard(ctx, q.keys[0]+":quota:key-"+string(rune('a'+i))).Val()
	}
	if total != 17 {
		t.Fatalf("denials consumed key tokens: %d", total)
	}
	if err := q.Take(ctx, []limits.Bucket{{ID: "other-org:user", Name: "user", Count: 1, Window: time.Minute}}); err != nil {
		t.Fatal("independent organization blocked", err)
	}
}
func TestQuotaRollingWindowAndLoweredLimit(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	id := q.keys[0] + ":quota:window"
	now := q.client.Time(ctx).Val().UnixMilli()
	// Three admissions near the previous wall-clock boundary. A lower limit
	// must wait for enough entries to expire, not just the oldest one.
	q.client.ZAdd(ctx, id, redis.Z{Score: float64(now - 59000), Member: "a"}, redis.Z{Score: float64(now - 10000), Member: "b"}, redis.Z{Score: float64(now - 5000), Member: "c"})
	b := []limits.Bucket{{ID: "window", Name: "read", Count: 2, Window: time.Minute}}
	var e *limits.Exceeded
	if err := q.Take(ctx, b); !errors.As(err, &e) || e.RetryAfter < 45*time.Second {
		t.Fatalf("incorrect retry after lowering quota: %v", err)
	}
	q.client.ZAdd(ctx, id, redis.Z{Score: float64(now - 61000), Member: "a"}, redis.Z{Score: float64(now - 60001), Member: "b"})
	if err := q.Take(ctx, b); err != nil {
		t.Fatal("expired requests retained", err)
	}
	if ttl := q.client.PTTL(ctx, id).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatal("counter has no bounded lifetime", ttl)
	}
}
