package series

import (
	"strings"
	"testing"
)

func TestNavigate(t *testing.T) {
	parts := []Part{{Position: 1, PostID: 10}, {Position: 2, PostID: 20}, {Position: 3, PostID: 30}}
	s := Series{ID: 1}

	n, ok := Navigate(s, parts, 20)
	if !ok || n.Position != 2 || n.Prev.PostID != 10 || n.Next.PostID != 30 || len(n.Posts) != 3 {
		t.Fatalf("mid: %+v", n)
	}
	n, ok = Navigate(s, parts, 10)
	if !ok || n.Prev != nil || n.Next == nil {
		t.Fatalf("first: %+v", n)
	}
	n, ok = Navigate(s, parts, 30)
	if !ok || n.Prev == nil || n.Next != nil {
		t.Fatalf("last: %+v", n)
	}
	if _, ok := Navigate(s, parts, 99); ok {
		t.Fatal("missing found")
	}
}

func TestNormalize(t *testing.T) {
	got, err := SeriesInput{Title: " t ", Description: " d "}.Normalize()
	if err != nil || got.Title != "t" || got.Description != "d" {
		t.Fatalf("%+v %v", got, err)
	}
	bad := []SeriesInput{
		{Title: ""},
		{Title: strings.Repeat("a", 201)},
		{Title: "a", Description: strings.Repeat("d", 1001)},
	}
	for i, b := range bad {
		if _, err := b.Normalize(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestValidateOrder(t *testing.T) {
	if err := ValidateOrder([]int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOrder(nil); err != nil {
		t.Fatal(err)
	}
	if ValidateOrder(make([]int, MaxParts+1)) == nil {
		t.Fatal("too many accepted")
	}
	if ValidateOrder([]int{0}) == nil || ValidateOrder([]int{-1}) == nil {
		t.Fatal("nonpositive accepted")
	}
	if ValidateOrder([]int{1, 1}) == nil {
		t.Fatal("dup accepted")
	}
	if ErrPostInOtherSeries == nil {
		t.Fatal("nil err")
	}
}
