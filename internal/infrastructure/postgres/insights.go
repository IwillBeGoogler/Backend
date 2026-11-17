package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

// InsightsRepo aggregates the admin insights report (application.InsightsRepo).
type InsightsRepo struct{ db *pgxpool.Pool }

func NewInsightsRepo(db *pgxpool.Pool) *InsightsRepo { return &InsightsRepo{db: db} }

func (r *InsightsRepo) Report(ctx context.Context, since time.Time, days int) (application.InsightsReport, error) {
	rep := application.InsightsReport{Days: days}
	steps := []func() error{
		func() error { return r.posts(ctx, since, &rep.Posts) },
		func() error { return r.users(ctx, since, &rep.Users) },
		func() error { return r.diagrams(ctx, since, &rep.Diagrams) },
		func() error { return r.moderation(ctx, since, &rep.Moderation) },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (r *InsightsRepo) posts(ctx context.Context, since time.Time, o *application.PostInsights) error {
	err := r.db.QueryRow(ctx, `SELECT
		count(*), count(*) FILTER (WHERE status = 'published'), count(*) FILTER (WHERE status = 'draft'),
		COALESCE(sum(likes), 0), (SELECT count(*) FROM comments)
		FROM posts`).Scan(&o.Total, &o.Published, &o.Drafts, &o.Likes, &o.Comments)
	if err != nil {
		return err
	}
	if o.Created, err = r.series(ctx, `SELECT (created_at AT TIME ZONE 'UTC')::date, count(*)
		FROM posts WHERE created_at >= $1 GROUP BY 1`, since); err != nil {
		return err
	}
	if o.PerDayPub, err = r.series(ctx, `SELECT (published_at AT TIME ZONE 'UTC')::date, count(*)
		FROM posts WHERE status = 'published' AND published_at >= $1 GROUP BY 1`, since); err != nil {
		return err
	}
	if o.NewComment, err = r.series(ctx, `SELECT (created_at AT TIME ZONE 'UTC')::date, count(*)
		FROM comments WHERE created_at >= $1 GROUP BY 1`, since); err != nil {
		return err
	}
	if o.ByCategory, err = r.named(ctx, `SELECT COALESCE(c.name, ''), count(*) FROM posts p
		LEFT JOIN categories c ON c.id = p.category_id WHERE p.status = 'published'
		GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 10`); err != nil {
		return err
	}
	const cols = `SELECT p.id, COALESCE(p.slug, ''), p.title, u.username, p.likes,
		(SELECT count(*) FROM comments c WHERE c.post_id = p.id)
		FROM posts p JOIN users u ON u.id = p.user_id WHERE p.status = 'published' `
	if o.TopByLikes, err = r.postRows(ctx, cols+`ORDER BY p.likes DESC, p.id DESC LIMIT 10`); err != nil {
		return err
	}
	o.TopByTalk, err = r.postRows(ctx, cols+`ORDER BY 6 DESC, p.id DESC LIMIT 10`)
	return err
}

func (r *InsightsRepo) users(ctx context.Context, since time.Time, o *application.UserInsights) error {
	err := r.db.QueryRow(ctx, `SELECT
		count(*), count(*) FILTER (WHERE created_at >= $1), count(*) FILTER (WHERE email_verified_at IS NOT NULL),
		(SELECT count(*) FROM user_sanctions)
		FROM users WHERE deleted_at IS NULL`, since).Scan(&o.Total, &o.New, &o.Verified, &o.Sanctioned)
	if err != nil {
		return err
	}
	if o.Signups, err = r.series(ctx, `SELECT (created_at AT TIME ZONE 'UTC')::date, count(*)
		FROM users WHERE created_at >= $1 GROUP BY 1`, since); err != nil {
		return err
	}
	o.TopAuthors, err = r.named(ctx, `SELECT u.username, count(*) FROM posts p JOIN users u ON u.id = p.user_id
		WHERE p.status = 'published' AND u.deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 10`)
	return err
}

func (r *InsightsRepo) diagrams(ctx context.Context, since time.Time, o *application.DiagramInsights) error {
	err := r.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM dg_projects), count(*), count(*) FILTER (WHERE visibility = 'public'),
		(SELECT count(*) FROM dg_templates) FROM dg_files`).Scan(&o.Projects, &o.Files, &o.Public, &o.Templates)
	if err != nil {
		return err
	}
	if o.Created, err = r.series(ctx, `SELECT (created_at AT TIME ZONE 'UTC')::date, count(*)
		FROM dg_files WHERE created_at >= $1 GROUP BY 1`, since); err != nil {
		return err
	}
	if o.ByKind, err = r.named(ctx, `SELECT file_kind, count(*) FROM dg_files GROUP BY 1 ORDER BY 2 DESC, 1`); err != nil {
		return err
	}
	if o.ByVisibility, err = r.named(ctx, `SELECT visibility, count(*) FROM dg_files GROUP BY 1 ORDER BY 2 DESC, 1`); err != nil {
		return err
	}
	o.TopOwners, err = r.named(ctx, `SELECT u.username, count(*) FROM dg_files f
		JOIN dg_projects p ON p.id = f.project_id JOIN users u ON u.id = p.owner_id
		GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 10`)
	return err
}

func (r *InsightsRepo) moderation(ctx context.Context, since time.Time, o *application.ModerationInsights) error {
	err := r.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM reports WHERE status = 'open'),
		(SELECT count(*) FROM feedback WHERE status = 'open'),
		(SELECT count(*) FROM client_errors WHERE status = 'open'),
		COALESCE((SELECT avg(extract(epoch FROM resolved_at - created_at)) / 3600.0 FROM reports
			WHERE resolved_at >= $1), 0)`, since).Scan(&o.OpenReports, &o.OpenFeedback, &o.OpenErrors, &o.AvgResolveHours)
	if err != nil {
		return err
	}
	if o.Opened, err = r.series(ctx, `SELECT (created_at AT TIME ZONE 'UTC')::date, count(*)
		FROM reports WHERE created_at >= $1 GROUP BY 1`, since); err != nil {
		return err
	}
	if o.Resolved, err = r.series(ctx, `SELECT (resolved_at AT TIME ZONE 'UTC')::date, count(*)
		FROM reports WHERE resolved_at >= $1 GROUP BY 1`, since); err != nil {
		return err
	}
	if o.ByReason, err = r.named(ctx, `SELECT reason, count(*) FROM reports WHERE created_at >= $1
		GROUP BY 1 ORDER BY 2 DESC, 1`, since); err != nil {
		return err
	}
	o.FeedbackByKind, err = r.named(ctx, `SELECT kind, count(*) FROM feedback WHERE created_at >= $1
		GROUP BY 1 ORDER BY 2 DESC, 1`, since)
	return err
}

// series fills every day from since to today, zero where q has no row.
func (r *InsightsRepo) series(ctx context.Context, q string, since time.Time) ([]application.DayCount, error) {
	rows, err := r.db.Query(ctx, `SELECT to_char(d, 'YYYY-MM-DD'), COALESCE(x.n, 0)
		FROM generate_series(($1::timestamptz AT TIME ZONE 'UTC')::date, (now() AT TIME ZONE 'UTC')::date, interval '1 day') d
		LEFT JOIN (`+q+`) AS x(day, n) ON x.day = d::date ORDER BY d`, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[application.DayCount])
}

func (r *InsightsRepo) named(ctx context.Context, q string, args ...any) ([]application.NamedCount, error) {
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[application.NamedCount])
}

func (r *InsightsRepo) postRows(ctx context.Context, q string) ([]application.InsightPost, error) {
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[application.InsightPost])
}
