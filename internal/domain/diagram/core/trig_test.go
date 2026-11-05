package core

import (
	"math"
	"testing"
)

// Values printed by Node (V8) for Math.cos / Math.sin of i/15*2*pi.
func TestTrigMatchesV8(t *testing.T) {
	cases := []struct {
		i        int
		cos, sin float64
	}{
		{5, -0.4999999999999998, 0.8660254037844387},
		{7, -0.9781476007338057, 0.20791169081775931},
		{10, -0.5000000000000004, -0.8660254037844385},
		{2, 0.6691306063588582, 0.7431448254773942},
	}
	for _, c := range cases {
		a := float64(c.i) / 15 * math.Pi * 2
		if got := jsCos(a); got != c.cos {
			t.Errorf("cos(%d) = %v, want %v", c.i, got, c.cos)
		}
		if got := jsSin(a); got != c.sin {
			t.Errorf("sin(%d) = %v, want %v", c.i, got, c.sin)
		}
	}
}
