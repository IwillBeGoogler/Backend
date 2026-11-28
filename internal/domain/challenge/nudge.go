package challenge

import (
	"hash/fnv"
	"slices"
	"time"
)

// Nudge kinds.
const (
	NudgeEvening = "evening" // today is not done yet
	NudgeBroken  = "broken"  // yesterday broke a streak
	NudgeSilent  = "silent"  // several days without a post

	silentAfter = 3 // missed days before the nudge turns to "silent"
	giveUpAfter = 7 // missed days after which reminders stop until a post

	// NudgeWindow is how many days back a Candidate's counted dates must
	// reach to judge giveUpAfter missed days.
	NudgeWindow = giveUpAfter + 1
	// Variants is how many wordings each message has.
	Variants = 4
)

// Milestones are the counted-day totals worth announcing.
var Milestones = []int{7, 30, 50, 100}

// Candidate is a challenge that may be due a reminder. Done holds at least
// the counted dates of the last NudgeWindow days.
type Candidate struct {
	ChallengeID int
	UserID      int
	Timezone    string
	RemindAt    string // "HH:MM"
	StartDate   string
	Done        []string
}

// Due reports whether the reminder time has passed in the challenge's zone.
// Times late in the day are capped so that a periodic check cannot miss them
// before midnight.
func (c Candidate) Due(now time.Time) bool {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return now.In(loc).Format(clockLayout) >= min(c.RemindAt, "23:50")
}

// NudgeKind picks the reminder for today, or "" for none.
func NudgeKind(today, start string, done []string) string {
	now, err := time.Parse(dateLayout, today)
	if err != nil || today < start || slices.Contains(done, today) {
		return ""
	}
	first, err := time.Parse(dateLayout, start)
	if err != nil {
		return ""
	}
	last := ""
	for _, d := range done {
		if d >= start && d < today && d > last {
			last = d
		}
	}
	// missed counts the whole days without a post before today.
	missed := int(now.Sub(first) / (24 * time.Hour))
	if t, err := time.Parse(dateLayout, last); err == nil {
		missed = int(now.Sub(t)/(24*time.Hour)) - 1
	}
	switch {
	case missed >= giveUpAfter:
		return ""
	case missed >= silentAfter:
		return NudgeSilent
	case missed == 1 && last != "":
		return NudgeBroken
	}
	return NudgeEvening
}

// Variant picks a message wording from the date: stable within a day.
func Variant(date string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(date))
	return int(h.Sum32() % Variants)
}
