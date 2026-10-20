package queue

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/redis/go-redis/v9"
)

// A set replaces list scans. IDs remain known while pending or claimed.
var enqueueUnique = redis.NewScript(`
if redis.call('SADD', KEYS[2], ARGV[1]) == 0 then return 0 end
redis.call('LPUSH', KEYS[1], ARGV[1]); return 1
`)

const clockLua = `local t=redis.call('TIME'); local now=t[1]*1000+math.floor(t[2]/1000); `

var claimNext = redis.NewScript(clockLua + `
local id=redis.call('RPOP',KEYS[1]); if not id then return '' end
redis.call('HSET',KEYS[3],id,ARGV[1]);redis.call('ZADD',KEYS[4],now+ARGV[2],id);return id
`)
var finishClaim = redis.NewScript(clockLua + `
if redis.call('HGET',KEYS[3],ARGV[1])~=ARGV[2] then return 0 end
local untilTime=redis.call('ZSCORE',KEYS[4],ARGV[1]);if not untilTime or tonumber(untilTime)<=now then return 0 end
if ARGV[3]=='renew' then redis.call('ZADD',KEYS[4],now+ARGV[4],ARGV[1]);return 1 end
redis.call('HDEL',KEYS[3],ARGV[1]);redis.call('ZREM',KEYS[4],ARGV[1])
if ARGV[3]=='retry' then redis.call('LPUSH',KEYS[1],ARGV[1]) else redis.call('SREM',KEYS[2],ARGV[1]) end
return 1
`)
var recoverExpired = redis.NewScript(clockLua + `
local ids=redis.call('ZRANGEBYSCORE',KEYS[4],'-inf',now,'LIMIT',0,100)
for _,id in ipairs(ids) do
 redis.call('ZREM',KEYS[4],id);redis.call('HDEL',KEYS[3],id);redis.call('LPUSH',KEYS[1],id)
end
return #ids
`)

type RedisQueue struct {
	client *redis.Client
	keys   []string
}

func Open(ctx context.Context, redisURL, name string) (*RedisQueue, error) {
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(name) {
		return nil, fmt.Errorf("invalid queue name %q", name)
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	// Versioned keys permit recovery from PostgreSQL without trusting legacy
	// processing entries. Stop all old workers when deploying this protocol.
	base := "media-gateway:{" + name + "}:v2:"
	return &RedisQueue{client: client, keys: []string{base + "pending", base + "known", base + "owners", base + "leases"}}, nil
}
func (q *RedisQueue) Close() error                   { return q.client.Close() }
func (q *RedisQueue) Ping(ctx context.Context) error { return q.client.Ping(ctx).Err() }
func (q *RedisQueue) Enqueue(ctx context.Context, id string) error {
	return enqueueUnique.Run(ctx, q.client, q.keys, id).Err()
}
func (q *RedisQueue) Dequeue(ctx context.Context) (string, error) {
	token := core.ClaimToken(ctx)
	if token == "" {
		return "", core.ErrLeaseLost
	}
	// Short cancellable polls avoid one blocked Redis connection per execution
	// slot. Work and reconciliation run independently.
	until := time.Now().Add(time.Second)
	for {
		id, err := claimNext.Run(ctx, q.client, q.keys, token, core.WorkLeaseDuration.Milliseconds()).Text()
		if err != nil {
			return "", err
		}
		if id != "" {
			return id, nil
		}
		if time.Now().After(until) {
			return "", core.ErrQueueEmpty
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (q *RedisQueue) finish(ctx context.Context, id, action string) error {
	if core.ClaimToken(ctx) == "" {
		return core.ErrLeaseLost
	}
	n, err := finishClaim.Run(ctx, q.client, q.keys, id, core.ClaimToken(ctx), action, core.WorkLeaseDuration.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return core.ErrLeaseLost
	}
	return nil
}
func (q *RedisQueue) Ack(ctx context.Context, id string) error     { return q.finish(ctx, id, "ack") }
func (q *RedisQueue) Requeue(ctx context.Context, id string) error { return q.finish(ctx, id, "retry") }
func (q *RedisQueue) Renew(ctx context.Context, id string) error   { return q.finish(ctx, id, "renew") }
func (q *RedisQueue) Recover(ctx context.Context) error {
	return recoverExpired.Run(ctx, q.client, q.keys).Err()
}

// Reconciliation uses one network round trip per batch, with the same atomic
// deduplication as immediate admission. A partial failure is safe to retry.
func (q *RedisQueue) EnqueueBatch(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.client.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, id := range ids {
			p.Eval(ctx, `if redis.call('SADD',KEYS[2],ARGV[1])==0 then return 0 end;redis.call('LPUSH',KEYS[1],ARGV[1]);return 1`, q.keys, id)
		}
		return nil
	})
	return err
}
