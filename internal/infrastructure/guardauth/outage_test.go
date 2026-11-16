package guardauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apikeydomain "github.com/bakhod1r/guard/apikey/domain"
	identitydomain "github.com/bakhod1r/guard/identity/domain"
	sessiondomain "github.com/bakhod1r/guard/session/domain"
	"github.com/redis/go-redis/v9"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

func TestCredentialRejectedOnlyForBadCredentials(t *testing.T) {
	for _, err := range []error{sessiondomain.ErrSessionNotFound, sessiondomain.ErrSessionExpired, apikeydomain.ErrKeyInvalid,
		apikeydomain.ErrKeyNotFound, identitydomain.ErrUserBlocked, fmt.Errorf("wrapped: %w", sessiondomain.ErrSessionExpired)} {
		if !CredentialRejected(err) {
			t.Errorf("%v: want rejected", err)
		}
		if !errors.Is(authErr(err), domain.ErrUnauthorized) {
			t.Errorf("%v: want 401", err)
		}
	}
	// an outage says nothing about the credential: never 401, or clients sign out
	for _, err := range []error{redis.ErrClosed, context.Canceled, context.DeadlineExceeded, errors.New("dial tcp: connection refused")} {
		if CredentialRejected(err) {
			t.Errorf("%v: must not count as a rejected credential", err)
		}
		if got := authErr(err); errors.Is(got, domain.ErrUnauthorized) || !errors.Is(got, err) {
			t.Errorf("%v: authErr = %v", err, got)
		}
	}
}

func TestBearerOrKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if bearerOrKey(r) != "" {
		t.Error("no credential")
	}
	r.Header.Set("Authorization", "Bearer  abc ")
	if got := bearerOrKey(r); got != "abc" {
		t.Errorf("bearer = %q", got)
	}
	r.Header.Set("X-API-Key", "key1")
	if got := bearerOrKey(r); got != "key1" {
		t.Errorf("api key wins = %q", got)
	}
}
