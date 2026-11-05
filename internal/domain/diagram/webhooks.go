package diagram

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strconv"
	"time"
)

// Webhook events, as in the Node server. "ping" is only ever sent by the
// test route.
const (
	WebhookFileCreated    = "file.created"
	WebhookFileUpdated    = "file.updated"
	WebhookFileDeleted    = "file.deleted"
	WebhookLibraryUpdated = "library.updated"
	WebhookPing           = "ping"
)

// WebhookEvents lists the events an endpoint can subscribe to.
var WebhookEvents = []string{WebhookFileCreated, WebhookFileUpdated, WebhookFileDeleted, WebhookLibraryUpdated}

// ID prefixes of webhooks, deliveries and events.
const (
	PrefixWebhook  = "whk"
	PrefixDelivery = "dlv"
	PrefixEvent    = "evt"
)

// MaxWebhookURL is the longest endpoint URL.
const MaxWebhookURL = 2000

// Webhook is an endpoint of a project.
type Webhook struct {
	ID        string
	ProjectID string
	URL       string
	// Secret signs the deliveries; it is shown once, when the webhook is made.
	Secret    string
	Events    []string
	Active    bool
	CreatedBy int
	CreatedAt time.Time
}

// Subscribed reports whether the webhook is active and wants event.
func (w Webhook) Subscribed(event string) bool { return w.Active && slices.Contains(w.Events, event) }

// Delivery statuses.
const (
	DeliveryPending = "pending"
	DeliveryOK      = "ok"
	DeliveryFailed  = "failed"
)

// Delivery is one event sent (or to be sent) to one webhook.
type Delivery struct {
	ID            string
	WebhookID     string
	Event         string
	Payload       string
	Attempt       int
	Status        string
	ResponseCode  *int
	Error         string
	NextAttemptAt *time.Time
	CreatedAt     time.Time
}

// WebhookRepository stores webhooks and their delivery log. It returns
// domain.ErrNotFound for a missing webhook or delivery.
type WebhookRepository interface {
	CreateWebhook(ctx context.Context, w Webhook) error
	// Webhooks lists a project's webhooks, newest first.
	Webhooks(ctx context.Context, projectID string) ([]Webhook, error)
	// Webhook returns a webhook of the project.
	Webhook(ctx context.Context, projectID, id string) (Webhook, error)
	// WebhookByID returns a webhook whatever its project (the worker).
	WebhookByID(ctx context.Context, id string) (Webhook, error)
	// UpdateWebhook stores url, events and active.
	UpdateWebhook(ctx context.Context, w Webhook) error
	// DeleteWebhook removes the webhook and its deliveries.
	DeleteWebhook(ctx context.Context, id string) error
	// ActiveWebhooks lists a project's active webhooks.
	ActiveWebhooks(ctx context.Context, projectID string) ([]Webhook, error)

	CreateDelivery(ctx context.Context, d Delivery) error
	Delivery(ctx context.Context, id string) (Delivery, error)
	UpdateDelivery(ctx context.Context, d Delivery) error
	// Deliveries lists the newest deliveries of a webhook.
	Deliveries(ctx context.Context, webhookID string, limit int) ([]Delivery, error)
}

// NewWebhookSecret makes a signing secret: "whsec_" + 24 random bytes.
func NewWebhookSecret() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b[:])
}

// SignatureHeader and the other headers of a delivery.
const (
	HeaderEvent     = "X-SDD-Event"
	HeaderDelivery  = "X-SDD-Delivery"
	HeaderTimestamp = "X-SDD-Timestamp"
	HeaderSignature = "X-SDD-Signature"
)

// Sign is "sha256=" + the hex HMAC-SHA256 of "<timestamp>.<body>" with the
// webhook's secret (timestamp in unix seconds).
func Sign(secret string, timestamp int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// VerifySignature checks an X-SDD-Signature header in constant time. With
// a non-zero now, the timestamp must be within tolerance (default 5
// minutes) of it.
func VerifySignature(secret string, timestamp int64, body []byte, header string, now time.Time, tolerance time.Duration) bool {
	if !now.IsZero() {
		if tolerance <= 0 {
			tolerance = 5 * time.Minute
		}
		d := now.Sub(time.Unix(timestamp, 0))
		if d < -tolerance || d > tolerance {
			return false
		}
	}
	return hmac.Equal([]byte(Sign(secret, timestamp, body)), []byte(header))
}

// MaxDeliveryAttempts is how often a delivery is tried in all: the first
// attempt and four retries.
const MaxDeliveryAttempts = 5

// RetryDelay is the wait before the next attempt after attemptsMade
// attempts, growing by x6 (10s, 1m, 6m, 36m); jitter in [-1, 1] scales it
// by up to 20%. ok is false once the attempts are used up.
func RetryDelay(attemptsMade int, jitter float64) (d time.Duration, ok bool) {
	if attemptsMade < 1 || attemptsMade >= MaxDeliveryAttempts {
		return 0, false
	}
	base := 10 * time.Second
	for i := 1; i < attemptsMade; i++ {
		base *= 6
	}
	jitter = max(-1, min(1, jitter))
	return time.Duration(float64(base) * (1 + 0.2*jitter)), true
}

// Outcome classes of a response status.
const (
	StatusOK    = "ok"
	StatusRetry = "retry"
	StatusFail  = "fail"
)

// ClassifyStatus says what an HTTP status means for a delivery: 2xx is
// done, 5xx / 429 / 408 are worth retrying, anything else is final.
func ClassifyStatus(status int) string {
	switch {
	case status >= 200 && status < 300:
		return StatusOK
	case status >= 500, status == 429, status == 408:
		return StatusRetry
	}
	return StatusFail
}

// MCPAudit is one MCP tool call in the audit log.
type MCPAudit struct {
	ID         string
	UserID     int
	TokenID    string
	Tool       string
	ProjectID  string
	FileID     string
	Status     string // ok | error | denied | rate_limited
	Detail     string
	DurationMS int
	CreatedAt  time.Time
}

// MCP audit statuses.
const (
	AuditOK          = "ok"
	AuditError       = "error"
	AuditDenied      = "denied"
	AuditRateLimited = "rate_limited"
)

// PrefixAudit is the id prefix of audit entries.
const PrefixAudit = "aud"

// MCPAuditRepository keeps the audit trail of MCP calls.
type MCPAuditRepository interface {
	AddAudit(ctx context.Context, a MCPAudit) error
	// Audit lists a user's entries, newest first.
	Audit(ctx context.Context, userID, limit int) ([]MCPAudit, error)
}
