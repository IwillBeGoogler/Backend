package feedback

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	ok := Input{Kind: KindBug, Message: "  broken  ", ContactEmail: "a@b.co"}
	f, err := New(0, ok)
	if err != nil || f.Message != "broken" || f.ContactEmail != "a@b.co" || f.Status != StatusOpen {
		t.Fatalf("anonymous = %+v, %v", f, err)
	}
	if f, _ := New(7, ok); f.UserID != 7 || f.ContactEmail != "" {
		t.Errorf("logged in keeps no contact email: %+v", f)
	}
	for name, in := range map[string]Input{
		"kind":      {Kind: "x", Message: "m"},
		"empty":     {Kind: KindIdea, Message: "  "},
		"long":      {Kind: KindIdea, Message: strings.Repeat("a", 5001)},
		"email":     {Kind: KindIdea, Message: "m", ContactEmail: "nope"},
		"long mail": {Kind: KindIdea, Message: "m", ContactEmail: strings.Repeat("a", 250) + "@b.co"},
	} {
		if _, err := New(0, in); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	f, _ = New(0, Input{Kind: KindOther, Message: "m", PageURL: strings.Repeat("é", 3000), ErrorStack: strings.Repeat("s", 20000)})
	if len([]rune(f.PageURL)) != 2000 || len(f.ErrorStack) != 10000 {
		t.Errorf("diagnostics not cut: %d %d", len([]rune(f.PageURL)), len(f.ErrorStack))
	}
}
