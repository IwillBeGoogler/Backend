package challenge

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

const (
	// CertificateMinDays is the shortest challenge that earns a certificate.
	CertificateMinDays = 30

	certificateIDLen = 16
	maxRevokeReason  = 500
)

var (
	ErrCertificateShort      = domain.Invalid("certificates need a challenge of at least 30 days")
	ErrCertificateRunning    = domain.Invalid("challenge has not ended yet")
	ErrCertificateIncomplete = domain.Invalid("at least 90% of the days must be done")
	ErrCertificateRevoked    = domain.Invalid("certificate is already revoked")
)

// Certificate is a frozen record of a completed challenge.
type Certificate struct {
	ID string `json:"id"`
	// ChallengeID is left out once the challenge is deleted or while it is private.
	ChallengeID  *int       `json:"challenge_id,omitempty"`
	UserID       int        `json:"user_id"`
	HolderName   string     `json:"holder_name"`
	Username     string     `json:"username"`
	Title        string     `json:"title"`
	Days         int        `json:"days"`
	DaysDone     int        `json:"days_done"`
	BestStreak   int        `json:"best_streak"`
	StartDate    string     `json:"start_date"`
	EndDate      string     `json:"end_date"`
	IssuedAt     time.Time  `json:"issued_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokeReason string     `json:"revoke_reason,omitempty"`
}

// CertificatePage is one page of certificates.
type CertificatePage struct {
	Items []Certificate
	Page  int
	Limit int
	Total int
}

func (p CertificatePage) Paginated() (any, any) {
	return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "")
}

// Eligible reports why a challenge of days with these stats earns no
// certificate yet, or nil. It must have ended, or be on its last day with
// every day done.
func Eligible(days int, s Stats) error {
	switch {
	case days < CertificateMinDays:
		return ErrCertificateShort
	case !s.Ended && (s.Today != days || s.DaysDone < days):
		return ErrCertificateRunning
	case s.DaysDone*10 < days*9:
		return ErrCertificateIncomplete
	}
	return nil
}

// NewCertificateID returns an unguessable 16 character lowercase base32 id.
func NewCertificateID() string {
	b := make([]byte, certificateIDLen*5/8)
	_, _ = rand.Read(b) // never fails
	return strings.ToLower(base32.StdEncoding.EncodeToString(b))
}

// ValidCertificateID reports whether id has the shape of a certificate id.
func ValidCertificateID(id string) bool {
	return len(id) == certificateIDLen && strings.Trim(id, "abcdefghijklmnopqrstuvwxyz234567") == ""
}

// RevokeReason trims and checks the reason for revoking a certificate.
func RevokeReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return "", domain.Invalid("reason required")
	case utf8.RuneCountInString(reason) > maxRevokeReason || strings.ContainsRune(reason, 0):
		return "", domain.Invalid("reason too long or malformed")
	}
	return reason, nil
}
