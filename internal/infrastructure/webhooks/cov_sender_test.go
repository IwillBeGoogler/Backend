package webhooks

import (
	"context"
	"errors"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/netguard"
)

func TestCovResultOf(t *testing.T) {
	cases := []struct {
		st   int
		err  error
		want string
	}{
		{0, netguard.ErrBlocked, "blocked"},
		{0, context.DeadlineExceeded, "timeout"},
		{0, errors.New("x"), "error"},
		{204, nil, "2xx"},
		{0, nil, "error"},
		{700, nil, "error"},
	}
	for _, c := range cases {
		if got := resultOf(c.st, c.err); got != c.want {
			t.Errorf("resultOf(%d,%v)=%s want %s", c.st, c.err, got, c.want)
		}
	}
}

func TestCovCountBlocked(t *testing.T) { CountBlocked() }

func TestCovSendBadURL(t *testing.T) {
	_, err := New(false, nil).Send(context.Background(), application.WebhookRequest{URL: "http://a b\x7f"})
	if !errors.Is(err, application.ErrWebhookURL) {
		t.Fatalf("got %v", err)
	}
}
