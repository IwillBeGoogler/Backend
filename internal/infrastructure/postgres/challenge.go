package postgres

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/challenge"
)

// ChallengeRepo implements challenge.Repository.
type ChallengeRepo struct{ db *pgxpool.Pool }

func NewChallengeRepo(db *pgxpool.Pool) *ChallengeRepo { return &ChallengeRepo{db: db} }

// challengeCounted holds for a post p that counts towards challenge c.
// 300 is challenge.MinChars.
const challengeCounted = `p.user_id = c.user_id AND p.status = 'published' AND char_length(p.body) >= 300
	AND (p.published_at AT TIME ZONE c.timezone)::date BETWEEN c.start_date AND c.start_date + c.days - 1`

const challengeCols = `c.id, c.user_id, u.username, c.title, c.description, c.days, c.visibility, c.timezone, c.start_date::text,
	(SELECT count(DISTINCT (p.published_at AT TIME ZONE c.timezone)::date) FROM challenge_posts cp
		JOIN posts p ON p.id = cp.post_id WHERE cp.challenge_id = c.id AND ` + challengeCounted + `),
	c.created_at, c.updated_at, c.reminders, to_char(c.remind_at, 'HH24:MI')`

const challengeFrom = ` FROM challenges c JOIN users u ON u.id = c.user_id AND u.deleted_at IS NULL `

// challengeRunning holds while the last day is not over at the time in param.
func challengeRunning(param string) string {
	return `(` + param + `::timestamptz AT TIME ZONE c.timezone)::date < c.start_date + c.days`
}

func scanChallenge(row pgx.Row, extra ...any) (challenge.Challenge, error) {
	var c challenge.Challenge
	dest := append([]any{&c.ID, &c.UserID, &c.Author, &c.Title, &c.Description, &c.Days, &c.Visibility, &c.Timezone,
		&c.StartDate, &c.DaysDone, &c.CreatedAt, &c.UpdatedAt,
		&c.Reminders, &c.RemindAt}, extra...)
	return c, notFound(row.Scan(dest...))
}

// Create locks per user, so concurrent creates cannot pass the limit. The
// start date is computed by Postgres, which also rejects zones it lacks (one
// stored unknown zone would break every list query).
func (r *ChallengeRepo) Create(ctx context.Context, userID int, in challenge.Input, now time.Time) (challenge.Challenge, error) {
	var id int
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('challenges'), $1)`, userID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM challenges c WHERE c.user_id = $1 AND `+challengeRunning("$2"),
			userID, now).Scan(&n); err != nil {
			return err
		}
		if n >= challenge.MaxActive {
			return challenge.ErrTooManyActive
		}
		err := tx.QueryRow(ctx, `INSERT INTO challenges (user_id, title, description, days, visibility, timezone, start_date)
			VALUES ($1, $2, $3, $4, $5, $6, ($7::timestamptz AT TIME ZONE $6)::date) RETURNING id`,
			userID, in.Title, in.Description, in.Days, in.Visibility, in.Timezone, now).Scan(&id)
		if pgCode(err) == "22023" { // invalid_parameter_value: unknown time zone
			return challenge.ErrBadTimezone
		}
		return err
	})
	if err != nil {
		return challenge.Challenge{}, err
	}
	return r.Get(ctx, id)
}

func (r *ChallengeRepo) Get(ctx context.Context, id int) (challenge.Challenge, error) {
	return scanChallenge(r.db.QueryRow(ctx, `SELECT `+challengeCols+challengeFrom+`WHERE c.id = $1`, id))
}

func (r *ChallengeRepo) OfPost(ctx context.Context, postID int) (challenge.Challenge, error) {
	return scanChallenge(r.db.QueryRow(ctx, `SELECT `+challengeCols+challengeFrom+
		`WHERE c.id = (SELECT challenge_id FROM challenge_posts WHERE post_id = $1)`, postID))
}

func (r *ChallengeRepo) Update(ctx context.Context, id int, in challenge.Input) (challenge.Challenge, error) {
	if err := execOne(ctx, r.db, `UPDATE challenges SET title = $2, description = $3, visibility = $4,
			reminders = COALESCE($5, reminders), remind_at = COALESCE(NULLIF($6, '')::time, remind_at), updated_at = now()
		WHERE id = $1`, id, in.Title, in.Description, in.Visibility, in.Reminders, in.RemindAt); err != nil {
		return challenge.Challenge{}, err
	}
	return r.Get(ctx, id)
}

