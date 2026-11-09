package postgres

import (
	"context"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SiteStatsRepo reads the counts and public content lists behind the site
// statistics (application.SiteStatsRepo).
type SiteStatsRepo struct{ db *pgxpool.Pool }

func NewSiteStatsRepo(db *pgxpool.Pool) *SiteStatsRepo { return &SiteStatsRepo{db: db} }

func (r *SiteStatsRepo) Counts(ctx context.Context) (application.SiteCounts, error) {
	var c application.SiteCounts
	err := r.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM posts p JOIN users u ON u.id = p.user_id
			WHERE p.status = 'published' AND u.deleted_at IS NULL),
		(SELECT count(*) FROM dg_files f JOIN dg_projects p ON p.id = f.project_id
			JOIN users u ON u.id = p.owner_id WHERE f.visibility = 'public' AND u.deleted_at IS NULL),
		(SELECT count(*) FROM users WHERE deleted_at IS NULL),
		(SELECT count(DISTINCT `+who+`) FROM analytics_events WHERE name = 'page_view')`).
		Scan(&c.Posts, &c.Diagrams, &c.Users, &c.Visitors)
	return c, err
}

func (r *SiteStatsRepo) PublicPosts(ctx context.Context) ([]application.PostRef, error) {
	rows, err := r.db.Query(ctx, `SELECT p.id, p.slug, p.title, u.username
		FROM posts p JOIN users u ON u.id = p.user_id
		WHERE p.status = 'published' AND u.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (application.PostRef, error) {
		var p application.PostRef
		err := row.Scan(&p.ID, &p.Slug, &p.Title, &p.Author)
		return p, err
	})
}

func (r *SiteStatsRepo) PublicDiagrams(ctx context.Context) ([]application.DiagramRef, error) {
	rows, err := r.db.Query(ctx, `SELECT f.id, COALESCE(f.slug, ''), f.title, u.username
		FROM dg_files f JOIN dg_projects p ON p.id = f.project_id JOIN users u ON u.id = p.owner_id
		WHERE f.visibility = 'public' AND u.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (application.DiagramRef, error) {
		var d application.DiagramRef
		err := row.Scan(&d.FileID, &d.Slug, &d.Title, &d.Author)
		return d, err
	})
}

func (r *SiteStatsRepo) PageViews(ctx context.Context, since time.Time) ([]application.DayCount, error) {
	rows, err := r.db.Query(ctx, `SELECT to_char((at AT TIME ZONE 'UTC')::date, 'YYYY-MM-DD'), count(*)
		FROM analytics_events WHERE name = 'page_view' AND at >= $1 GROUP BY 1 ORDER BY 1`, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[application.DayCount])
}
