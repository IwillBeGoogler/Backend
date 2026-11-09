package readinglist

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	got, err := ListInput{Name: " a ", Description: " d "}.Normalize()
	if err != nil || got.Name != "a" || got.Description != "d" {
		t.Fatalf("%+v %v", got, err)
	}
	bad := []ListInput{
		{Name: " "},
		{Name: strings.Repeat("a", 101)},
		{Name: "a", Description: strings.Repeat("d", 501)},
	}
	for i, b := range bad {
		if _, err := b.Normalize(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if ErrTooMany == nil || MaxLists != 100 {
		t.Fatal("consts")
	}
}

func TestPaginated(t *testing.T) {
	items, meta := Page{Items: []List{{ID: 1}}, Total: 1, Page: 1, Limit: 5}.Paginated()
	if len(items.([]List)) != 1 || meta == nil {
		t.Fatal("bad")
	}
}
