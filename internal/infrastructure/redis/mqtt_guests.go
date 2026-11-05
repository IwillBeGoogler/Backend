package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
)

// MQTTGuests keeps the share token of a guest's broker connection by client
// id, so the API instance that answers the broker's access question need not
// be the one that answered its login.
type MQTTGuests struct{ RDB *redis.Client }

func guestKey(clientID string) string { return "diagram:mqtt:guest:" + clientID }

func (g MQTTGuests) Put(ctx context.Context, clientID, token string, ttl time.Duration) error {
	return g.RDB.Set(ctx, guestKey(clientID), token, ttl).Err()
}

func (g MQTTGuests) Get(ctx context.Context, clientID string) (string, error) {
	v, err := g.RDB.Get(ctx, guestKey(clientID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", domain.ErrNotFound
	}
	return v, err
}

// MQTTClients keeps sets of broker client ids by key (a share link or a
// user in a project), so a revoked link or lowered member can be cut off
// from any API instance.
type MQTTClients struct{ RDB *redis.Client }

func clientsKey(key string) string { return "diagram:mqtt:clients:" + key }

func (c MQTTClients) Add(ctx context.Context, key, clientID string, ttl time.Duration) error {
	k := clientsKey(key)
	pipe := c.RDB.Pipeline()
	pipe.SAdd(ctx, k, clientID)
	pipe.Expire(ctx, k, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// Take returns the client ids under key and forgets them.
func (c MQTTClients) Take(ctx context.Context, key string) ([]string, error) {
	k := clientsKey(key)
	pipe := c.RDB.TxPipeline()
	members := pipe.SMembers(ctx, k)
	pipe.Del(ctx, k)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	return members.Val(), nil
}
