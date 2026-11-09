package redis_test

import (
	"context"
	"errors"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/redis"
)

func deadClient() *goredis.Client {
	return goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
}

func TestMQTTGuestsAndClients(t *testing.T) {
	rdb := connect(t)
	ctx := context.Background()

	g := redis.MQTTGuests{RDB: rdb}
	if _, err := g.Get(ctx, "c1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing guest: %v", err)
	}
	if err := g.Put(ctx, "c1", "tok", time.Minute); err != nil {
		t.Fatal(err)
	}
	if v, err := g.Get(ctx, "c1"); err != nil || v != "tok" {
		t.Errorf("guest = %q, %v", v, err)
	}

	c := redis.MQTTClients{RDB: rdb}
	for _, id := range []string{"a", "b", "a"} {
		if err := c.Add(ctx, "share:1", id, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := c.Take(ctx, "share:1")
	if err != nil || len(ids) != 2 {
		t.Fatalf("take = %v, %v", ids, err)
	}
	if ids, err := c.Take(ctx, "share:1"); err != nil || len(ids) != 0 {
		t.Errorf("second take = %v, %v", ids, err)
	}
}

func TestRedisErrorPaths(t *testing.T) {
	ctx := context.Background()
	if _, err := redis.Connect(ctx, "redis://127.0.0.1:1"); err == nil {
		t.Error("connect to a dead server succeeded")
	}
	d := deadClient()
	defer d.Close()

	if err := (redis.MQTTGuests{RDB: d}).Put(ctx, "c", "t", time.Minute); err == nil {
		t.Error("guest put")
	}
	if _, err := (redis.MQTTGuests{RDB: d}).Get(ctx, "c"); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("guest get: %v", err)
	}
	if err := (redis.MQTTClients{RDB: d}).Add(ctx, "k", "c", time.Minute); err == nil {
		t.Error("clients add")
	}
	if _, err := (redis.MQTTClients{RDB: d}).Take(ctx, "k"); err == nil {
		t.Error("clients take")
	}
	if _, err := (redis.ResetTokens{RDB: d}).Issue(ctx, 1, time.Minute); err == nil {
		t.Error("reset issue")
	}
	if _, err := (redis.ResetTokens{RDB: d}).Consume(ctx, "x"); err == nil || err.Error() == "token not found" {
		t.Errorf("reset consume: %v", err)
	}
	m := redis.MFAChallenges{RDB: d}
	if _, err := m.Issue(ctx, 1, time.Minute); err == nil {
		t.Error("mfa issue")
	}
	if _, err := m.Attempt(ctx, "x"); err == nil {
		t.Error("mfa attempt")
	}
	if err := m.Consume(ctx, "x"); err == nil {
		t.Error("mfa consume")
	}
	if _, err := (redis.Events{RDB: d}).Subscribe(ctx, "ch"); err == nil {
		t.Error("subscribe")
	}
	v := redis.Views{RDB: d}
	if _, err := v.Trending(ctx, 3, 5); err == nil {
		t.Error("trending")
	}
	if _, err := v.Daily(ctx, 1, []time.Time{time.Now()}); err == nil {
		t.Error("daily")
	}
}

func TestResetTokenBadValue(t *testing.T) {
	rdb := connect(t)
	ctx := context.Background()
	r := redis.ResetTokens{RDB: rdb}
	tok, err := r.Issue(ctx, 7, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Overwrite the stored value with a non-number: Consume surfaces the parse error.
	sum := redis.ResetTokens{RDB: rdb}
	_ = sum
	keys, _ := rdb.Keys(ctx, "pwreset:*").Result()
	if len(keys) != 1 {
		t.Fatalf("keys %v", keys)
	}
	rdb.Set(ctx, keys[0], "abc", time.Minute)
	if _, err := r.Consume(ctx, tok); err == nil {
		t.Error("non-numeric value accepted")
	}
}

func TestEventsSubscribeStops(t *testing.T) {
	rdb := connect(t)
	ctx, cancel := context.WithCancel(context.Background())
	e := redis.Events{RDB: rdb}
	ch, err := e.Subscribe(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	// slow consumer: overflow the 16-slot buffer without reading
	for i := 0; i < 40; i++ {
		if err := e.Publish(context.Background(), "t", i); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if err := e.Publish(context.Background(), "t", make(chan int)); err == nil {
		t.Error("unmarshalable value accepted")
	}
	cancel()
	for range ch { // drains and ends once ctx is cancelled
	}
}
