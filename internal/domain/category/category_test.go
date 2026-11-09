package category

import (
	"strings"
	"testing"
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
