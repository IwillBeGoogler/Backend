package challenge

import (
	"strings"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	ok := Input{Title: " t ", Days: 7, Timezone: "Asia/Tashkent"}
	got, err := ok.Normalize()
	if err != nil || got.Title != "t" || got.Visibility != Public {
		t.Fatalf("%+v %v", got, err)
	}
	for name, edit := range map[string]func(*Input){
		"no title":    func(i *Input) { i.Title = " " },
		"long title":  func(i *Input) { i.Title = strings.Repeat("a", 121) },
		"long desc":   func(i *Input) { i.Description = strings.Repeat("a", 1001) },
		"few days":    func(i *Input) { i.Days = 6 },
		"many days":   func(i *Input) { i.Days = 366 },
		"visibility":  func(i *Input) { i.Visibility = "friends" },
		"no zone":     func(i *Input) { i.Timezone = "" },
		"local zone":  func(i *Input) { i.Timezone = "Local" },
		"bad zone":    func(i *Input) { i.Timezone = "Mars/Olympus" },
		"path escape": func(i *Input) { i.Timezone = "../etc/passwd" },
	} {
		in := ok
		edit(&in)
		if _, err := in.Normalize(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestToday(t *testing.T) {
	now := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	if got := Today("Asia/Tashkent", now); got != "2026-03-02" {
		t.Errorf("tashkent = %s", got)
	}
	if got := Today("nope", now); got != "2026-03-01" {
		t.Errorf("fallback = %s", got)
	}
}

func TestCompute(t *testing.T) {
	const start, end = "2026-02-25", "2026-03-06"
	cases := []struct {
		name  string
		done  []string
		today string
		want  Stats
	}{
		{"empty", nil, start, Stats{Today: 1, EndDate: end}},
		{"today done, across month end", []string{"2026-02-28", "2026-03-01"}, "2026-03-01",
			Stats{DaysDone: 2, CurrentStreak: 2, BestStreak: 2, Today: 5, EndDate: end}},
		{"yesterday keeps streak", []string{"2026-02-26", "2026-02-27"}, "2026-02-28",
			Stats{DaysDone: 2, CurrentStreak: 2, BestStreak: 2, Today: 4, EndDate: end}},
		{"gap breaks streak", []string{"2026-02-25", "2026-02-26", "2026-02-27"}, "2026-03-01",
			Stats{DaysDone: 3, BestStreak: 3, Today: 5, EndDate: end}},
		{"outside and duplicates ignored", []string{"2026-02-24", "2026-02-25", "2026-02-25", "2026-03-07"}, start,
			Stats{DaysDone: 1, CurrentStreak: 1, BestStreak: 1, Today: 1, EndDate: end}},
		{"last day", nil, end, Stats{Today: 10, EndDate: end}},
		{"day after end", []string{end}, "2026-03-07",
			Stats{DaysDone: 1, CurrentStreak: 1, BestStreak: 1, Ended: true, EndDate: end}},
		{"before start", nil, "2026-02-01", Stats{EndDate: end}},
	}
	for _, c := range cases {
		if got := Compute(start, 10, c.done, c.today); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	if (Compute("x", 10, nil, start) != Stats{}) || (Compute(start, 10, nil, "x") != Stats{}) {
		t.Error("bad dates")
	}
}