func (r *ChallengeRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM challenges WHERE id = $1`, id)
}

func (r *ChallengeRepo) ListPublicActive(ctx context.Context, now time.Time, p domain.Paging) (challenge.Page, error) {
	return r.list(ctx, `c.visibility = 'public' AND `+challengeRunning("$1"), now, p)
}

func (r *ChallengeRepo) ListByUser(ctx context.Context, userID int, p domain.Paging) (challenge.Page, error) {
	return r.list(ctx, `c.user_id = $1`, userID, p)
}

// list pages challenges matching where (which uses $1), newest start first;
// cursors are (start date as unix seconds, id).
func (r *ChallengeRepo) list(ctx context.Context, where string, key any, p domain.Paging) (challenge.Page, error) {
	page := challenge.Page{Items: []challenge.Challenge{}, Page: p.Page, Limit: p.Limit}
	args := []any{key}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	cursor, offset := "", p.Offset()
	if p.Cursor != "" {
		c, err := domain.DecodeCursor(p.Cursor, 2)
		if err != nil {
			return page, err
		}
		offset, page.Page = 0, 0
		cursor = "WHERE (c.start_date, c.id) < (" + arg(time.Unix(c[0], 0).UTC().Format(time.DateOnly)) + "::date, " + arg(c[1]) + ")"
	}
	// The window counts the whole list; the cursor filters afterwards. One extra row tells whether a next page exists.
	sql := `SELECT ` + challengeCols + `, c.total FROM (SELECT c.*, count(*) OVER () AS total` + challengeFrom + `WHERE ` + where + `) c
		JOIN users u ON u.id = c.user_id ` + cursor + `
		ORDER BY c.start_date DESC, c.id DESC LIMIT ` + arg(p.Limit+1) + ` OFFSET ` + arg(offset)
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanChallenge(rows, &page.Total)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, c)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > p.Limit {
		page.Items = page.Items[:p.Limit]
		last := page.Items[p.Limit-1]
		start, err := time.Parse(time.DateOnly, last.StartDate)
		if err != nil {
			return page, err
		}
		page.NextCursor = domain.EncodeCursor(start.Unix(), int64(last.ID))
	}
	return page, nil
}

func (r *ChallengeRepo) Running(ctx context.Context, userID int, now time.Time) ([]challenge.Challenge, error) {
	rows, err := r.db.Query(ctx, `SELECT `+challengeCols+`,
			ARRAY(SELECT post_id FROM challenge_posts WHERE challenge_id = c.id ORDER BY post_id)`+challengeFrom+`
		WHERE c.user_id = $1 AND `+challengeRunning("$2")+` ORDER BY c.start_date DESC, c.id DESC`, userID, now)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (challenge.Challenge, error) {
		var ids []int
		c, err := scanChallenge(row, &ids)
		c.PostIDs = ids
		return c, err
	})
}

func (r *ChallengeRepo) CountActive(ctx context.Context, userID int, now time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM challenges c WHERE c.user_id = $1 AND `+challengeRunning("$2"), userID, now).Scan(&n)
	return n, err
}

func (r *ChallengeRepo) Link(ctx context.Context, id, postID int) error {
	// The no-op update returns the existing row's challenge on a repeat.
	var linked int
	err := r.db.QueryRow(ctx, `INSERT INTO challenge_posts (challenge_id, post_id) VALUES ($1, $2)
		ON CONFLICT (post_id) DO UPDATE SET post_id = EXCLUDED.post_id RETURNING challenge_id`, id, postID).Scan(&linked)
	switch {
	case pgCode(err) == pgForeignKeyViolation:
		return domain.ErrNotFound
	case err != nil:
		return err
	case linked != id:
		return domain.ErrConflict
	}
	return nil
}

func (r *ChallengeRepo) Unlink(ctx context.Context, id, postID int) error {
	return execOne(ctx, r.db, `DELETE FROM challenge_posts WHERE challenge_id = $1 AND post_id = $2`, id, postID)
}

