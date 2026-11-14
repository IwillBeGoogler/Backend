package postgres

import (
	"context"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/glossary"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GlossaryRepo struct{ db *pgxpool.Pool }

func NewGlossaryRepo(db *pgxpool.Pool) *GlossaryRepo { return &GlossaryRepo{db: db} }

const glossaryCols = `id, term, aliases, definition, lang, created_at, updated_at`

func (r *GlossaryRepo) List(ctx context.Context, lang string) ([]glossary.Term, error) {
	rows, err := r.db.Query(ctx, `SELECT `+glossaryCols+` FROM glossary_terms
		WHERE $1 = '' OR lang = $1 OR lang = '' ORDER BY lower(term), id`, lang)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[glossary.Term])
}

func (r *GlossaryRepo) Create(ctx context.Context, in glossary.Input) (glossary.Term, error) {
	rows, err := r.db.Query(ctx, `INSERT INTO glossary_terms (term, aliases, definition, lang)
		VALUES ($1, $2, $3, $4) RETURNING `+glossaryCols,
		in.Term, in.Aliases, in.Definition, in.Lang)
	if err != nil {
		return glossary.Term{}, err
	}
	t, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[glossary.Term])
	if pgCode(err) == pgUniqueViolation {
		return t, domain.ErrConflict
	}
	return t, err
}

func (r *GlossaryRepo) Update(ctx context.Context, id int, in glossary.Input) (glossary.Term, error) {
	rows, err := r.db.Query(ctx, `UPDATE glossary_terms SET term = $2, aliases = $3, definition = $4, lang = $5,
		updated_at = now() WHERE id = $1 RETURNING `+glossaryCols,
		id, in.Term, in.Aliases, in.Definition, in.Lang)
	if err != nil {
		return glossary.Term{}, err
	}
	t, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[glossary.Term])
	if pgCode(err) == pgUniqueViolation {
		return t, domain.ErrConflict
	}
	return t, notFound(err)
}

func (r *GlossaryRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM glossary_terms WHERE id = $1`, id)
}
