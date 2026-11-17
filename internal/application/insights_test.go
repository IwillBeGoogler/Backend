package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

type fakeInsightsRepo struct {
	since time.Time
	days  int
}

func (f *fakeInsightsRepo) Report(_ context.Context, since time.Time, days int) (InsightsReport, error) {
	f.since, f.days = since, days
	return InsightsReport{Days: days}, nil
}

func TestInsightsReport(t *testing.T) {
	repo := &fakeInsightsRepo{}
	in := NewInsights(repo, fakeAuthz{caller: 1, perms: map[string]bool{"stats.read": true}})
	in.now = func() time.Time { return time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC) }

	for days, want := range map[int]int{0: InsightsDefaultDays, 7: 7, -3: 1, 9999: 365} {
		if _, err := in.Report(context.Background(), days); err != nil || repo.days != want {
			t.Fatalf("days %d: got %d, err %v, want %d", days, repo.days, err, want)
		}
	}
	if _, err := in.Report(context.Background(), 7); err != nil || !repo.since.Equal(time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("since = %v", repo.since)
	}

	denied := NewInsights(repo, fakeAuthz{caller: 2})
	if _, err := denied.Report(context.Background(), 7); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want forbidden", err)
	}
}
