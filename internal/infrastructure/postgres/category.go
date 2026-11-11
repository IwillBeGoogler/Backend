package postgres

import (
	"context"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/category"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CategoryRepo struct{ db *pgxpool.Pool }

func NewCategoryRepo(db *pgxpool.Pool) *CategoryRepo { return &CategoryRepo{db: db} }

func (r *CategoryRepo) List(ctx context.Context) ([]category.Category, error) {
	rows, err := r.db.Query(ctx, `SELECT `+categoryCols+` FROM categories c ORDER BY c.parent_id NULLS FIRST, c.position, c.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[category.Category])
}

const categoryCols = `c.id, COALESCE(c.parent_id, 0), c.slug, c.name, c.names, c.description, c.position,
	(SELECT count(*) FROM post_categories pc JOIN posts p ON p.id = pc.post_id
		WHERE pc.category_id = c.id AND p.status = 'published')`

func (r *CategoryRepo) BySlug(ctx context.Context, slug string) (category.Category, error) {
	rows, err := r.db.Query(ctx, `SELECT `+categoryCols+` FROM categories c WHERE c.slug = $1`, slug)
	if err != nil {
		return category.Category{}, err
	}
	c, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[category.Category])
	return c, notFound(err)
}

func (r *CategoryRepo) Exists(ctx context.Context, id int) (bool, error) {
	return exists(ctx, r.db, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = $1)`, id)
}

func (r *CategoryRepo) Create(ctx context.Context, in category.Input) (category.Category, error) {
	if in.Names == nil {
		in.Names = map[string]string{}
	}
	c := category.Category{ParentID: in.ParentID, Slug: in.Slug, Name: in.Name, Names: in.Names,
		Description: in.Description, Position: in.Position}
	err := r.db.QueryRow(ctx, `INSERT INTO categories (name, slug, parent_id, names, description, position)
		VALUES ($1, $2, NULLIF($3, 0), $4, $5, $6) RETURNING id`,
		in.Name, in.Slug, in.ParentID, in.Names, in.Description, in.Position).Scan(&c.ID)
	switch pgCode(err) {
	case pgUniqueViolation:
		return c, domain.ErrConflict
	case pgForeignKeyViolation:
		return c, domain.ErrNotFound
	}
	return c, err
}

// Delete removes the subtree (ON DELETE CASCADE); posts lose those categories.
func (r *CategoryRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM categories WHERE id = $1`, id)
}

type StatsRepo struct{ db *pgxpool.Pool }

func NewStatsRepo(db *pgxpool.Pool) *StatsRepo { return &StatsRepo{db: db} }

func (r *StatsRepo) Stats(ctx context.Context) (application.Stats, error) {
	var s application.Stats
	err := r.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM posts),
		(SELECT count(*) FROM comments),
		(SELECT COALESCE(sum(likes), 0) FROM posts),
		(SELECT count(*) FROM categories),
		(SELECT count(*) FROM users)`).Scan(&s.Posts, &s.Comments, &s.Likes, &s.Categories, &s.Users)
	return s, err
}
