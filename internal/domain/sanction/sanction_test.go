package sanction

import (
	"strings"
	"testing"
	"time"
)

func TestKindOf(t *testing.T) {
	u := time.Now()
	if KindOf(nil) != Banned || KindOf(&u) != Suspended {
		t.Fatal("KindOf wrong")
	}
}

func TestNew(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	s, err := New(1, 2, "  spam ", nil, now)
	if err != nil || s.Kind != Banned || s.Reason != "spam" || s.UserID != 1 || s.ActorID != 2 {
		t.Fatalf("ban: %+v %v", s, err)
	}
	s, err = New(1, 2, "spam", &future, now)
	if err != nil || s.Kind != Suspended || s.Until != &future {
		t.Fatalf("suspend: %+v %v", s, err)
	}
	bad := []struct {
		reason string
		until  *time.Time
		actor  int
	}{
		{" ", nil, 2},
		{strings.Repeat("x", maxReason+1), nil, 2},
		{"r", &past, 2},
		{"r", &now, 2},
		{"r", nil, 1},
	}
	for i, b := range bad {
		if _, err := New(1, b.actor, b.reason, b.until, now); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestPaginated(t *testing.T) {
	items, meta := Page{Items: []Sanction{{UserID: 1}}, Total: 1, Page: 1, Limit: 5}.Paginated()
	if len(items.([]Sanction)) != 1 || meta == nil {
		t.Fatal("bad")
	}
}
