package relation

import "testing"

func TestKindValid(t *testing.T) {
	if !Block.Valid() || !Mute.Valid() || Kind("x").Valid() || Kind("").Valid() {
		t.Fatal("Valid wrong")
	}
	if ErrBlocked == nil {
		t.Fatal("nil err")
	}
}
