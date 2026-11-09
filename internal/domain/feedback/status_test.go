package feedback

import (
	"strings"
	"testing"
)

func TestValidateKind(t *testing.T) {
	for _, k := range []Kind{"", KindBug, KindIdea, KindOther} {
		if ValidateKind(k) != nil {
			t.Errorf("%q", k)
		}
	}
	if ValidateKind("nope") == nil {
		t.Error("invalid accepted")
	}
}

func TestValidateStatus(t *testing.T) {
	for _, s := range []Status{StatusOpen, StatusResolved, StatusDismissed} {
		if ValidateStatus(s, false) != nil {
			t.Errorf("%s", s)
		}
	}
	if ValidateStatus("", true) != nil {
		t.Error("empty filter")
	}
	if ValidateStatus("", false) == nil || ValidateStatus("nope", true) == nil {
		t.Error("invalid accepted")
	}
}

func TestPaginated(t *testing.T) {
	items, meta := Page{Items: []Feedback{{ID: 1}}, Total: 1, Page: 1, Limit: 5}.Paginated()
	if len(items.([]Feedback)) != 1 || meta == nil {
		t.Fatal("bad")
	}
}

func TestNewEmailAndCut(t *testing.T) {
	f, err := New(0, Input{Kind: KindBug, Message: " m ", ContactEmail: " a@b.co ", PageURL: strings.Repeat("é", maxURL+5)})
	if err != nil || f.ContactEmail != "a@b.co" || f.Message != "m" || len([]rune(f.PageURL)) != maxURL {
		t.Fatalf("%+v %v", f, err)
	}
	if f, _ := New(7, Input{Kind: KindIdea, Message: "m", ContactEmail: "a@b.co"}); f.ContactEmail != "" || f.UserID != 7 {
		t.Errorf("account email kept: %+v", f)
	}
	for _, e := range []string{strings.Repeat("a", maxEmail) + "@b.co", "nope", "Name <a@b.co>"} {
		if _, err := New(0, Input{Kind: KindBug, Message: "m", ContactEmail: e}); err == nil {
			t.Errorf("%q accepted", e)
		}
	}
	for _, in := range []Input{{Kind: "x", Message: "m"}, {Kind: KindBug, Message: " "}, {Kind: KindBug, Message: strings.Repeat("m", maxMessage+1)}} {
		if _, err := New(0, in); err == nil {
			t.Errorf("%+v accepted", in)
		}
	}
}
