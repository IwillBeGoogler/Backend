package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/redis"
)

func TestDiagramViewsAndDailyTotals(t *testing.T) {
	rdb := connect(t)
	ctx := context.Background()
	dv := redis.DiagramViews{RDB: rdb}

	for _, v := range []string{"a", "b", "a"} {
		if err := dv.Hit(ctx, "f1", v); err != nil {
			t.Fatal(err)
		}
	}
	if err := dv.Hit(ctx, "f2", "a"); err != nil {
		t.Fatal(err)
	}
	if n := dv.Count(ctx, "f1"); n != 2 {
		t.Errorf("count = %d", n)
	}
	got := dv.Counts(ctx, []string{"f1", "f2", "none"})
	if got["f1"] != 2 || got["f2"] != 1 || got["none"] != 0 {
		t.Errorf("counts = %v", got)
	}
	days := []time.Time{time.Now().AddDate(0, 0, -1), time.Now()}
	tot, err := dv.DailyTotals(ctx, days)
	if err != nil || len(tot) != 2 || tot[0] != 0 || tot[1] != 3 {
		t.Errorf("diagram totals = %v, %v", tot, err)
	}
	if _, err := (redis.Views{RDB: rdb}).DailyTotals(ctx, days); err != nil {
		t.Errorf("views totals: %v", err)
	}
}

func TestDiagramViewsDead(t *testing.T) {
	ctx := context.Background()
	d := deadClient()
	defer d.Close()
	dv := redis.DiagramViews{RDB: d}
	if err := dv.Hit(ctx, "f", "v"); err == nil {
		t.Error("hit")
	}
	if n := dv.Count(ctx, "f"); n != 0 {
		t.Error("count")
	}
	if m := dv.Counts(ctx, []string{"f"}); m["f"] != 0 {
		t.Error("counts")
	}
	days := []time.Time{time.Now()}
	if _, err := dv.DailyTotals(ctx, days); err == nil {
		t.Error("diagram totals")
	}
	if _, err := (redis.Views{RDB: d}).DailyTotals(ctx, days); err == nil {
		t.Error("views totals")
	}
	if _, err := (redis.ResetTokens{RDB: d}).Issue(ctx, 1, time.Minute); err == nil {
		t.Error("issue")
	}
}
