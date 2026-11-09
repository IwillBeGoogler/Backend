package report

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	r, err := New(1, TargetPost, 5, ReasonSpam, " n ")
	if err != nil || r.Status != StatusOpen || r.Note != "n" || r.TargetID != 5 || r.ReporterID != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := New(1, TargetDiagram, 5, ReasonSpam, ""); err == nil {
		t.Fatal("diagram via New accepted")
	}
	if _, err := New(1, TargetUser, 0, ReasonSpam, ""); err == nil {
		t.Fatal("bad id accepted")
	}
	if _, err := New(1, TargetComment, 1, "nope", ""); err == nil {
		t.Fatal("bad reason accepted")
	}
}

func TestNewDiagram(t *testing.T) {
	r, err := NewDiagram(1, "f1", ReasonAbuse, "")
	if err != nil || r.TargetType != TargetDiagram || r.TargetFileID != "f1" || r.TargetID != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := NewDiagram(1, "", ReasonAbuse, ""); err == nil {
		t.Fatal("empty id accepted")
	}
	if _, err := NewDiagram(1, strings.Repeat("f", 65), ReasonAbuse, ""); err == nil {
		t.Fatal("long id accepted")
	}
}

func TestBuildReasons(t *testing.T) {
	for _, r := range []Reason{ReasonSpam, ReasonAbuse, ReasonHarassment, ReasonOffTopic} {
		if _, err := New(1, TargetPost, 1, r, ""); err != nil {
			t.Errorf("%s: %v", r, err)
		}
	}
	if _, err := New(1, TargetPost, 1, ReasonOther, "  "); err == nil {
		t.Fatal("other without note accepted")
	}
	if _, err := New(1, TargetPost, 1, ReasonOther, "why"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(1, TargetPost, 1, ReasonSpam, strings.Repeat("x", maxNote+1)); err == nil {
		t.Fatal("long note accepted")
	}
}

func TestValidateDecision(t *testing.T) {
	if ValidateDecision(StatusResolved) != nil || ValidateDecision(StatusDismissed) != nil {
		t.Fatal("valid rejected")
	}
	if ValidateDecision(StatusOpen) == nil || ValidateDecision("") == nil {
		t.Fatal("invalid accepted")
	}
}

func TestPaginated(t *testing.T) {
	items, meta := Page{Items: []Report{{ID: 1}}, Total: 1, Page: 1, Limit: 5}.Paginated()
	if len(items.([]Report)) != 1 || meta == nil {
		t.Fatal("bad")
	}
}
