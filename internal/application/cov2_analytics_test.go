package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type recAnalytics struct {
	got      []AnalyticsEvent
	insertEr error
	since    time.Time
	days     int
	purged   time.Time
}

func (r *recAnalytics) Insert(_ context.Context, e AnalyticsEvent) error {
	r.got = append(r.got, e)
	return r.insertEr
}

func (r *recAnalytics) Report(_ context.Context, since time.Time, days int) (AnalyticsReport, error) {
	r.since, r.days = since, days
	return AnalyticsReport{Days: days}, nil
}

func (r *recAnalytics) PurgeBefore(_ context.Context, before time.Time) (int64, error) {
	r.purged = before
	return 1, r.insertEr
}

func TestAnalyticsTrack(t *testing.T) {
	long := "/" + strings.Repeat("x", 400)
	cases := []struct {
		name    string
		ev      AnalyticsEvent
		wantErr bool
		check   func(t *testing.T, e AnalyticsEvent)
	}{
		{name: "unknown event", ev: AnalyticsEvent{Name: "nope", Visitor: "abcdefgh1"}, wantErr: true},
		{name: "bad visitor", ev: AnalyticsEvent{Name: EventPageView, Visitor: "x"}, wantErr: true},
		{name: "relative path", ev: AnalyticsEvent{Name: EventPageView, Visitor: "abcdefgh1", Path: "home"}, wantErr: true},
		{name: "read time without post", ev: AnalyticsEvent{Name: EventReadTime, Visitor: "abcdefgh1", Value: 5}, wantErr: true},
		{name: "read time without value", ev: AnalyticsEvent{Name: EventReadTime, Visitor: "abcdefgh1", PostID: 1}, wantErr: true},
		{name: "read time capped", ev: AnalyticsEvent{Name: EventReadTime, Visitor: "abcdefgh1", PostID: 1, Value: 1 << 20}, check: func(t *testing.T, e AnalyticsEvent) {
			if e.Value != MaxReadSeconds {
				t.Errorf("value = %d", e.Value)
			}
		}},
		{name: "negative value floored and fields cleaned", ev: AnalyticsEvent{Name: EventPageView, Visitor: "abcdefgh1", Value: -4, Path: long, Lang: "en-US-extra", Referrer: " https://Example.COM/a?b=c "}, check: func(t *testing.T, e AnalyticsEvent) {
			if e.Value != 0 || len(e.Path) != 300 || e.Lang != "en-US-ex" || e.Referrer != "example.com" {
				t.Errorf("event = %+v", e)
			}
		}},
		{name: "signed in visitor derived", ev: AnalyticsEvent{Name: EventPageView, UserID: 12}, check: func(t *testing.T, e AnalyticsEvent) {
			if e.Visitor != "u12" {
				t.Errorf("visitor = %q", e.Visitor)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &recAnalytics{}
			a := NewAnalytics(r, nil)
			err := a.record(context.Background(), c.ev)
			if name := c.ev.Name; name == "nope" {
				err = a.Track(context.Background(), c.ev)
			} else if ClientEvents[name] {
				err = a.Track(context.Background(), c.ev)
			}
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if c.check != nil {
				c.check(t, r.got[len(r.got)-1])
			}
		})
	}
}

func TestAnalyticsRecordReportPurge(t *testing.T) {
	r := &recAnalytics{insertEr: errors.New("db down")}
	a := NewAnalytics(r, fakeAuthz{caller: 1, perms: map[string]bool{"stats.read": true}})
	a.now = func() time.Time { return time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC) }

	a.Record(context.Background(), "signup", 4, 0) // errors are swallowed
	if len(r.got) != 1 || r.got[0].Visitor != "u4" {
		t.Errorf("recorded %+v", r.got)
	}
	var nilA *Analytics
	nilA.Record(context.Background(), "signup", 1, 0)
	if err := nilA.PurgeOld(context.Background()); err != nil {
		t.Errorf("nil purge: %v", err)
	}
	if err := a.Track(context.Background(), AnalyticsEvent{Name: EventPageView, Visitor: "abcdefgh1"}); !errors.Is(err, r.insertEr) {
		t.Errorf("track must surface repo error: %v", err)
	}
	if err := a.PurgeOld(context.Background()); !errors.Is(err, r.insertEr) {
		t.Errorf("purge err = %v", err)
	}
	if want := a.now().Add(-AnalyticsRetention); !r.purged.Equal(want) {
		t.Errorf("purged before %v, want %v", r.purged, want)
	}

	for in, want := range map[int]int{-5: 1, 0: 1, 7: 7, 9999: 365} {
		rep, err := a.Report(context.Background(), in)
		if err != nil || rep.Days != want {
			t.Errorf("days %d: %+v %v", in, rep, err)
		}
	}
	if _, err := a.Report(context.Background(), 365); err != nil || !r.since.Equal(time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -364)) {
		t.Errorf("since = %v, %v", r.since, err)
	}
	a.Authz = fakeAuthz{caller: 1}
	if _, err := a.Report(context.Background(), 7); err == nil {
		t.Error("report without stats.read must fail")
	}
}

