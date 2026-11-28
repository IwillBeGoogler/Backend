// Package challenge tracks "N days of ..." writing challenges. Progress is
// never stored: it is computed from the linked posts on every read.
package challenge

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

const (
	MinDays   = 7
	MaxDays   = 365
	MaxActive = 3   // running challenges per user
	MinChars  = 300 // shortest post body that counts as a day

	maxTitle       = 120
	maxDescription = 1000

	Public  = "public"
	Private = "private"

	dateLayout  = "2006-01-02"
	clockLayout = "15:04"
)

var (
	ErrTooManyActive = domain.Invalid("at most 3 running challenges")
	ErrEnded         = domain.Invalid("challenge has ended")
	ErrNotOwnPost    = domain.Invalid("post must belong to the challenge owner")
	ErrBadTimezone   = domain.Invalid("timezone must be an IANA zone name")
)

// Challenge is the stored aggregate. Dates are "YYYY-MM-DD" in Timezone.
type Challenge struct {
	ID          int       `json:"id"`
	UserID      int       `json:"user_id"`
	Author      string    `json:"author"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Days        int       `json:"days"`
	Visibility  string    `json:"visibility"`
	Timezone    string    `json:"timezone"`
	StartDate   string    `json:"start_date"`
	DaysDone    int       `json:"days_done"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// PostIDs are all linked posts, drafts too; only on the owner's own list.
	PostIDs []int `json:"post_ids,omitempty"`
	// Reminder settings are private: Detail shows them to the owner only.
	Reminders bool   `json:"-"`
	RemindAt  string `json:"-"` // "HH:MM" in Timezone
}

// Input creates a challenge; Days and Timezone cannot change afterwards.
type Input struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Days        int    `json:"days"`
	Visibility  string `json:"visibility"` // "public" (default) or "private"
	Timezone    string `json:"timezone"`   // IANA name, e.g. "Asia/Tashkent"
	// Update only; left out keeps the current value.
	Reminders *bool  `json:"reminders,omitempty"`
	RemindAt  string `json:"remind_at,omitempty"` // "HH:MM"
}

func (in Input) Normalize() (Input, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Visibility == "" {
		in.Visibility = Public
	}
	switch {
	case in.Title == "":
		return in, domain.Invalid("title required")
	case utf8.RuneCountInString(in.Title) > maxTitle:
		return in, domain.Invalid("title too long")
	case utf8.RuneCountInString(in.Description) > maxDescription:
		return in, domain.Invalid("description too long")
	case in.Days < MinDays || in.Days > MaxDays:
		return in, domain.Invalid("days must be between 7 and 365")
	case in.Visibility != Public && in.Visibility != Private:
		return in, domain.Invalid("visibility must be public or private")
	case in.Timezone == "" || in.Timezone == "Local":
		return in, ErrBadTimezone
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return in, ErrBadTimezone
	}
	if in.RemindAt != "" {
		if _, err := time.Parse(clockLayout, in.RemindAt); err != nil || len(in.RemindAt) != len(clockLayout) {
			return in, domain.Invalid("remind_at must be HH:MM")
		}
	}
	return in, nil
}

// Today is the calendar date at now in the zone (UTC if the zone is unknown).
func Today(timezone string, now time.Time) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return now.In(loc).Format(dateLayout)
}

// Day is a counted day: the first long enough published post of that date.
type Day struct {
	Date   string `json:"date"`
	N      int    `json:"n"` // 1-based day number
	PostID int    `json:"post_id"`
	Title  string `json:"title"`
	Slug   string `json:"slug"`
	Author string `json:"author"`
}

// Stats is the progress on a given date.
type Stats struct {
	DaysDone      int    `json:"days_done"`
	CurrentStreak int    `json:"current_streak"`
	BestStreak    int    `json:"best_streak"`
	Today         int    `json:"today"` // today's day number; 0 outside the challenge
	Ended         bool   `json:"ended"`
	EndDate       string `json:"end_date"` // last day, inclusive
}

// Compute derives the stats from the counted dates. The current streak counts
// back from today, or from yesterday while today is not done yet. Dates outside
// the challenge and duplicates are ignored.
func Compute(start string, days int, done []string, today string) Stats {
	first, err := time.Parse(dateLayout, start)
	if err != nil {
		return Stats{}
	}
	now, err := time.Parse(dateLayout, today)
	if err != nil {
		return Stats{}
	}
	// offsets from the start date; both sides are UTC midnights
	offset := func(t time.Time) int { return int(t.Sub(first) / (24 * time.Hour)) }
	set := map[int]bool{}
	for _, d := range done {
		if t, err := time.Parse(dateLayout, d); err == nil {
			if o := offset(t); o >= 0 && o < days {
				set[o] = true
			}
		}
	}
	t := offset(now)
	s := Stats{DaysDone: len(set), Ended: t >= days, EndDate: first.AddDate(0, 0, days-1).Format(dateLayout)}
	if t >= 0 && t < days {
		s.Today = t + 1
	}
	offsets := make([]int, 0, len(set))
	for o := range set {
		offsets = append(offsets, o)
	}
	slices.Sort(offsets)
	run := 0
	for i, o := range offsets {
		if i > 0 && o == offsets[i-1]+1 {
			run++
		} else {
			run = 1
		}
		s.BestStreak = max(s.BestStreak, run)
	}
	if !set[t] {
		t--
	}
	for ; set[t]; t-- {
		s.CurrentStreak++
	}
	return s
}

