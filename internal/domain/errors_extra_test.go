package domain

import (
	"errors"
	"testing"
)

func TestValidationError(t *testing.T) {
	err := Invalid("bad thing")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Error() != "bad thing" {
		t.Fatalf("%v", err)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := EncodeCursor(5, 42)
	got, err := DecodeCursor(c, 2)
	if err != nil || len(got) != 2 || got[0] != 5 || got[1] != 42 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := DecodeCursor("!!!", 1); err != ErrBadCursor {
		t.Error("bad base64")
	}
	if _, err := DecodeCursor(c, 3); err != ErrBadCursor {
		t.Error("wrong part count")
	}
	for _, bad := range []string{EncodeCursor(0), EncodeCursor(-3)} {
		if _, err := DecodeCursor(bad, 1); err != ErrBadCursor {
			t.Errorf("nonpositive %q", bad)
		}
	}
	// non-numeric field
	if _, err := DecodeCursor("YWJj", 1); err != ErrBadCursor {
		t.Error("non numeric")
	}
}
