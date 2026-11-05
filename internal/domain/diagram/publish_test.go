package diagram

import (
	"strings"
	"testing"
)

func TestNewSlug(t *testing.T) {
	cases := map[string]string{
		"Checkout Flow":        "checkout-flow-",
		"  --Hello, World!-- ": "hello-world-",
		"Привет":               "diagram-",
		"":                     "diagram-",
		"a/b\\c?d":             "a-b-c-d-",
	}
	for title, prefix := range cases {
		s := NewSlug(title)
		if !strings.HasPrefix(s, prefix) || !ValidSlug(s) {
			t.Errorf("NewSlug(%q) = %q, want prefix %q and a valid slug", title, s, prefix)
		}
	}
	if long := NewSlug(strings.Repeat("word ", 40)); !ValidSlug(long) || len(long) > 70 {
		t.Errorf("long slug = %q", long)
	}
	if NewSlug("x") == NewSlug("x") {
		t.Error("equal titles collide")
	}
	for _, bad := range []string{"", "-a", "a-", "A", "a b", "../x", "a_b"} {
		if ValidSlug(bad) {
			t.Errorf("ValidSlug(%q)", bad)
		}
	}
}

func TestCleanPublication(t *testing.T) {
	p, err := CleanPublication(VisPublic, "  T ", " d ", []string{"A", " a ", "B"}, "file")
	if err != nil || p.Title != "T" || p.Description != "d" || len(p.Tags) != 2 || p.Tags[0] != "a" {
		t.Fatalf("got %+v, %v", p, err)
	}
	if p, _ = CleanPublication(VisUnlisted, " ", "", nil, "Named"); p.Title != "Named" || p.Tags == nil {
		t.Errorf("defaults = %+v", p)
	}
	for _, v := range []Visibility{VisPrivate, "", "secret"} {
		if _, err := CleanPublication(v, "t", "", nil, "f"); err == nil {
			t.Errorf("visibility %q accepted", v)
		}
	}
	if _, err := CleanPublication(VisPublic, "bad\x00title", "", nil, "f"); err == nil {
		t.Error("control characters accepted")
	}
	if !VisUnlisted.Published() || !VisPublic.Published() || VisPrivate.Published() {
		t.Error("Published")
	}
}

func TestCleanTemplate(t *testing.T) {
	ok := Template{Title: " T ", Kind: "db", FileID: "fil_x", Description: " d "}
	got, err := CleanTemplate(ok)
	if err != nil || got.Title != "T" || got.Description != "d" {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, mut := range []func(*Template){
		func(x *Template) { x.Title = "" },
		func(x *Template) { x.Title = strings.Repeat("é", 121) },
		func(x *Template) { x.Kind = "uml" },
		func(x *Template) { x.FileID = "" },
		func(x *Template) { x.Position = -1 },
		func(x *Template) { x.Description = strings.Repeat("d", 501) },
	} {
		bad := ok
		mut(&bad)
		if _, err := CleanTemplate(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	// 120 multi-byte characters are fine: limits count characters
	ok.Title = strings.Repeat("é", 120)
	if _, err := CleanTemplate(ok); err != nil {
		t.Errorf("120 runes: %v", err)
	}
}
