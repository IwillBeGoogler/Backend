package post

import (
	"strings"
	"testing"
	"time"
)

func TestTranslationInputNormalize(t *testing.T) {
	got, err := TranslationInput{Title: "  Hi ", Subtitle: " s "}.Normalize()
	if err != nil || got.Title != "Hi" || got.Subtitle != "s" {
		t.Fatalf("got %+v, %v", got, err)
	}
	for name, in := range map[string]TranslationInput{
		"no title":      {Title: " "},
		"long title":    {Title: strings.Repeat("a", maxTitle+1)},
		"long subtitle": {Title: "a", Subtitle: strings.Repeat("a", maxSub+1)},
		"long body":     {Title: "a", Body: strings.Repeat("a", maxBody+1)},
	} {
		if _, err := in.Normalize(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestDraftLang(t *testing.T) {
	d, err := Draft{Title: "x"}.Normalize(time.Now())
	if err != nil || d.Lang != "uz" {
		t.Fatalf("default lang = %q, %v", d.Lang, err)
	}
	if _, err := (Draft{Title: "x", Lang: "fr"}).Normalize(time.Now()); err == nil {
		t.Fatal("bad lang accepted")
	}
	if got := (Post{Lang: "en"}).Draft().Lang; got != "en" {
		t.Fatalf("Draft lang = %q", got)
	}
}
