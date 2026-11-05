package postgres

import (
	"context"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/feedback"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FeedbackRepo struct{ db *pgxpool.Pool }

func NewFeedbackRepo(db *pgxpool.Pool) *FeedbackRepo { return &FeedbackRepo{db: db} }

const feedbackCols = `f.id, COALESCE(f.user_id, 0), COALESCE(u.username, ''), f.kind, f.message, f.contact_email, f.page_url, f.user_agent,
	f.app_version, f.error_message, f.error_stack, f.status, COALESCE(f.resolved_by, 0), f.resolved_at, f.created_at`

const feedbackFrom = ` FROM feedback f LEFT JOIN users u ON u.id = f.user_id`

func scanFeedback(row pgx.Row, extra ...any) (feedback.Feedback, error) {
	var f feedback.Feedback
	dest := append([]any{&f.ID, &f.UserID, &f.Username, &f.Kind, &f.Message, &f.ContactEmail, &f.PageURL, &f.UserAgent,
		&f.AppVersion, &f.ErrorMessage, &f.ErrorStack, &f.Status, &f.ResolvedBy, &f.ResolvedAt, &f.CreatedAt}, extra...)
	return f, notFound(row.Scan(dest...))
}

func (r *FeedbackRepo) Create(ctx context.Context, f feedback.Feedback) (feedback.Feedback, error) {
	var id int
	err := r.db.QueryRow(ctx, `INSERT INTO feedback (user_id, kind, message, contact_email, page_url, user_agent, app_version, error_message, error_stack)
		VALUES (NULLIF($1, 0), $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		f.UserID, f.Kind, f.Message, f.ContactEmail, f.PageURL, f.UserAgent, f.AppVersion, f.ErrorMessage, f.ErrorStack).Scan(&id)
	if err != nil {
		return f, err
	}
	return r.Get(ctx, id)
}

func (r *FeedbackRepo) Get(ctx context.Context, id int) (feedback.Feedback, error) {
	return scanFeedback(r.db.QueryRow(ctx, `SELECT `+feedbackCols+feedbackFrom+` WHERE f.id = $1`, id))
}

func (r *FeedbackRepo) List(ctx context.Context, f feedback.Filter) (feedback.Page, error) {
	page := feedback.Page{Items: []feedback.Feedback{}, Page: f.Paging.Page, Limit: f.Paging.Limit}
	rows, err := r.db.Query(ctx, `SELECT `+feedbackCols+`, count(*) OVER ()`+feedbackFrom+`
		WHERE ($1 = '' OR f.status = $1) AND ($2 = '' OR f.kind = $2) ORDER BY f.id DESC LIMIT $3 OFFSET $4`,
		f.Status, f.Kind, f.Paging.Limit, f.Paging.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		fb, err := scanFeedback(rows, &page.Total)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, fb)
	}
	return page, rows.Err()
}

func (r *FeedbackRepo) SetStatus(ctx context.Context, id int, s feedback.Status, moderatorID int) (feedback.Feedback, error) {
	if s == feedback.StatusOpen {
		moderatorID = 0 // reopening clears who decided
	}
	tag, err := r.db.Exec(ctx, `UPDATE feedback SET status = $2, resolved_by = NULLIF($3, 0),
		resolved_at = CASE WHEN $3 = 0 THEN NULL ELSE now() END WHERE id = $1`, id, s, moderatorID)
	if err != nil {
		return feedback.Feedback{}, err
	}
	if tag.RowsAffected() == 0 {
		return feedback.Feedback{}, domain.ErrNotFound
	}
	return r.Get(ctx, id)
}