type failSite struct {
	fakeSiteRepo
	failAt, n int
}

func (f *failSite) hit() error {
	f.n++
	if f.n == f.failAt {
		return errBoom
	}
	return nil
}

func (f *failSite) Counts(ctx context.Context) (SiteCounts, error) {
	if err := f.hit(); err != nil {
		return SiteCounts{}, err
	}
	return f.fakeSiteRepo.Counts(ctx)
}

func (f *failSite) PublicPosts(ctx context.Context) ([]PostRef, error) {
	if err := f.hit(); err != nil {
		return nil, err
	}
	return f.fakeSiteRepo.PublicPosts(ctx)
}

func (f *failSite) PublicDiagrams(ctx context.Context) ([]DiagramRef, error) {
	if err := f.hit(); err != nil {
		return nil, err
	}
	return f.fakeSiteRepo.PublicDiagrams(ctx)
}

func (f *failSite) PageViews(ctx context.Context, since time.Time) ([]DayCount, error) {
	if err := f.hit(); err != nil {
		return nil, err
	}
	return f.fakeSiteRepo.PageViews(ctx, since)
}

type failDaily struct{ DiagramViewCounter }

func (failDaily) Counts(context.Context, []string) map[string]int64 { return nil }
func (failDaily) DailyTotals(context.Context, []time.Time) ([]int64, error) {
	return nil, errBoom
}

type failPostDaily struct{ ViewCounter }

func (failPostDaily) Counts(context.Context, []int) map[int]int64 { return nil }
func (failPostDaily) DailyTotals(context.Context, []time.Time) ([]int64, error) {
	return nil, errBoom
}

func TestSiteStatsErrors(t *testing.T) {
	authz := fakeAuthz{caller: 1, perms: map[string]bool{"stats.read": true}}
	for at := 1; at <= 3; at++ {
		s := NewSiteStats(&failSite{failAt: at}, nil, nil, authz)
		if _, err := s.Public(context.Background()); !errors.Is(err, errBoom) {
			t.Errorf("public failAt %d: %v", at, err)
		}
		s = NewSiteStats(&failSite{failAt: at}, nil, nil, authz)
		if _, err := s.Admin(context.Background(), 0); !errors.Is(err, errBoom) {
			t.Errorf("admin failAt %d: %v", at, err)
		}
	}
	// page views is the fourth repository call of Admin
	s := NewSiteStats(&failSite{failAt: 4}, nil, nil, authz)
	if _, err := s.Admin(context.Background(), 0); !errors.Is(err, errBoom) {
		t.Errorf("page views: %v", err)
	}
	s = NewSiteStats(&failSite{}, failPostDaily{}, nil, authz)
	if _, err := s.Admin(context.Background(), 0); !errors.Is(err, errBoom) {
		t.Errorf("post daily: %v", err)
	}
	s = NewSiteStats(&failSite{}, nil, failDaily{}, authz)
	if _, err := s.Admin(context.Background(), 0); !errors.Is(err, errBoom) {
		t.Errorf("diagram daily: %v", err)
	}
}
