package challenge

import (
	"testing"
	"time"
)

func TestNudgeKind(t *testing.T) {
	const start = "2026-03-01"
	for _, tc := range []struct {
		name, today string
		done        []string
		want        string
	}{
		{"before start", "2026-02-28", nil, ""},
		{"first day", start, nil, NudgeEvening},
		{"posted today", "2026-03-05", []string{"2026-03-05"}, ""},
		{"posted yesterday", "2026-03-05", []string{"2026-03-04"}, NudgeEvening},
		{"streak broken", "2026-03-05", []string{"2026-03-03"}, NudgeBroken},
		{"never posted, one missed", "2026-03-02", nil, NudgeEvening},
		{"two missed", "2026-03-05", []string{"2026-03-02"}, NudgeEvening},
		{"three missed", "2026-03-05", []string{"2026-03-01"}, NudgeSilent},
		{"never posted, three missed", "2026-03-04", nil, NudgeSilent},
		{"six missed", "2026-03-10", []string{"2026-03-03"}, NudgeSilent},
		{"seven missed", "2026-03-11", []string{"2026-03-03"}, ""},
		{"long silence", "2026-04-11", nil, ""},
		{"back after silence", "2026-04-11", []string{"2026-04-10"}, NudgeEvening},
		{"bad date", "x", nil, ""},
	} {
		if got := NudgeKind(tc.today, start, tc.done); got != tc.want {
			t.Errorf("%s: NudgeKind = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCandidateDue(t *testing.T) {
	// 15:00 UTC is 20:00 in Tashkent.
	now := time.Date(2026, 3, 1, 15, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		zone, at string
		want     bool
	}{
		{"Asia/Tashkent", "20:00", true},
		{"Asia/Tashkent", "20:01", false},
		{"UTC", "20:00", false},
	} {
		if got := (Candidate{Timezone: tc.zone, RemindAt: tc.at}).Due(now); got != tc.want {
			t.Errorf("Due(%s %s) = %v", tc.zone, tc.at, got)
		}
	}
	late := Candidate{Timezone: "UTC", RemindAt: "23:59"}
	if !late.Due(time.Date(2026, 3, 1, 23, 50, 0, 0, time.UTC)) {
		t.Error("late reminder time not capped")
	}
}

func TestVariant(t *testing.T) {
	for _, d := range []string{"2026-03-01", "2026-03-02", ""} {
		if v := Variant(d); v < 0 || v >= Variants || v != Variant(d) {
			t.Errorf("Variant(%q) = %d", d, v)
		}
	}
}

func TestRemindAtValidation(t *testing.T) {
	in := Input{Title: "x", Days: 7, Timezone: "UTC"}
	for at, ok := range map[string]bool{"": true, "00:00": true, "23:59": true, "9:00": false, "24:00": false, "x": false} {
		in.RemindAt = at
		if _, err := in.Normalize(); (err == nil) != ok {
			t.Errorf("remind_at %q: err = %v", at, err)
		}
	}
}
