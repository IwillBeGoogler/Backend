package application

import (
	"context"
	"time"
)

// Admin insights: one report with posts, users, diagrams and moderation
// sections for the last N days. Needs stats.read.

// InsightsDefaultDays is the window when the caller names none.
const InsightsDefaultDays = 30

// NamedCount is one labelled bucket of a breakdown.
type NamedCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// InsightPost is a published post in a top list.
type InsightPost struct {
	ID       int    `json:"id"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Likes    int    `json:"likes"`
	Comments int    `json:"comments"`
}

// PostInsights covers posts and their comments.
type PostInsights struct {
	Total      int           `json:"total"`
	Published  int           `json:"published"`
	Drafts     int           `json:"drafts"`
	Likes      int           `json:"likes"`
	Comments   int           `json:"comments"`
	Created    []DayCount    `json:"created"`           // posts created per day
	PerDayPub  []DayCount    `json:"published_per_day"` // posts published per day
	NewComment []DayCount    `json:"comments_per_day"`
	ByCategory []NamedCount  `json:"by_category"`
	TopByLikes []InsightPost `json:"top_liked"`
	TopByTalk  []InsightPost `json:"top_commented"`
}

// UserInsights covers accounts.
type UserInsights struct {
	Total      int          `json:"total"`
	New        int          `json:"new"` // signed up in the window
	Verified   int          `json:"verified"`
	Sanctioned int          `json:"sanctioned"`
	Signups    []DayCount   `json:"signups"`
	TopAuthors []NamedCount `json:"top_authors"` // published posts per author
}

// DiagramInsights covers diagram projects and files.
type DiagramInsights struct {
	Projects     int          `json:"projects"`
	Files        int          `json:"files"`
	Public       int          `json:"public"`
	Templates    int          `json:"templates"`
	Created      []DayCount   `json:"created"` // files created per day
	ByKind       []NamedCount `json:"by_kind"`
	ByVisibility []NamedCount `json:"by_visibility"`
	TopOwners    []NamedCount `json:"top_owners"` // files per owner
}

// ModerationInsights covers reports, feedback and client errors.
type ModerationInsights struct {
	OpenReports     int          `json:"open_reports"`
	OpenFeedback    int          `json:"open_feedback"`
	OpenErrors      int          `json:"open_errors"`
	AvgResolveHours float64      `json:"avg_resolve_hours"` // reports resolved in the window
	Opened          []DayCount   `json:"opened"`            // reports opened per day
	Resolved        []DayCount   `json:"resolved"`          // reports resolved or dismissed per day
	ByReason        []NamedCount `json:"by_reason"`
	FeedbackByKind  []NamedCount `json:"feedback_by_kind"`
}

// InsightsReport is what GET /api/admin/insights answers.
type InsightsReport struct {
	Days       int                `json:"days"`
	Posts      PostInsights       `json:"posts"`
	Users      UserInsights       `json:"users"`
	Diagrams   DiagramInsights    `json:"diagrams"`
	Moderation ModerationInsights `json:"moderation"`
}

// InsightsRepo aggregates the report from the database; since is 00:00 UTC of the first day.
type InsightsRepo interface {
	Report(ctx context.Context, since time.Time, days int) (InsightsReport, error)
}

type Insights struct {
	Repo  InsightsRepo
	Authz Authorizer
	now   func() time.Time
}

func NewInsights(repo InsightsRepo, authz Authorizer) *Insights {
	return &Insights{Repo: repo, Authz: authz, now: time.Now}
}

// Report needs stats.read. days is clamped to 1..365 (0 means the default).
func (i *Insights) Report(ctx context.Context, days int) (InsightsReport, error) {
	ctx, span := tracer.Start(ctx, "Insights.Report")
	defer span.End()
	if err := i.Authz.Can(ctx, "stats", "read", 0, 0); err != nil {
		return InsightsReport{}, err
	}
	if days == 0 {
		days = InsightsDefaultDays
	}
	days = min(max(days, 1), 365)
	now := i.now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))
	return i.Repo.Report(ctx, since, days)
}
