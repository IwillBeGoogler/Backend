package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
)

// IdentityRepo stores social login links (provider + subject -> user).
type IdentityRepo struct{ db *pgxpool.Pool }

func NewIdentityRepo(db *pgxpool.Pool) *IdentityRepo { return &IdentityRepo{db: db} }

func (r *IdentityRepo) UserBySubject(ctx context.Context, provider, subject string) (int, error) {
	var id int
	err := r.db.QueryRow(ctx, `SELECT user_id FROM user_identities WHERE provider = $1 AND subject = $2`, provider, subject).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return id, err
}

// Link is idempotent; an existing link keeps its user.
func (r *IdentityRepo) Link(ctx context.Context, provider, subject string, userID int) error {
	_, err := r.db.Exec(ctx, `INSERT INTO user_identities (provider, subject, user_id) VALUES ($1, $2, $3)
		ON CONFLICT (provider, subject) DO NOTHING`, provider, subject, userID)
	return err
}
