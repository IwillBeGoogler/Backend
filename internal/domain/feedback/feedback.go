// Package feedback holds bug reports and ideas sent by readers.
package feedback

import (
	"context"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

type Kind string

const (
	KindBug   Kind = "bug"
	KindIdea  Kind = "idea"
	KindOther Kind = "other"
)

type Status string

const (
	StatusOpen      Status = "open"
	StatusResolved  Status = "resolved"  // acted on
	StatusDismissed Status = "dismissed" // no action needed
)

const (
	maxMessage = 5000
	maxEmail   = 254
	maxURL     = 2000
	maxAgent   = 500
	maxVersion = 100
	maxErrMsg  = 2000
	maxErrStk  = 10000
)

type Feedback struct {
	ID int `json:"id"`
	// UserID and Username are empty (0, "") for anonymous senders.
	UserID       int        `json:"user_id"`
	Username     string     `json:"username"`
	Kind         Kind       `json:"kind"`
	Message      string     `json:"message"`
	ContactEmail string     `json:"contact_email"`
	PageURL      string     `json:"page_url"`
	UserAgent    string     `json:"user_agent"`
	AppVersion   string     `json:"app_version"`
	ErrorMessage string     `json:"error_message"`
	ErrorStack   string     `json:"error_stack"`
	Status       Status     `json:"status"`
	ResolvedBy   int        `json:"resolved_by"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Page struct {
	Items []Feedback `json:"items"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Limit int        `json:"limit"`
}

// Filter narrows a list; empty fields match everything.
type Filter struct {
	Status Status
	Kind   Kind
	Paging domain.Paging
}

// Input is what a sender provides.
type Input struct {
	Kind         Kind
	Message      string
	ContactEmail string
	PageURL      string
	UserAgent    string
	AppVersion   string
	ErrorMessage string
	ErrorStack   string
}

// New validates input and builds an unsaved open entry. userID is 0 for an
// anonymous sender. The page, agent and error fields are diagnostics: too
// long ones are cut, not refused.
func New(userID int, in Input) (Feedback, error) {
	switch in.Kind {
	case KindBug, KindIdea, KindOther:
	default:
		return Feedback{}, domain.Invalid("kind: bug, idea or other")
	}
	message := strings.TrimSpace(in.Message)
	if n := utf8.RuneCountInString(message); n == 0 || n > maxMessage {
		return Feedback{}, domain.Invalid("message: 1 to 5000 characters")
	}
	email := strings.TrimSpace(in.ContactEmail)
	if userID != 0 {
		email = "" // the account already says who sent it
	}
	if email != "" {
		if len(email) > maxEmail {
			return Feedback{}, domain.Invalid("invalid email")
		}
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
			return Feedback{}, domain.Invalid("invalid email")
		}
	}
	return Feedback{
		UserID: userID, Kind: in.Kind, Message: message, ContactEmail: email,
		PageURL: cut(in.PageURL, maxURL), UserAgent: cut(in.UserAgent, maxAgent), AppVersion: cut(in.AppVersion, maxVersion),
		ErrorMessage: cut(in.ErrorMessage, maxErrMsg), ErrorStack: cut(in.ErrorStack, maxErrStk), Status: StatusOpen,
	}, nil
}

// cut shortens s to at most n characters.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// ValidateKind checks a list filter; empty means all.
func ValidateKind(k Kind) error {
	if k != "" && k != KindBug && k != KindIdea && k != KindOther {
		return domain.Invalid("kind: bug, idea or other")
	}
	return nil
}

// ValidateStatus checks a status; a list filter may also be empty.
func ValidateStatus(s Status, allowEmpty bool) error {
	switch s {
	case StatusOpen, StatusResolved, StatusDismissed:
		return nil
	case "":
		if allowEmpty {
			return nil
		}
	}
	return domain.Invalid("status: open, resolved or dismissed")
}

// Repository returns domain.ErrNotFound for a missing entry.
type Repository interface {
	Create(ctx context.Context, f Feedback) (Feedback, error)
	Get(ctx context.Context, id int) (Feedback, error)
	// List filters by status and kind, newest first.
	List(ctx context.Context, f Filter) (Page, error)
	// SetStatus records a moderator's decision; open reopens the entry.
	SetStatus(ctx context.Context, id int, s Status, moderatorID int) (Feedback, error)
}

func (p Page) Paginated() (any, any) { return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "") }
