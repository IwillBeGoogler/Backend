package application

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Site statistics: view totals, content and visitor counts and the most
// viewed posts and diagrams. The public view carries only what any visitor
// can already see; the admin view adds daily series and longer lists.

// SiteStatsTTL is how long a computed snapshot is reused.
const SiteStatsTTL = time.Minute

// SiteStatsDays is the length of the admin daily series.
const SiteStatsDays = 30

// Top list sizes: the public home page and the admin default / cap.
const (
	PublicTopN   = 5
	AdminTopN    = 20
	AdminTopNMax = 100
)

// SiteCounts are the database counts of public content and people.
type SiteCounts struct {
	Posts    int `json:"posts"`    // published posts
	Diagrams int `json:"diagrams"` // public diagrams
	Users    int `json:"users"`    // active accounts
	Visitors int `json:"visitors"` // distinct page_view visitors, all time
}

// PostRef is a published post as the stats list it.
type PostRef struct {
	ID     int
	Slug   string
	Title  string
	Author string
}

// DiagramRef is a public diagram as the stats list it.
type DiagramRef struct {
	FileID string
	Slug   string
	Title  string
	Author string
}

// SiteStatsRepo reads the database side of the site statistics.
type SiteStatsRepo interface {
	Counts(ctx context.Context) (SiteCounts, error)
	PublicPosts(ctx context.Context) ([]PostRef, error)
	PublicDiagrams(ctx context.Context) ([]DiagramRef, error)
	// PageViews counts page_view events per UTC day since the given time.
	PageViews(ctx context.Context, since time.Time) ([]DayCount, error)
}

// SiteTotals are the headline numbers.
type SiteTotals struct {
	PostViews    int64 `json:"post_views"`
	DiagramViews int64 `json:"diagram_views"`
	Visitors     int   `json:"visitors"`
	Posts        int   `json:"posts"`
	Diagrams     int   `json:"diagrams"`
	Users        int   `json:"users"`
}

// TopPost is a post with its unique views.
type TopPost struct {
	ID     int    `json:"id"`
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Views  int64  `json:"views"`
}

// TopDiagram is a public diagram with its unique views.
type TopDiagram struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Views  int64  `json:"views"`
}

// SitePublicStats is what GET /api/stats/site answers.
type SitePublicStats struct {
	Totals      SiteTotals   `json:"totals"`
	TopPosts    []TopPost    `json:"top_posts"`
	TopDiagrams []TopDiagram `json:"top_diagrams"`
}

// SiteDay is one day of the admin series.
type SiteDay struct {
	Day          string `json:"day"` // YYYY-MM-DD, UTC
	PostViews    int64  `json:"post_views"`
	DiagramViews int64  `json:"diagram_views"`
	PageViews    int    `json:"page_views"`
}

// SiteAdminStats is the admin view: the public one with longer lists and
// the last SiteStatsDays days.
type SiteAdminStats struct {
	SitePublicStats
	Days []SiteDay `json:"days"`
}

type siteSnapshot struct {
	totals   SiteTotals
	posts    []TopPost    // most viewed first
	diagrams []TopDiagram // most viewed first
}

// SiteStats computes the site statistics and caches them for SiteStatsTTL.
type SiteStats struct {
	Repo     SiteStatsRepo
	Posts    ViewCounter
	Diagrams DiagramViewCounter // nil: diagram views read as zero
	Authz    Authorizer
	now      func() time.Time

	mu   sync.Mutex
	at   time.Time
	snap siteSnapshot
}

func NewSiteStats(repo SiteStatsRepo, posts ViewCounter, diagrams DiagramViewCounter, authz Authorizer) *SiteStats {
	return &SiteStats{Repo: repo, Posts: posts, Diagrams: diagrams, Authz: authz, now: time.Now}
}

