package queue

import (
	"context"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/limits"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"time"
)

// One atomic decision across all windows and identities. Denials consume no
// tokens. Redis time avoids clock skew across gateway replicas. Sorted sets
// implement rolling windows, including bursts at minute boundaries.
var takeQuota = redis.NewScript(clockLua + `
local retry=0;local rejected=0
for i,key in ipairs(KEYS) do
 local window=tonumber(ARGV[2*i]);local maximum=tonumber(ARGV[2*i+1])
 redis.call('ZREMRANGEBYSCORE',key,'-inf',now-window)
 local count=redis.call('ZCARD',key)
 if count>=maximum then
  local first=redis.call('ZRANGE',key,count-maximum,count-maximum,'WITHSCORES')
  local wait=tonumber(first[2])+window-now
  if wait>retry then retry=wait;rejected=i end
 end
end
if rejected>0 then return {rejected,retry} end
for i,key in ipairs(KEYS) do
 redis.call('ZADD',key,now,ARGV[1]);redis.call('PEXPIRE',key,ARGV[2*i])
end
return {0,0}
`)

func (q *RedisQueue) Take(ctx context.Context, b []limits.Bucket) error {
	if len(b) == 0 {
		return nil
	}
	keys := make([]string, len(b))
	args := []any{uuid.NewString()}
	for i, bucket := range b {
		if bucket.Count < 1 || bucket.Window <= 0 {
			return fmt.Errorf("invalid quota configuration")
		}
		keys[i] = q.keys[0] + ":quota:" + bucket.ID
		args = append(args, bucket.Window.Milliseconds(), bucket.Count)
	}
	result, err := takeQuota.Run(ctx, q.client, keys, args...).Int64Slice()
	if err != nil {
		return err
	}
	if result[0] > 0 {
		return &limits.Exceeded{Name: b[result[0]-1].Name, RetryAfter: time.Duration(result[1]) * time.Millisecond}
	}
	return nil
}
