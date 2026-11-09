package post

import (
	"strings"
	"testing"
	"time"
)

func TestNewLabel(t *testing.T) {
	l, err := NewLabel(" Go ", "")
	if err != nil || l.Name != "Go" || l.Color != "#6b7280" {
		t.Fatalf("%+v %v", l, err)
	}
	l, err = NewLabel("x", "#ABCDEF")
	if err != nil || l.Color != "#abcdef" {
		t.Fatalf("%+v %v", l, err)
	}
	for _, c := range [][2]string{{" ", ""}, {strings.Repeat("a", 31), ""}, {"x", "red"}, {"x", "#12345"}} {
		if _, err := NewLabel(c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}

func TestDraftNormalize(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	ok := func(d Draft) Draft {
		t.Helper()
		got, err := d.Normalize(now)
		if err != nil {
			t.Fatalf("%+v: %v", d, err)
		}
		return got
	}
	bad := func(name string, d Draft) {
		t.Helper()
		if d.Title == "" && name != "no title" {
			d.Title = "t"
		}
		if _, err := d.Normalize(now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	d := ok(Draft{Title: " T ", Subtitle: " s ", PublishAt: &future, Tags: []string{" Go ", "go", "", "Rust"}, LabelIDs: []int{3, 1, 3}})
	if d.Title != "T" || d.Subtitle != "s" || d.Status != StatusPublished || d.PublishAt != nil {
		t.Fatalf("%+v", d)
	}
	if len(d.Tags) != 2 || d.Tags[0] != "go" || d.Tags[1] != "rust" || len(d.LabelIDs) != 2 || d.LabelIDs[0] != 1 {
		t.Fatalf("%+v", d)
	}
	for _, s := range []Status{StatusDraft, StatusPublished, StatusUnlisted} {
		if got := ok(Draft{Title: "t", Status: s, PublishAt: &future}); got.PublishAt != nil || got.Status != s {
			t.Errorf("%s: %+v", s, got)
		}
	}
	if got := ok(Draft{Title: "t", Status: StatusScheduled, PublishAt: &future}); got.PublishAt == nil {
		t.Error("scheduled lost publish_at")
	}
	for _, u := range []string{"/api/uploads/a", "https://x", "http://x"} {
		ok(Draft{Title: "t", CoverURL: u})
	}
	ok(Draft{Title: "t", CanonicalURL: "http://x"})
	ok(Draft{Title: "t", CanonicalURL: " https://x/y "})

	bad("no title", Draft{})
	bad("long title", Draft{Title: strings.Repeat("a", maxTitle+1)})
	bad("long subtitle", Draft{Subtitle: strings.Repeat("a", maxSub+1)})
	bad("long body", Draft{Body: strings.Repeat("a", maxBody+1)})
	bad("neg category", Draft{CategoryID: -1})
	bad("neg publication", Draft{PublicationID: -1})
	bad("live body", Draft{Body: "```html-live bogus\nx\n```"})
	bad("scheduled nil", Draft{Status: StatusScheduled})
	bad("scheduled past", Draft{Status: StatusScheduled, PublishAt: &past})
	bad("status", Draft{Status: "weird"})
	bad("cover", Draft{CoverURL: "ftp://x"})
	bad("canonical scheme", Draft{CanonicalURL: "ftp://x"})
	bad("canonical long", Draft{CanonicalURL: "https://" + strings.Repeat("a", 2000)})
	many := make([]string, maxTags+1)
	for i := range many {
		many[i] = string(rune('a'+i)) + "tag"
	}
	bad("tags", Draft{Tags: many})
	bad("labels", Draft{LabelIDs: []int{1, 2, 3, 4, 5, 6}})
}

func TestNewSlug(t *testing.T) {
	s := NewSlug("Hello, World!!  Go")
	if !strings.HasPrefix(s, "hello-world-go-") || len(s) != len("hello-world-go-")+8 {
		t.Fatalf("%q", s)
	}
	if s2 := NewSlug("Hello, World!!  Go"); s2 == s {
		t.Fatal("no random suffix")
	}
	if s := NewSlug("!!!"); !strings.HasPrefix(s, "post-") {
		t.Fatalf("%q", s)
	}
	if s := NewSlug("trailing dash ---"); !strings.HasPrefix(s, "trailing-dash-") || strings.Contains(s, "--") {
		t.Fatalf("%q", s)
	}
	long := NewSlug(strings.Repeat("a", 100))
	if len(long) != 60+1+8 {
		t.Fatalf("%q", long)
	}
}

func TestTags(t *testing.T) {
	if NormalizeTag("  GoLang ") != "golang" {
		t.Fatal("NormalizeTag")
	}
	if got, err := ValidateTag(" Go "); err != nil || got != "go" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := ValidateTag("  "); err == nil {
		t.Fatal("empty accepted")
	}
	if _, err := ValidateTag(strings.Repeat("a", 41)); err == nil {
		t.Fatal("long accepted")
	}
}

func TestSortTimeAndCursor(t *testing.T) {
	pub := time.Unix(300, 0).UTC()
	sched := time.Unix(200, 0).UTC()
	upd := time.Unix(100, 0).UTC()
	if (Post{PublishedAt: &pub, PublishAt: &sched, UpdatedAt: upd}).SortTime() != pub {
		t.Error("published")
	}
	if (Post{PublishAt: &sched, UpdatedAt: upd}).SortTime() != sched {
		t.Error("scheduled")
	}
	if (Post{UpdatedAt: upd}).SortTime() != upd {
		t.Error("updated")
	}

	p := Post{ID: 7, PublishedAt: &pub}
	c, err := ParseCursor(CursorAfter(p))
	if err != nil || c.ID != 7 || !c.At.Equal(pub) {
		t.Fatalf("%+v %v", c, err)
	}
	for _, s := range []string{"!!!", "bm9jb2xvbg", "YTo3", "MTA6YQ", "MTA6MA", "MTA6LTU"} {
		if _, err := ParseCursor(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestValidateClaps(t *testing.T) {
	for _, n := range []int{1, MaxClaps} {
		if ValidateClaps(n) != nil {
			t.Errorf("%d", n)
		}
	}
	for _, n := range []int{0, -1, MaxClaps + 1} {
		if ValidateClaps(n) == nil {
			t.Errorf("%d", n)
		}
	}
}

func TestNewHighlight(t *testing.T) {
	body := "héllo wörld   "
	h, err := NewHighlight(body, 1, 2, 0, 5)
	if err != nil || h.Text != "héllo" || h.PostID != 1 || h.UserID != 2 || h.Start != 0 || h.End != 5 {
		t.Fatalf("%+v %v", h, err)
	}
	for _, c := range [][2]int{{-1, 3}, {3, 3}, {3, 2}, {0, 100}, {11, 14}} {
		if _, err := NewHighlight(body, 1, 2, c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	if _, err := NewHighlight(strings.Repeat("a", maxHighlight+2), 1, 2, 0, maxHighlight+1); err == nil {
		t.Error("long accepted")
	}
}

func TestPaginated(t *testing.T) {
	items, meta := Page{Items: []Post{{ID: 1}}, Total: 1, Page: 1, Limit: 5, NextCursor: "c"}.Paginated()
	if len(items.([]Post)) != 1 || meta == nil {
		t.Fatal("bad")
	}
}

func TestLiveEdgeCases(t *testing.T) {
	// "html-live" in prose but no block: StripLive returns the body untouched.
	prose := "we talk about html-live here"
	if StripLive(prose) != prose {
		t.Error("StripLive changed prose")
	}
	// A backtick fence whose info string holds a backtick is inline code, not a fence.
	if got := LiveBlocks("```html-live `x`\ncode\n```"); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}
