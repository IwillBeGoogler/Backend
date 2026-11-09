package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

type fakeSiteRepo struct{ calls int }

func (f *fakeSiteRepo) Counts(context.Context) (SiteCounts, error) {
	f.calls++
	return SiteCounts{Posts: 7, Diagrams: 2, Users: 9, Visitors: 40}, nil
}

func (f *fakeSiteRepo) PublicPosts(context.Context) ([]PostRef, error) {
	out := make([]PostRef, 7)
	for i := range out {
		out[i] = PostRef{ID: i + 1, Slug: "p" + itoa(i+1), Title: "P", Author: "a"}
	}
	return out, nil
}

func (f *fakeSiteRepo) PublicDiagrams(context.Context) ([]DiagramRef, error) {
	return []DiagramRef{{FileID: "f1", Slug: "d1"}, {FileID: "f2", Slug: "d2"}}, nil
}

func (f *fakeSiteRepo) PageViews(_ context.Context, since time.Time) ([]DayCount, error) {
	return []DayCount{{Day: since.Format("2006-01-02"), Count: 3}}, nil
}

type fakePostViews struct{ ViewCounter }

func (fakePostViews) Counts(_ context.Context, ids []int) map[int]int64 {
	m := map[int]int64{}
	for _, id := range ids {
		m[id] = int64(id) * 10
	}
	return m
}

func (fakePostViews) DailyTotals(_ context.Context, days []time.Time) ([]int64, error) {
	return make([]int64, len(days)), nil
}

type fakeDiagramViews struct{ DiagramViewCounter }

func (fakeDiagramViews) Counts(context.Context, []string) map[string]int64 {
	return map[string]int64{"f1": 1, "f2": 5}
}

func (fakeDiagramViews) DailyTotals(_ context.Context, days []time.Time) ([]int64, error) {
	out := make([]int64, len(days))
	out[len(out)-1] = 4
	return out, nil
}

func TestSiteStats(t *testing.T) {
	repo := &fakeSiteRepo{}
	s := NewSiteStats(repo, fakePostViews{}, fakeDiagramViews{}, fakeAuthz{caller: 1, perms: map[string]bool{"stats.read": true}})
	pub, err := s.Public(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pub.Totals.PostViews != 280 || pub.Totals.DiagramViews != 6 || pub.Totals.Visitors != 40 || pub.Totals.Users != 9 {
		t.Errorf("totals = %+v", pub.Totals)
	}
	if len(pub.TopPosts) != PublicTopN || pub.TopPosts[0].ID != 7 || pub.TopDiagrams[0].Slug != "d2" {
		t.Errorf("tops = %+v %+v", pub.TopPosts, pub.TopDiagrams)
	}
	adm, err := s.Admin(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(adm.TopPosts) != 7 || len(adm.Days) != SiteStatsDays || adm.Days[SiteStatsDays-1].DiagramViews != 4 || adm.Days[0].PageViews != 3 {
		t.Errorf("admin = %+v", adm.Days[SiteStatsDays-1])
	}
	if repo.calls != 1 {
		t.Errorf("snapshot not cached: %d reads", repo.calls)
	}
	s.Authz = fakeAuthz{caller: 1}
	if _, err := s.Admin(context.Background(), 5); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("admin without permission: %v", err)
	}
}