func (s *SiteStats) snapshot(ctx context.Context) (siteSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && s.now().Sub(s.at) < SiteStatsTTL {
		return s.snap, nil
	}
	counts, err := s.Repo.Counts(ctx)
	if err != nil {
		return siteSnapshot{}, err
	}
	posts, err := s.Repo.PublicPosts(ctx)
	if err != nil {
		return siteSnapshot{}, err
	}
	diagrams, err := s.Repo.PublicDiagrams(ctx)
	if err != nil {
		return siteSnapshot{}, err
	}
	snap := siteSnapshot{totals: SiteTotals{Visitors: counts.Visitors, Posts: counts.Posts, Diagrams: counts.Diagrams, Users: counts.Users},
		posts: make([]TopPost, len(posts)), diagrams: make([]TopDiagram, len(diagrams))}

	var pv map[int]int64
	if s.Posts != nil {
		ids := make([]int, len(posts))
		for i, p := range posts {
			ids[i] = p.ID
		}
		pv = s.Posts.Counts(ctx, ids)
	}
	for i, p := range posts {
		snap.posts[i] = TopPost{ID: p.ID, Slug: p.Slug, Title: p.Title, Author: p.Author, Views: pv[p.ID]}
		snap.totals.PostViews += pv[p.ID]
	}
	var dv map[string]int64
	if s.Diagrams != nil {
		ids := make([]string, len(diagrams))
		for i, d := range diagrams {
			ids[i] = d.FileID
		}
		dv = s.Diagrams.Counts(ctx, ids)
	}
	for i, d := range diagrams {
		snap.diagrams[i] = TopDiagram{Slug: d.Slug, Title: d.Title, Author: d.Author, Views: dv[d.FileID]}
		snap.totals.DiagramViews += dv[d.FileID]
	}
	sort.SliceStable(snap.posts, func(i, j int) bool { return snap.posts[i].Views > snap.posts[j].Views })
	sort.SliceStable(snap.diagrams, func(i, j int) bool { return snap.diagrams[i].Views > snap.diagrams[j].Views })
	s.snap, s.at = snap, s.now()
	return snap, nil
}

func (sn siteSnapshot) top(n int) SitePublicStats {
	out := SitePublicStats{Totals: sn.totals, TopPosts: []TopPost{}, TopDiagrams: []TopDiagram{}}
	out.TopPosts = append(out.TopPosts, sn.posts[:min(n, len(sn.posts))]...)
	out.TopDiagrams = append(out.TopDiagrams, sn.diagrams[:min(n, len(sn.diagrams))]...)
	return out
}

// Public returns the totals and the top PublicTopN posts and public
// diagrams. It needs no permission and exposes nothing private.
func (s *SiteStats) Public(ctx context.Context) (SitePublicStats, error) {
	ctx, span := tracer.Start(ctx, "SiteStats.Public")
	defer span.End()
	sn, err := s.snapshot(ctx)
	if err != nil {
		return SitePublicStats{}, err
	}
	return sn.top(PublicTopN), nil
}

// Admin returns the public numbers with the top limit lists (default
// AdminTopN, at most AdminTopNMax) and the last SiteStatsDays days. It
// needs stats.read.
func (s *SiteStats) Admin(ctx context.Context, limit int) (SiteAdminStats, error) {
	ctx, span := tracer.Start(ctx, "SiteStats.Admin")
	defer span.End()
	if err := s.Authz.Can(ctx, "stats", "read", 0, 0); err != nil {
		return SiteAdminStats{}, err
	}
	if limit <= 0 {
		limit = AdminTopN
	}
	limit = min(limit, AdminTopNMax)
	sn, err := s.snapshot(ctx)
	if err != nil {
		return SiteAdminStats{}, err
	}
	out := SiteAdminStats{SitePublicStats: sn.top(limit)}

	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := make([]time.Time, SiteStatsDays)
	for i := range days {
		days[i] = today.AddDate(0, 0, i-(SiteStatsDays-1))
	}
	var pv, dv []int64
	if s.Posts != nil {
		if pv, err = s.Posts.DailyTotals(ctx, days); err != nil {
			return out, err
		}
	}
	if s.Diagrams != nil {
		if dv, err = s.Diagrams.DailyTotals(ctx, days); err != nil {
			return out, err
		}
	}
	views, err := s.Repo.PageViews(ctx, days[0])
	if err != nil {
		return out, err
	}
	byDay := make(map[string]int, len(views))
	for _, v := range views {
		byDay[v.Day] = v.Count
	}
	out.Days = make([]SiteDay, len(days))
	for i, d := range days {
		key := d.Format("2006-01-02")
		day := SiteDay{Day: key, PageViews: byDay[key]}
		if pv != nil {
			day.PostViews = pv[i]
		}
		if dv != nil {
			day.DiagramViews = dv[i]
		}
		out.Days[i] = day
	}
	return out, nil
}
