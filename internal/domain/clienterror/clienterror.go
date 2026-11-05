// Package clienterror holds JavaScript errors reported by the web app,
// grouped by fingerprint. Entries carry no user id and no IP address.
package clienterror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
)

type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved" // fixed; a new occurrence reopens it
	StatusIgnored  Status = "ignored"  // noise; stays ignored however often it recurs
)

const (
	maxMessage = 1000
	maxStack   = 8000
	maxURL     = 2000
	maxAgent   = 500

	// MaxRows caps the table: past it, new fingerprints are dropped.
	MaxRows = 5000
	// Retention is how long an entry is kept after it was last seen.
	Retention = 90 * 24 * time.Hour
)

type ClientError struct {
	ID          int       `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	Message     string    `json:"message"`
	Stack       string    `json:"stack"`
	PageURL     string    `json:"page_url"`
	UserAgent   string    `json:"user_agent"`
	Count       int       `json:"count"`
	Status      Status    `json:"status"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

type Page struct {
	Items []ClientError `json:"items"`
	Total int           `json:"total"`
	Page  int           `json:"page"`
	Limit int           `json:"limit"`
}

// Filter narrows a list; an empty status matches everything.
type Filter struct {
	Status Status
	Paging domain.Paging
}

// Input is what the browser sends. Any fingerprint it might send is ignored.
type Input struct {
	Message   string
	Stack     string
	PageURL   string
	UserAgent string
}

// New scrubs and cuts the input and computes the fingerprint on the server.
// Too long fields are cut, not refused; an empty message is refused.
func New(in Input) (ClientError, error) {
	message := cut(Scrub(cut(strings.TrimSpace(in.Message), 4*maxMessage)), maxMessage)
	if message == "" {
		return ClientError{}, domain.Invalid("message: required")
	}
	stack := cut(Scrub(cut(strings.TrimSpace(in.Stack), 2*maxStack)), maxStack)
	return ClientError{
		Fingerprint: Fingerprint(message, stack),
		Message:     message,
		Stack:       stack,
		PageURL:     cut(Scrub(cut(strings.TrimSpace(in.PageURL), 2*maxURL)), maxURL),
		UserAgent:   cut(strings.TrimSpace(in.UserAgent), maxAgent),
		Status:      StatusOpen,
	}, nil
}

var (
	urlWithTail = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>)?#]*[?#][^\s"'<>)]*`)
	posSuffix   = regexp.MustCompile(`(:\d+){1,2}$`)
	bearer      = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	jwt         = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)
	email       = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	longRun     = regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`)
)

const redacted = "[redacted]"

// Scrub removes what could identify a person or open an account: query
// strings and fragments of URLs (a trailing :line:column is kept), emails,
// JWT-looking strings, Bearer tokens, and hex or base64 runs of 32+ chars.
func Scrub(s string) string {
	s = urlWithTail.ReplaceAllStringFunc(s, func(u string) string {
		i := strings.IndexAny(u, "?#")
		pos := posSuffix.FindString(u)
		return u[:i] + pos
	})
	s = bearer.ReplaceAllString(s, redacted)
	s = jwt.ReplaceAllString(s, redacted)
	s = email.ReplaceAllString(s, redacted)
	// A long run with no digit is a long identifier or word, not a secret.
	return longRun.ReplaceAllStringFunc(s, func(m string) string {
		if strings.ContainsAny(m, "0123456789") {
			return redacted
		}
		return m
	})
}

var (
	assetHash = regexp.MustCompile(`[-.][A-Za-z0-9_-]{6,16}\.(m?js|css)\b`)
	lineCol   = regexp.MustCompile(`:\d+(:\d+)?`)
	spaces    = regexp.MustCompile(`\s+`)
)

// Fingerprint groups the same error across builds and occurrences: sha256 of
// the message and the first three stack lines, with line and column numbers
// and asset content hashes (index-Dk3jf92K.js) removed. Pass scrubbed text.
func Fingerprint(message, stack string) string {
	h := sha256.New()
	h.Write([]byte(spaces.ReplaceAllString(strings.TrimSpace(message), " ")))
	n := 0
	for _, line := range strings.Split(stack, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if n == 3 {
			break
		}
		n++
		line = assetHash.ReplaceAllString(line, ".$1")
		line = lineCol.ReplaceAllString(line, "")
		h.Write([]byte{'\n'})
		h.Write([]byte(spaces.ReplaceAllString(line, " ")))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cut shortens s to at most n characters.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// ValidateStatus checks a status; a list filter may also be empty.
func ValidateStatus(s Status, allowEmpty bool) error {
	switch s {
	case StatusOpen, StatusResolved, StatusIgnored:
		return nil
	case "":
		if allowEmpty {
			return nil
		}
	}
	return domain.Invalid("status: open, resolved or ignored")
}

// Repository returns domain.ErrNotFound for a missing entry.
type Repository interface {
	// Record stores one occurrence. A known fingerprint counts up, sets
	// last_seen, page and agent, and reopens a resolved entry (a regression)
	// but leaves an ignored one alone. A new fingerprint is dropped when the
	// table already holds maxRows rows.
	Record(ctx context.Context, e ClientError, maxRows int) error
	// List filters by status, newest last_seen first.
	List(ctx context.Context, f Filter) (Page, error)
	SetStatus(ctx context.Context, id int, s Status) (ClientError, error)
	// PurgeBefore deletes entries last seen before t.
	PurgeBefore(ctx context.Context, t time.Time) (int64, error)
}

func (p Page) Paginated() (any, any) { return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "") }
