package diagram

import "testing"

func TestValidateClaps(t *testing.T) {
	for n, ok := range map[int]bool{-1: false, 0: false, 1: true, 50: true, 51: false} {
		if err := ValidateClaps(n); (err == nil) != ok {
			t.Errorf("ValidateClaps(%d) = %v", n, err)
		}
	}
}
