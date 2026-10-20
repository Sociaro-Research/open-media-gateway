package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Stats contains counts only; task IDs and claim tokens never leave Redis.
type Stats struct {
	Pending int64
	Claimed int64
	Expired int64
}

var queueStats = redis.NewScript(clockLua + `return {
 redis.call('LLEN',KEYS[1]), redis.call('ZCARD',KEYS[4]),
 redis.call('ZCOUNT',KEYS[4],'-inf',now)
}`)

func (q *RedisQueue) Stats(ctx context.Context) (Stats, error) {
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}
	// Stats share the pool but use short socket deadlines, even when the queue's
	// normal provider-work commands do not enable context-derived deadlines.
	timeout := 500 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	values, err := queueStats.Run(ctx, q.client.WithTimeout(timeout), q.keys).Slice()
	if err != nil {
		return Stats{}, err
	}
	if len(values) != 3 {
		return Stats{}, fmt.Errorf("invalid queue statistics")
	}
	counts := [3]int64{}
	for i, value := range values {
		count, ok := value.(int64)
		if !ok {
			return Stats{}, fmt.Errorf("invalid queue statistic")
		}
		counts[i] = count
	}
	return Stats{Pending: counts[0], Claimed: counts[1], Expired: counts[2]}, nil
}
