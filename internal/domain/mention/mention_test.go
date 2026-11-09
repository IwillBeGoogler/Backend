package mention

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"hi @Alice and @bob_1!", []string{"alice", "bob_1"}},
		{"@alice @ALICE", []string{"alice"}},
		{"mail me a@example.com", nil},
		{"@@alice", nil},
		{"@ab", nil},
		{"`@code` and ```\n@fenced\n``` @real", []string{"real"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := Parse(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseMax(t *testing.T) {
	var b strings.Builder
	for i := 0; i < Max+5; i++ {
		fmt.Fprintf(&b, "@user%02d ", i)
	}
	if got := Parse(b.String()); len(got) != Max {
		t.Fatalf("len = %d", len(got))
	}
}

func TestNew(t *testing.T) {
	got := New("hi @alice", "hi @alice @bob @carol")
	if !reflect.DeepEqual(got, []string{"bob", "carol"}) {
		t.Fatalf("got %v", got)
	}
	if got := New("@a_b", "@a_b"); got != nil {
		t.Fatalf("got %v", got)
	}
}