// Detail is a challenge with its progress and counted days in order.
type Detail struct {
	Challenge
	Stats        Stats `json:"stats"`
	Entries      []Day `json:"entries"`
	Followers    int   `json:"followers"`
	IsFollowing  bool  `json:"is_following"`
	CheersToday  int   `json:"cheers_today"`
	CheeredToday bool  `json:"cheered_today"`
	// CertificateID is the issued certificate, revoked or not.
	CertificateID string `json:"certificate_id,omitempty"`
	// Owner only.
	Reminders *bool  `json:"reminders,omitempty"`
	RemindAt  string `json:"remind_at,omitempty"`
}

// Cheers is the state of today's cheers after cheering.
type Cheers struct {
	CheersToday int  `json:"cheers_today"`
	Cheered     bool `json:"cheered"`
}

// PostPlace is a counted post's place in its challenge.
type PostPlace struct {
	Challenge Challenge `json:"challenge"`
	Day       int       `json:"day"`
	Of        int       `json:"of"`
}

// Page is one page of challenges.
type Page struct {
	Items      []Challenge
	Page       int
	Limit      int
	Total      int
	NextCursor string
}

func (p Page) Paginated() (any, any) {
	return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, p.NextCursor)
}

// Repository returns domain.ErrNotFound for a missing challenge. "Running"
// means the last day is not over yet at now, in the challenge's zone.
type Repository interface {
	// Create starts the challenge on now's date in in.Timezone; it returns
	// ErrTooManyActive when the user already runs MaxActive.
	Create(ctx context.Context, userID int, in Input, now time.Time) (Challenge, error)
	Get(ctx context.Context, id int) (Challenge, error)
	// Update changes title, description, visibility and the reminder settings
	// (nil Reminders and empty RemindAt keep them).
	Update(ctx context.Context, id int, in Input) (Challenge, error)
	Delete(ctx context.Context, id int) error
	// ListPublicActive lists public running challenges, newest start first.
	ListPublicActive(ctx context.Context, now time.Time, p domain.Paging) (Page, error)
	// ListByUser lists all the user's challenges, newest start first.
	ListByUser(ctx context.Context, userID int, p domain.Paging) (Page, error)
	// Running lists the user's running challenges with PostIDs.
	Running(ctx context.Context, userID int, now time.Time) ([]Challenge, error)
	CountActive(ctx context.Context, userID int, now time.Time) (int, error)
	// Link adds the post; linking it again to the same challenge is a no-op,
	// to another one is domain.ErrConflict.
	Link(ctx context.Context, id, postID int) error
	Unlink(ctx context.Context, id, postID int) error
	// Days lists the counted days in date order.
	Days(ctx context.Context, id int) ([]Day, error)
	// OfPost returns the challenge a post is linked to.
	OfPost(ctx context.Context, postID int) (Challenge, error)

	// Follow and Unfollow are idempotent.
	Follow(ctx context.Context, id, userID int) error
	Unfollow(ctx context.Context, id, userID int) error
	// FollowerIDs and CountFollowers skip deleted accounts.
	FollowerIDs(ctx context.Context, id int) ([]int, error)
	CountFollowers(ctx context.Context, id int) (int, error)
	IsFollowing(ctx context.Context, id, userID int) (bool, error)
	// ListFollowed lists the public challenges the user follows.
	ListFollowed(ctx context.Context, userID int, p domain.Paging) (Page, error)
	// Cheer records one cheer per user per day; a repeat is a no-op.
	Cheer(ctx context.Context, id, userID int, day string) error
	CheersOn(ctx context.Context, id int, day string) (int, error)
	Cheered(ctx context.Context, id, userID int, day string) (bool, error)

	// Claim takes a send-once key; false if it was already taken.
	Claim(ctx context.Context, key string) (bool, error)
	// PurgeSent drops keys older than before, except milestone keys, which
	// must hold for the life of the challenge.
	PurgeSent(ctx context.Context, before time.Time) error
	// ReminderCandidates lists running challenges with reminders on whose
	// owner is not deleted.
	ReminderCandidates(ctx context.Context, now time.Time) ([]Candidate, error)

	// Certificate finds one by id; those of deleted accounts are not found.
	Certificate(ctx context.Context, id string) (Certificate, error)
	CertificateOf(ctx context.Context, challengeID int) (Certificate, error)
	// IssueCertificate stores c unless the challenge already has one; it
	// returns the stored one and whether it is new.
	IssueCertificate(ctx context.Context, challengeID int, c Certificate) (Certificate, bool, error)
	ListCertificates(ctx context.Context, p domain.Paging) (CertificatePage, error)
	// RevokeCertificate returns ErrCertificateRevoked on a repeat.
	RevokeCertificate(ctx context.Context, id, reason string) (Certificate, error)
}
