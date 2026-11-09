// Package webhooks sends diagram webhook deliveries over HTTP
// (application.WebhookSender). The target is checked and the connection is
// made to the checked address (netguard), redirects are not followed, the
// request has a timeout and only a small part of the answer is read.
package webhooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/netguard"
)

const (
	// Timeout bounds one delivery, connect to last byte.
	Timeout = 5 * time.Second
	// MaxResponse is how much of an answer is read (the rest is dropped).
	MaxResponse = 64 << 10
	userAgent   = "system-design-diagram-webhooks/1"
)

// No labels carry webhook or project ids: cardinality stays fixed.
var (
	attempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iwillbegoogler_webhook_attempts_total",
		Help: "Webhook delivery attempts by result: 2xx, 3xx, 4xx, 5xx, timeout, blocked or error.",
	}, []string{"result"})
	attemptSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "iwillbegoogler_webhook_attempt_seconds",
		Help:    "Duration of one webhook delivery attempt.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5},
	})
)

func resultOf(status int, err error) string {
	switch {
	case errors.Is(err, netguard.ErrBlocked):
		return "blocked"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case err != nil:
		return "error"
	case status >= 200 && status < 600:
		return fmt.Sprintf("%dxx", status/100)
	}
	return "error"
}

// CountBlocked records an attempt refused before it was sent.
func CountBlocked() { attempts.WithLabelValues("blocked").Inc() }

// Sender posts deliveries.
type Sender struct {
	client       *http.Client
	AllowPrivate bool
	lookup       netguard.Lookup
}

// New builds a sender; allowPrivate (WEBHOOK_ALLOW_PRIVATE) lifts the
// address check. lookup nil uses the system resolver.
func New(allowPrivate bool, lookup netguard.Lookup) *Sender {
	d := netguard.Dialer{AllowPrivate: allowPrivate, Lookup: lookup, Timeout: Timeout}
	return &Sender{AllowPrivate: allowPrivate, lookup: lookup, client: &http.Client{
		Timeout: Timeout,
		Transport: &http.Transport{
			Proxy:                 nil, // a proxy would resolve the name itself, past the check
			DialContext:           d.DialContext,
			TLSHandshakeTimeout:   Timeout,
			ResponseHeaderTimeout: Timeout,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// CheckURL is the save-time check of an endpoint URL.
func (s *Sender) CheckURL(ctx context.Context, raw string) error {
	_, err := netguard.CheckURL(ctx, raw, s.AllowPrivate, s.lookup)
	return err
}

// Send posts the delivery and returns the response status.
func (s *Sender) Send(ctx context.Context, r application.WebhookRequest) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", application.ErrWebhookURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set(diagram.HeaderEvent, r.Event)
	req.Header.Set(diagram.HeaderDelivery, r.DeliveryID)
	req.Header.Set(diagram.HeaderTimestamp, fmt.Sprint(r.Timestamp))
	req.Header.Set(diagram.HeaderSignature, r.Signature)
	start := time.Now()
	res, err := s.client.Do(req)
	attemptSeconds.Observe(time.Since(start).Seconds())
	if err != nil {
		attempts.WithLabelValues(resultOf(0, err)).Inc()
		if errors.Is(err, netguard.ErrBlocked) {
			return 0, fmt.Errorf("%w: %v", application.ErrWebhookURL, err)
		}
		return 0, err
	}
	defer res.Body.Close()
	attempts.WithLabelValues(resultOf(res.StatusCode, nil)).Inc()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, MaxResponse))
	return res.StatusCode, nil
}
