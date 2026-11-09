package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// DiagramViews counts unique viewers per published diagram with
// HyperLogLog, apart from the posts' keys, and keeps one sorted set per day
// of first-time views for the daily series.
type DiagramViews struct{ RDB *redis.Client }

func diagramViewsKey(fileID string) string { return "views:diagram:" + fileID }
func diagramDayKey(t time.Time) string     { return "dviews:" + t.UTC().Format("20060102") }

func (v DiagramViews) Hit(ctx context.Context, fileID, viewer string) error {
	added, err := v.RDB.PFAdd(ctx, diagramViewsKey(fileID), viewer).Result()
	if err != nil || added == 0 {
		return err
	}
	key := diagramDayKey(time.Now())
	pipe := v.RDB.TxPipeline()
	pipe.ZIncrBy(ctx, key, 1, fileID)
	pipe.Expire(ctx, key, trendingTTL)
	_, err = pipe.Exec(ctx)
	return err
}

func (v DiagramViews) Count(ctx context.Context, fileID string) int64 {
	n, _ := v.RDB.PFCount(ctx, diagramViewsKey(fileID)).Result()
	return n
}

// Counts returns unique views for many diagrams in one round trip.
func (v DiagramViews) Counts(ctx context.Context, fileIDs []string) map[string]int64 {
	pipe := v.RDB.Pipeline()
	cmds := make([]*redis.IntCmd, len(fileIDs))
	for i, id := range fileIDs {
		cmds[i] = pipe.PFCount(ctx, diagramViewsKey(id))
	}
	pipe.Exec(ctx) // per-command errors read below; missing keys count 0
	out := make(map[string]int64, len(fileIDs))
	for i, c := range cmds {
		out[fileIDs[i]], _ = c.Result()
	}
	return out
}

// DailyTotals returns the first-time unique views of all diagrams on each day.
func (v DiagramViews) DailyTotals(ctx context.Context, days []time.Time) ([]int64, error) {
	return dailyTotals(ctx, v.RDB, diagramDayKey, days)
}
