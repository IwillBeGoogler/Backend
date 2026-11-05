package postgres

import (
	"context"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain/clienterror"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ClientErrorRepo struct{ db *pgxpool.Pool }

func NewClientErrorRepo(db *pgxpool.Pool) *ClientErrorRepo { return &ClientErrorRepo{db: db} }

const clientErrorCols = `id, fingerprint, message, stack, page_url, user_agent, count, status, first_seen, last_seen`

func scanClientError(row pgx.Row, extra ...any) (clienterror.ClientError, error) {
	var e clienterror.ClientError
	dest := append([]any{&e.ID, &e.Fingerprint, &e.Message, &e.Stack, &e.PageURL, &e.UserAgent, &e.Count, &e.Status, &e.FirstSeen, &e.LastSeen}, extra...)
	return e, notFound(row.Scan(dest...))
}

// Record is one statement, so concurrent reports of one error cannot lose a
// count. The WHERE keeps known fingerprints counting once the table is full.
func (r *ClientErrorRepo) Record(ctx context.Context, e clienterror.ClientError, maxRows int) error {
	_, err := r.db.Exec(ctx, `INSERT INTO client_errors (fingerprint, message, stack, page_url, user_agent)
		SELECT $1, $2, $3, $4, $5
		WHERE EXISTS (SELECT 1 FROM client_errors WHERE fingerprint = $1)
		   OR (SELECT count(*) FROM client_errors) < $6
		ON CONFLICT (fingerprint) DO UPDATE SET count = client_errors.count + 1, last_seen = now(),
			page_url = EXCLUDED.page_url, user_agent = EXCLUDED.user_agent,
			status = CASE WHEN client_errors.status = 'resolved' THEN 'open' ELSE client_errors.status END`,
		e.Fingerprint, e.Message, e.Stack, e.PageURL, e.UserAgent, maxRows)
	return err
}

func (r *ClientErrorRepo) List(ctx context.Context, f clienterror.Filter) (clienterror.Page, error) {
	page := clienterror.Page{Items: []clienterror.ClientError{}, Page: f.Paging.Page, Limit: f.Paging.Limit}
	rows, err := r.db.Query(ctx, `SELECT `+clientErrorCols+`, count(*) OVER () FROM client_errors
		WHERE ($1 = '' OR status = $1) ORDER BY last_seen DESC, id DESC LIMIT $2 OFFSET $3`,
		f.Status, f.Paging.Limit, f.Paging.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanClientError(rows, &page.Total)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, e)
	}
	return page, rows.Err()
}

func (r *ClientErrorRepo) SetStatus(ctx context.Context, id int, s clienterror.Status) (clienterror.ClientError, error) {
	return scanClientError(r.db.QueryRow(ctx, `UPDATE client_errors SET status = $2 WHERE id = $1 RETURNING `+clientErrorCols, id, s))
}

func (r *ClientErrorRepo) PurgeBefore(ctx context.Context, t time.Time) (int64, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM client_errors WHERE last_seen < $1`, t)
	return tag.RowsAffected(), err
}
