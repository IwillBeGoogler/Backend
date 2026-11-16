package category

import (
	"errors"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"  Go  ", "Go", false},
		{"   ", "", true},
		{strings.Repeat("a", 51), "", true},
		{strings.Repeat("a", 50), strings.Repeat("a", 50), false},
	}
	for _, c := range cases {
		got, err := NormalizeName(c.in)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("NormalizeName(%q) = %q, %v", c.in, got, err)
		}
	}
}

func TestInputNormalize(t *testing.T) {
	got, err := (Input{
		Name:        "  System Design ",
		ParentID:    2,
		Names:       map[string]string{"uz": "  Tizim ", "ru": "  Система  ", "fr": "ignored"},
		Description: "desc",
		Position:    1,
	}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "System Design" || got.Slug != "system-design" || got.Names["uz"] != "Tizim" || got.Names["ru"] != "Система" || len(got.Names) != 2 {
		t.Fatalf("got %+v", got)
	}

	got2, err := (Input{Name: "Go", Slug: "  Go Lang! "}).Normalize()
	if err != nil || got2.Slug != "go-lang" {
		t.Fatalf("custom slug = %+v, %v", got2, err)
	}

	badCases := map[string]Input{
		"empty name":       {Name: "   "},
		"non-alnum slug":   {Name: "!!!"},
		"long slug":        {Name: "Go", Slug: strings.Repeat("a", 61)},
		"negative parent":  {Name: "Go", ParentID: -1},
		"long description": {Name: "Go", Description: strings.Repeat("d", 301)},
		"long uz name":     {Name: "Go", Names: map[string]string{"uz": strings.Repeat("u", 51)}},
	}
	for name, in := range badCases {
		_, err := in.Normalize()
		var ve *domain.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
