Rate limiting protects a service from abuse and from its own clients' retry storms. The algorithm matters less than where the state lives and what happens when that state is unavailable.

## Comparing algorithms

| Algorithm | Memory per key | Bursts | Accuracy |
|---|---|---|---|
| Fixed window | Very low | Up to 2x at the boundary | Low |
| Sliding log | High | None | Exact |
| Sliding window counter | Low | Small | Good |
| Token bucket | Low | Configurable | Good |

Token bucket is the usual default: the capacity sets the allowed burst and the refill rate sets the sustained rate.

## Atomic token bucket in Redis

A read followed by a write races under concurrency, so run the whole step as a Lua script:

```lua
local tokens = tonumber(redis.call('HGET', KEYS[1], 't') or ARGV[1])
local last   = tonumber(redis.call('HGET', KEYS[1], 'ts') or ARGV[3])
tokens = math.min(ARGV[1], tokens + (ARGV[3] - last) * ARGV[2])
local allowed = tokens >= 1 and 1 or 0
if allowed == 1 then tokens = tokens - 1 end
redis.call('HSET', KEYS[1], 't', tokens, 'ts', ARGV[3])
redis.call('EXPIRE', KEYS[1], 3600)
return allowed
```

Take the timestamp from `redis.call('TIME')` if your app servers' clocks may drift.

## Fail open or fail closed?

If Redis is down, a limiter on a public read endpoint should **fail open**: a short period without limits is better than an outage you caused yourself. A limiter on login or password reset should **fail closed**, because it is a security control.

## Tell clients what happened

Return `429` with `Retry-After`. Without it, clients guess, and they usually guess too soon. Well-behaved clients add jitter to their backoff, so a recovering service does not get hit by every client at the same moment.
