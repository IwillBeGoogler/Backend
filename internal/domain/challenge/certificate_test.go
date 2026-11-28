package challenge

import (
	"errors"
	"strings"
	"testing"
)

func TestEligible(t *testing.T) {
	for _, tc := range []struct {
		name  string
		days  int
		stats Stats
		want  error
	}{
		{"too short", 29, Stats{Ended: true, DaysDone: 29}, ErrCertificateShort},
		{"running", 30, Stats{Today: 29, DaysDone: 28}, ErrCertificateRunning},
		{"last day, one missing", 30, Stats{Today: 30, DaysDone: 29}, ErrCertificateRunning},
		{"last day, all done", 30, Stats{Today: 30, DaysDone: 30}, nil},
		{"before start", 30, Stats{}, ErrCertificateRunning},
		{"ended at 90%", 30, Stats{Ended: true, DaysDone: 27}, nil},
		{"ended below 90%", 30, Stats{Ended: true, DaysDone: 26}, ErrCertificateIncomplete},
		{"rounds up", 31, Stats{Ended: true, DaysDone: 27}, ErrCertificateIncomplete},
		{"rounded up met", 31, Stats{Ended: true, DaysDone: 28}, nil},
	} {
		if err := Eligible(tc.days, tc.stats); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestCertificateID(t *testing.T) {
	a, b := NewCertificateID(), NewCertificateID()
	if !ValidCertificateID(a) || a == b {
		t.Errorf("ids %q %q", a, b)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("A", 16), strings.Repeat("1", 16), strings.Repeat("a", 17)} {
		if ValidCertificateID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRevokeReason(t *testing.T) {
	if got, err := RevokeReason(" spam "); err != nil || got != "spam" {
		t.Errorf("%q %v", got, err)
	}
	for _, bad := range []string{"  ", strings.Repeat("a", 501), "a\x00"} {
		if _, err := RevokeReason(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
