package comment

import (
	"strings"
	"testing"
)

func TestValidateText(t *testing.T) {
	if got, err := ValidateText("  hi "); err != nil || got != "hi" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := ValidateText(" "); err == nil {
		t.Fatal("empty accepted")
	}
	if _, err := ValidateText(strings.Repeat("x", maxText+1)); err == nil {
		t.Fatal("long accepted")
	}
}

func TestNew(t *testing.T) {
	c, err := New(1, 2, 3, "bob", " yo ")
	if err != nil || c.PostID != 1 || c.ParentID != 2 || c.UserID != 3 || c.Author != "bob" || c.Text != "yo" {
		t.Fatalf("got %+v %v", c, err)
	}
	if _, err := New(1, 0, 3, "bob", ""); err == nil {
		t.Fatal("empty text accepted")
	}
	if _, err := New(1, -1, 3, "bob", "x"); err == nil {
		t.Fatal("negative parent accepted")
	}
}

func TestPaginated(t *testing.T) {
	items, meta := Page{Items: []Comment{{ID: 1}}, Total: 1, Page: 1, Limit: 10, NextCursor: "n"}.Paginated()
	if len(items.([]Comment)) != 1 || meta == nil {
		t.Fatal("bad paginated")
	}
}
