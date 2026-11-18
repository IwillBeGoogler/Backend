package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

type AnalyticsRepo struct{ db *pgxpool.Pool }

func NewAnalyticsRepo(db *pgxpool.Pool) *AnalyticsRepo { return &AnalyticsRepo{db: db} }

// Insert drops ids that don't exist (deleted post, purged user) instead of
// failing on the foreign key.
func (r *AnalyticsRepo) Insert(ctx context.Context, e application.AnalyticsEvent) error {
	_, err := r.db.Exec(ctx, `INSERT INTO analytics_events (name, visitor, user_id, path, post_id, value, lang, referrer)
		VALUES ($1, $2, (SELECT id FROM users WHERE id = $3), $4, (SELECT id FROM posts WHERE id = $5), $6, $7, $8)`,
		e.Name, e.Visitor, e.UserID, e.Path, e.PostID, e.Value, e.Lang, e.Referrer)
	if err == nil && e.Name == application.EventReadTime && e.PostID > 0 {
		_, err = r.db.Exec(ctx, `UPDATE posts SET read_seconds = read_seconds + $2, read_sessions = read_sessions + 1
			WHERE id = $1`, e.PostID, int64(e.Value))
	}
	return err
}

func (r *AnalyticsRepo) PurgeBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM analytics_events WHERE at < $1`, before)
	return tag.RowsAffected(), err
}

// who is one person: the account when signed in, else the visitor id.
const who = `COALESCE('u' || user_id, visitor)`

func (r *AnalyticsRepo) Report(ctx context.Context, since time.Time, days int) (application.AnalyticsReport, error) {
	rep := application.AnalyticsReport{Days: days}
	var err error
	if rep.DAU, err = r.series(ctx, `SELECT (at AT TIME ZONE 'UTC')::date, count(DISTINCT `+who+`)
		FROM analytics_events WHERE at >= $1 GROUP BY 1`, since); err != nil {
		return rep, err
	}
	if rep.Signups, err = r.series(ctx, `SELECT (created_at AT TIME ZONE 'UTC')::date, count(*)
		FROM users WHERE created_at >= $1 GROUP BY 1`, since); err != nil {
		return rep, err
	}
	err = r.db.QueryRow(ctx, `SELECT
		(SELECT count(DISTINCT `+who+`) FROM analytics_events WHERE at >= now() - interval '7 days'),
		(SELECT count(DISTINCT `+who+`) FROM analytics_events WHERE at >= now() - interval '30 days'),
		COALESCE((SELECT avg(value) FROM analytics_events WHERE name = 'read_time' AND at >= $1), 0),
		COALESCE((SELECT sum(value) FROM analytics_events WHERE name = 'read_time' AND at >= $1), 0) / 3600.0`,
		since).Scan(&rep.WAU, &rep.MAU, &rep.AvgReadSeconds, &rep.TotalReadHours)
	if err != nil {
		return rep, err
	}

	// Funnel: visitors, saw the signup form, signed up, published a story.
	var visitors, opened, signed, published int
	err = r.db.QueryRow(ctx, `WITH s AS (SELECT DISTINCT user_id FROM analytics_events
			WHERE name = 'signup' AND at >= $1 AND user_id IS NOT NULL)
		SELECT
		(SELECT count(DISTINCT visitor) FROM analytics_events WHERE name = 'page_view' AND at >= $1),
		(SELECT count(DISTINCT visitor) FROM analytics_events WHERE name = 'signup_open' AND at >= $1),
		(SELECT count(*) FROM s),
		(SELECT count(*) FROM s WHERE EXISTS (SELECT 1 FROM analytics_events p
			WHERE p.name = 'publish' AND p.user_id = s.user_id))`,
		since).Scan(&visitors, &opened, &signed, &published)
	if err != nil {
		return rep, err
	}
	rep.Funnel = []application.FunnelStep{
		{Step: "visit", Count: visitors}, {Step: "signup_open", Count: opened},
		{Step: "signup", Count: signed}, {Step: "first_publish", Count: published},
	}

	// Retention: of users who signed up in the window and are old enough to
	// measure, the share active exactly N days after signup.
	var cohort, n1, k1, n7, k7, n30, k30 int
	err = r.db.QueryRow(ctx, `WITH c AS (
			SELECT id, (created_at AT TIME ZONE 'UTC')::date AS d FROM users WHERE created_at >= $1),
		a AS (SELECT DISTINCT user_id, (at AT TIME ZONE 'UTC')::date AS d FROM analytics_events
			WHERE user_id IS NOT NULL AND at >= $1),
		t AS (SELECT (now() AT TIME ZONE 'UTC')::date AS d)
		SELECT count(*),
		count(*) FILTER (WHERE c.d + 1 <= t.d),
		count(*) FILTER (WHERE c.d + 1 <= t.d AND EXISTS (SELECT 1 FROM a WHERE a.user_id = c.id AND a.d = c.d + 1)),
		count(*) FILTER (WHERE c.d + 7 <= t.d),
		count(*) FILTER (WHERE c.d + 7 <= t.d AND EXISTS (SELECT 1 FROM a WHERE a.user_id = c.id AND a.d = c.d + 7)),
		count(*) FILTER (WHERE c.d + 30 <= t.d),
		count(*) FILTER (WHERE c.d + 30 <= t.d AND EXISTS (SELECT 1 FROM a WHERE a.user_id = c.id AND a.d = c.d + 30))
		FROM c, t`, since).Scan(&cohort, &n1, &k1, &n7, &k7, &n30, &k30)
	if err != nil {
		return rep, err
	}
	rep.Retention = application.Retention{Cohort: cohort, D1: share(k1, n1), D7: share(k7, n7), D30: share(k30, n30)}

	if rep.TopPaths, err = r.counts(ctx, `SELECT path, count(DISTINCT `+who+`) FROM analytics_events
		WHERE name = 'page_view' AND at >= $1 GROUP BY path ORDER BY 2 DESC, 1 LIMIT 20`, since); err != nil {
		return rep, err
	}
	rep.Languages, err = r.counts(ctx, `SELECT COALESCE(NULLIF(lang, ''), '?'), count(DISTINCT `+who+`) FROM analytics_events
		WHERE name = 'page_view' AND at >= $1 GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 10`, since)
	if err != nil {
		return rep, err
	}
	if rep.Referrers, err = r.counts(ctx, `SELECT referrer, count(DISTINCT `+who+`) FROM analytics_events
		WHERE name = 'page_view' AND referrer <> '' AND at >= $1 GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 10`, since); err != nil {
		return rep, err
	}
	if rep.Heatmap, err = r.heatmap(ctx, since); err != nil {
		return rep, err
	}
	rep.Cohorts, err = r.cohorts(ctx, since)
	return rep, err
}

func (r *AnalyticsRepo) heatmap(ctx context.Context, since time.Time) ([]application.HeatCell, error) {
	rows, err := r.db.Query(ctx, `SELECT extract(dow FROM at AT TIME ZONE 'UTC')::int, extract(hour FROM at AT TIME ZONE 'UTC')::int, count(*)
		FROM analytics_events WHERE name = 'page_view' AND at >= $1 GROUP BY 1, 2 ORDER BY 1, 2`, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[application.HeatCell])
}

// maxCohortWeeks bounds the retention table width.
const maxCohortWeeks = 12

// cohorts groups signups by week and, per cohort, the share of users seen in
// each following week. Offsets that lie in the future are dropped.
func (r *AnalyticsRepo) cohorts(ctx context.Context, since time.Time) ([]application.CohortRow, error) {
	rows, err := r.db.Query(ctx, `WITH c AS (
			SELECT id, date_trunc('week', created_at AT TIME ZONE 'UTC')::date AS w FROM users WHERE created_at >= $1),
		a AS (SELECT DISTINCT user_id, date_trunc('week', at AT TIME ZONE 'UTC')::date AS w FROM analytics_events
			WHERE user_id IS NOT NULL AND at >= $1),
		sz AS (SELECT w, count(*) AS n FROM c GROUP BY w)
		SELECT to_char(sz.w, 'YYYY-MM-DD'), sz.n::int, COALESCE((a.w - sz.w) / 7, -1), count(DISTINCT c.id)::int
		FROM sz
		LEFT JOIN c ON c.w = sz.w
		LEFT JOIN a ON a.user_id = c.id AND a.w >= c.w AND (a.w - c.w) / 7 < $2
		GROUP BY sz.w, sz.n, 3 ORDER BY sz.w, 3`, since, maxCohortWeeks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	thisWeek := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	thisWeek = thisWeek.AddDate(0, 0, -((int(thisWeek.Weekday()) + 6) % 7))
	var out []application.CohortRow
	for rows.Next() {
		var week string
		var size, k, active int
		if err := rows.Scan(&week, &size, &k, &active); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Week != week {
			start, _ := time.Parse("2006-01-02", week)
			span := min(int(thisWeek.Sub(start).Hours()/24/7)+1, maxCohortWeeks)
			out = append(out, application.CohortRow{Week: week, Size: size, Retained: make([]float64, max(span, 1))})
		}
		row := &out[len(out)-1]
		if k >= 0 && k < len(row.Retained) {
			row.Retained[k] = share(active, size)
		}
	}
	return out, rows.Err()
}

// series fills every day from since to today, zero where q has no row.
func (r *AnalyticsRepo) series(ctx context.Context, q string, since time.Time) ([]application.DayCount, error) {
	rows, err := r.db.Query(ctx, `SELECT to_char(d, 'YYYY-MM-DD'), COALESCE(x.n, 0)
		FROM generate_series(($1::timestamptz AT TIME ZONE 'UTC')::date, (now() AT TIME ZONE 'UTC')::date, interval '1 day') d
		LEFT JOIN (`+q+`) AS x(day, n) ON x.day = d::date ORDER BY d`, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[application.DayCount])
}

func (r *AnalyticsRepo) counts(ctx context.Context, q string, since time.Time) ([]application.PathCount, error) {
	rows, err := r.db.Query(ctx, q, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[application.PathCount])
}

func share(k, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(k) / float64(n)
}
