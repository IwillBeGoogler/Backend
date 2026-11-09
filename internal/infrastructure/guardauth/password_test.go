package guardauth

import (
	"errors"
	"testing"

	identitydomain "github.com/bakhod1r/guard/identity/domain"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

func TestWeakPasswordErr(t *testing.T) {
	err := identitydomain.ValidatePasswordFor("a@b.uz", "password123")
	if err == nil {
		t.Skip("password123 not in common list")
	}
	got := weakPasswordErr(err)
	if !errors.Is(got, application.ErrWeakPassword) {
		t.Fatalf("got %v, want ErrWeakPassword", got)
	}
	if got.Error() != "weak password: password is too common" {
		t.Errorf("detail = %q", got.Error())
	}
	other := errors.New("db down")
	if weakPasswordErr(other) != other {
		t.Error("non-policy error must pass through")
	}
}
