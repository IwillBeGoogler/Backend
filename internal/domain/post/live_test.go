package post

import (
	"strings"
	"testing"
	"time"
)

func block(info, code string) string { return "```" + info + "\n" + code + "\n```\n" }

func TestLiveBlocks(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		n      int
		height int
	}{
		{"default height", block("html-live", "<p>x</p>"), 1, 400},
		{"height", block("html-live height=250", "x"), 1, 250},
		{"clamp low", block("html-live height=5", "x"), 1, 100},
		{"clamp high", block("html-live height=99999", "x"), 1, 1000},
		{"tilde fence", "~~~html-live height=300\nx\n~~~\n", 1, 300},
		{"inside other fence", "````md\n" + block("html-live", "x") + "````\n", 0, 0},
		{"nested in list", "- item\n\n    " + "```html-live\n    x\n    ```\n", 0, 0},
		{"other language", block("html", "x"), 0, 0},
		{"lookalike language", block("html-live-400", "x"), 0, 0},
	}
	for _, c := range cases {
		got := LiveBlocks(c.body)
		if len(got) != c.n || c.n > 0 && got[0].Height != c.height {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
}

func TestValidateLive(t *testing.T) {
	bad := map[string]string{
		"junk option":      block("html-live height=1 onload=alert(1)", "x"),
		"non integer":      block("html-live height=40abc", "x"),
		"negative":         block("html-live height=-5", "x"),
		"float":            block("html-live height=1e3", "x"),
		"unknown option":   block("html-live autoplay", "x"),
		"too big":          block("html-live", strings.Repeat("a", MaxLiveBytes+1)),
		"too many":         strings.Repeat(block("html-live", "x")+"\n", MaxLiveBlocks+1),
		"unterminated big": "```html-live\n" + strings.Repeat("a", MaxLiveBytes+1),
	}
	for name, body := range bad {
		if err := ValidateLive(body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := block("html-live", strings.Repeat("a", MaxLiveBytes)) + strings.Repeat(block("html-live", "x"), MaxLiveBlocks-1)
	if err := ValidateLive(ok); err != nil {
		t.Errorf("limits: %v", err)
	}
}

func TestNormalizeRejectsBadLive(t *testing.T) {
	d := Draft{Title: "t", Body: block("html-live height=x", "y")}
	if _, err := d.Normalize(time.Now()); err == nil || !strings.Contains(err.Error(), "html-live") {
		t.Fatalf("err = %v", err)
	}
}

func TestStripLiveAndReadingTime(t *testing.T) {
	body := "intro words\n\n" + block("html-live", strings.Repeat("word ", 1000)) + "\nafter"
	got := StripLive(body)
	if strings.Contains(got, "word word") || !strings.Contains(got, "intro words") || !strings.Contains(got, "after") {
		t.Errorf("StripLive = %q", got)
	}
	if ReadingTime(body) != 1 {
		t.Errorf("ReadingTime = %d", ReadingTime(body))
	}
}

func TestLiveToLink(t *testing.T) {
	h := "<p>a</p>" + LiveHTML(LiveBlock{Height: 300, Code: `<script>alert("x")</script>`}) + "<p>b</p>"
	got := LiveToLink(h, "https://x.test/p/a?b=1&c=2")
	if strings.Contains(got, "script") || !strings.Contains(got, LiveNote) || !strings.Contains(got, "b=1&amp;c=2") {
		t.Errorf("got %q", got)
	}
}
