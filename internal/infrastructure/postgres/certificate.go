package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/challenge"
)

// A private challenge's id is never given out.
const certificateSelect = `SELECT ct.id, c.id, ct.user_id, ct.holder_name, ct.username, ct.title, ct.days, ct.days_done,
	ct.best_streak, ct.start_date::text, ct.end_date::text, ct.issued_at, ct.revoked_at, ct.revoke_reason`

const certificateFrom = ` FROM challenge_certificates ct
	LEFT JOIN challenges c ON c.id = ct.challenge_id AND c.visibility = 'public' `

func scanCertificate(row pgx.Row, extra ...any) (challenge.Certificate, error) {
	var c challenge.Certificate
	dest := append([]any{&c.ID, &c.ChallengeID, &c.UserID, &c.HolderName, &c.Username, &c.Title, &c.Days, &c.DaysDone,
		&c.BestStreak, &c.StartDate, &c.EndDate, &c.IssuedAt, &c.RevokedAt, &c.RevokeReason}, extra...)
	return c, notFound(row.Scan(dest...))
}

// Certificate hides the certificates of accounts awaiting deletion.
func (r *ChallengeRepo) Certificate(ctx context.Context, id string) (challenge.Certificate, error) {
	return scanCertificate(r.db.QueryRow(ctx, certificateSelect+certificateFrom+
		`JOIN users u ON u.id = ct.user_id AND u.deleted_at IS NULL WHERE ct.id = $1`, id))
}

func (r *ChallengeRepo) CertificateOf(ctx context.Context, challengeID int) (challenge.Certificate, error) {
	return scanCertificate(r.db.QueryRow(ctx, certificateSelect+certificateFrom+`WHERE ct.challenge_id = $1`, challengeID))
}

// IssueCertificate stores c for the challenge unless it already has one, and
// returns the stored certificate.
func (r *ChallengeRepo) IssueCertificate(ctx context.Context, challengeID int, c challenge.Certificate) (challenge.Certificate, bool, error) {
	tag, err := r.db.Exec(ctx, `INSERT INTO challenge_certificates
			(id, challenge_id, user_id, holder_name, username, title, days, days_done, best_streak, start_date, end_date, issued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::date, $11::date, $12) ON CONFLICT (challenge_id) DO NOTHING`,
		c.ID, challengeID, c.UserID, c.HolderName, c.Username, c.Title, c.Days, c.DaysDone, c.BestStreak,
		c.StartDate, c.EndDate, c.IssuedAt)
	switch {
	case pgCode(err) == pgForeignKeyViolation:
		return challenge.Certificate{}, false, domain.ErrNotFound
	case err != nil:
		return challenge.Certificate{}, false, err
	}
	got, err := r.CertificateOf(ctx, challengeID)
	return got, tag.RowsAffected() == 1, err
}

// ListCertificates lists all certificates, newest first.
func (r *ChallengeRepo) ListCertificates(ctx context.Context, p domain.Paging) (challenge.CertificatePage, error) {
	page := challenge.CertificatePage{Items: []challenge.Certificate{}, Page: p.Page, Limit: p.Limit}
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM challenge_certificates`).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := r.db.Query(ctx, certificateSelect+certificateFrom+
		`ORDER BY ct.issued_at DESC, ct.id LIMIT $1 OFFSET $2`, p.Limit, p.Offset())
	if err != nil {
		return page, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (challenge.Certificate, error) {
		return scanCertificate(row)
	})
	if len(items) > 0 {
		page.Items = items
	}
	return page, err
}

// RevokeCertificate marks it revoked; the first revocation is kept.
func (r *ChallengeRepo) RevokeCertificate(ctx context.Context, id, reason string) (challenge.Certificate, error) {
	tag, err := r.db.Exec(ctx, `UPDATE challenge_certificates SET revoked_at = now(), revoke_reason = $2
		WHERE id = $1 AND revoked_at IS NULL`, id, reason)
	if err != nil {
		return challenge.Certificate{}, err
	}
	// Unlike Certificate, this also finds one whose owner is awaiting deletion.
	c, err := scanCertificate(r.db.QueryRow(ctx, certificateSelect+certificateFrom+`WHERE ct.id = $1`, id))
	if err == nil && tag.RowsAffected() == 0 {
		err = challenge.ErrCertificateRevoked
	}
	return c, err
}