func (r *ChallengeRepo) Days(ctx context.Context, id int) ([]challenge.Day, error) {
	rows, err := r.db.Query(ctx, `SELECT DISTINCT ON (d) d::text, d - c.start_date + 1, p.id, p.title, p.slug, u.username
		FROM challenges c
		JOIN users u ON u.id = c.user_id
		JOIN challenge_posts cp ON cp.challenge_id = c.id
		JOIN posts p ON p.id = cp.post_id
		CROSS JOIN LATERAL (SELECT (p.published_at AT TIME ZONE c.timezone)::date AS d) x
		WHERE c.id = $1 AND `+challengeCounted+`
		ORDER BY d, p.published_at, p.id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (challenge.Day, error) {
		var d challenge.Day
		err := row.Scan(&d.Date, &d.N, &d.PostID, &d.Title, &d.Slug, &d.Author)
		return d, err
	})
}

func (r *ChallengeRepo) Follow(ctx context.Context, id, userID int) error {
	_, err := r.db.Exec(ctx, `INSERT INTO challenge_follows (challenge_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, userID)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *ChallengeRepo) Unfollow(ctx context.Context, id, userID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM challenge_follows WHERE challenge_id = $1 AND user_id = $2`, id, userID)
	return err
}

const challengeFollowers = ` FROM challenge_follows f JOIN users u ON u.id = f.user_id AND u.deleted_at IS NULL
	WHERE f.challenge_id = $1`

func (r *ChallengeRepo) FollowerIDs(ctx context.Context, id int) ([]int, error) {
	rows, err := r.db.Query(ctx, `SELECT f.user_id`+challengeFollowers+` ORDER BY f.user_id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

func (r *ChallengeRepo) CountFollowers(ctx context.Context, id int) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*)`+challengeFollowers, id).Scan(&n)
	return n, err
}

func (r *ChallengeRepo) IsFollowing(ctx context.Context, id, userID int) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM challenge_follows WHERE challenge_id = $1 AND user_id = $2)`,
		id, userID).Scan(&ok)
	return ok, err
}

func (r *ChallengeRepo) ListFollowed(ctx context.Context, userID int, p domain.Paging) (challenge.Page, error) {
	return r.list(ctx, `c.visibility = 'public' AND EXISTS (SELECT 1 FROM challenge_follows f
		WHERE f.challenge_id = c.id AND f.user_id = $1)`, userID, p)
}

func (r *ChallengeRepo) Cheer(ctx context.Context, id, userID int, day string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO challenge_cheers (challenge_id, user_id, day) VALUES ($1, $2, $3::date)
		ON CONFLICT DO NOTHING`, id, userID, day)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *ChallengeRepo) CheersOn(ctx context.Context, id int, day string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM challenge_cheers WHERE challenge_id = $1 AND day = $2::date`, id, day).Scan(&n)
	return n, err
}

func (r *ChallengeRepo) Cheered(ctx context.Context, id, userID int, day string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM challenge_cheers
		WHERE challenge_id = $1 AND day = $2::date AND user_id = $3)`, id, day, userID).Scan(&ok)
	return ok, err
}

func (r *ChallengeRepo) Claim(ctx context.Context, key string) (bool, error) {
	tag, err := r.db.Exec(ctx, `INSERT INTO challenge_sent (key) VALUES ($1) ON CONFLICT DO NOTHING`, key)
	return tag.RowsAffected() == 1, err
}

func (r *ChallengeRepo) PurgeSent(ctx context.Context, before time.Time) error {
	_, err := r.db.Exec(ctx, `DELETE FROM challenge_sent WHERE created_at < $1 AND key NOT LIKE 'ms:%'`, before)
	return err
}

func (r *ChallengeRepo) ReminderCandidates(ctx context.Context, now time.Time) ([]challenge.Candidate, error) {
	rows, err := r.db.Query(ctx, `SELECT c.id, c.user_id, c.timezone, to_char(c.remind_at, 'HH24:MI'), c.start_date::text,
			ARRAY(SELECT DISTINCT (p.published_at AT TIME ZONE c.timezone)::date::text FROM challenge_posts cp
				JOIN posts p ON p.id = cp.post_id WHERE cp.challenge_id = c.id AND `+challengeCounted+`
				AND (p.published_at AT TIME ZONE c.timezone)::date >= ($1::timestamptz AT TIME ZONE c.timezone)::date - $2::int)`+
		challengeFrom+`WHERE c.reminders AND `+challengeRunning("$1")+` ORDER BY c.user_id, c.remind_at, c.id`,
		now, challenge.NudgeWindow)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (challenge.Candidate, error) {
		var c challenge.Candidate
		err := row.Scan(&c.ChallengeID, &c.UserID, &c.Timezone, &c.RemindAt, &c.StartDate, &c.Done)
		return c, err
	})
}
