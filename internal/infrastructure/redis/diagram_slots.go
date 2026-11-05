package redis

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RTCSlots counts the participants of diagram audio rooms across API
// instances: one sorted set per file, a member per connection scored by when
// its place lapses. An instance refreshes its connections' places while they
// are in the room, so a crashed instance's places free themselves.
type RTCSlots struct {
	RDB *redis.Client
	// TTL is how long a place lasts without a refresh (default 90 s).
	TTL time.Duration
}

func (s RTCSlots) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return 90 * time.Second
}

func rtcKey(fileID string) string { return "diagram:rtc:" + fileID }

// acquire: [KEYS 1] room; ARGV now(ms), expiry(ms), max, member.
var acquire = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if redis.call('ZSCORE', KEYS[1], ARGV[4]) == false and redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[3]) then
  return 0
end
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[4])
redis.call('PEXPIRE', KEYS[1], ARGV[5])
return 1`)

func (s RTCSlots) Acquire(ctx context.Context, fileID, clientID string, max int) (bool, error) {
	now := time.Now()
	n, err := acquire.Run(ctx, s.RDB, []string{rtcKey(fileID)},
		now.UnixMilli(), now.Add(s.ttl()).UnixMilli(), max, clientID, s.ttl().Milliseconds()).Int()
	return n == 1, err
}

func (s RTCSlots) Release(ctx context.Context, fileID, clientID string) error {
	return s.RDB.ZRem(ctx, rtcKey(fileID), clientID).Err()
}

// Members lists the clients in a file's room whose place has not lapsed.
func (s RTCSlots) Members(ctx context.Context, fileID string) ([]string, error) {
	return s.RDB.ZRangeByScore(ctx, rtcKey(fileID), &redis.ZRangeBy{Min: strconv.FormatInt(time.Now().UnixMilli(), 10), Max: "+inf"}).Result()
}
