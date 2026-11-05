package core

import "testing"

// 5.742821606144491 is what Node prints for Math.hypot(3.3, 4.7).
func TestHypotMatchesV8(t *testing.T) {
	if got := hypot2(3.3, 4.7); got != 5.742821606144491 {
		t.Errorf("hypot2 = %v", got)
	}
	if hypot2(0, 0) != 0 || hypot2(-3, 4) != 5 {
		t.Error("hypot2 basics")
	}
}
