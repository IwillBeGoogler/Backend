package revision

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestContentEqual(t *testing.T) {
	a := Content{"t", "s", "b"}
	if !a.Equal(Content{"t", "s", "b"}) || a.Equal(Content{"t", "s", "x"}) {
		t.Fatal("Equal wrong")
	}
}

func TestLines(t *testing.T) {
	cases := []struct {
		a, b string
		want []Op
	}{
		{"", "", []Op{}},
		{"a\nb", "a\nb", []Op{{"=", "a"}, {"=", "b"}}},
		{"", "x", []Op{{"+", "x"}}},
		{"x", "", []Op{{"-", "x"}}},
		{"a\nb\nc", "a\nB\nc", []Op{{"=", "a"}, {"-", "b"}, {"+", "B"}, {"=", "c"}}},
		// LCS middle: both branches of the walk
		{"a\nb\nc\nd", "b\nx\nd", []Op{{"-", "a"}, {"=", "b"}, {"-", "c"}, {"+", "x"}, {"=", "d"}}},
		{"b\nz", "a\nb", []Op{{"+", "a"}, {"=", "b"}, {"-", "z"}}},
		{"p\na\nq", "a", []Op{{"-", "p"}, {"=", "a"}, {"-", "q"}}},
		{"x\ny", "y\nz", []Op{{"-", "x"}, {"=", "y"}, {"+", "z"}}},
		{"p\nq\nr", "q\nr\ns\nt", []Op{{"-", "p"}, {"=", "q"}, {"=", "r"}, {"+", "s"}, {"+", "t"}}},
	}
	for _, c := range cases {
		if got := Lines(c.a, c.b); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Lines(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestLinesTrailing(t *testing.T) {
	// middle ends with leftovers on the x side and on the y side
	got := Lines("a\nb\nc", "a\nd")
	if len(got) == 0 || got[0] != (Op{"=", "a"}) {
		t.Fatalf("%v", got)
	}
	got = Lines("a\nb", "a\nb\nc\nd")
	want := []Op{{"=", "a"}, {"=", "b"}, {"+", "c"}, {"+", "d"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestLinesHuge(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < 2100; i++ {
		fmt.Fprintf(&a, "a%d\n", i)
		fmt.Fprintf(&b, "b%d\n", i)
	}
	got := Lines(a.String(), b.String())
	rem, add := 0, 0
	for _, o := range got {
		switch o.Op {
		case "-":
			rem++
		case "+":
			add++
		}
	}
	if rem != 2100 || add != 2100 {
		t.Fatalf("rem=%d add=%d", rem, add)
	}
	if got[0].Op != "-" {
		t.Fatal("fallback order wrong")
	}
}

func TestCompare(t *testing.T) {
	d := Compare(Content{"old", "sub", "a\nb"}, Content{"new", "sub", "a\nc\nd"})
	if d.Added != 3 || d.Removed != 2 {
		t.Fatalf("added=%d removed=%d", d.Added, d.Removed)
	}
	if len(d.Subtitle) != 1 || d.Subtitle[0].Op != "=" {
		t.Fatalf("%v", d.Subtitle)
	}
	d = Compare(Content{}, Content{})
	if d.Added != 0 || d.Removed != 0 {
		t.Fatal("empty diff")
	}
}
