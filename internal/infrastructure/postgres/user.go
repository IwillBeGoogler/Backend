package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

type UserRepo struct{ db *pgxpool.Pool }

func NewUserRepo(db *pgxpool.Pool) *UserRepo { return &UserRepo{db: db} }

const userCols = `id, username, COALESCE(email, ''), email_verified_at IS NOT NULL, display_name, bio, avatar_url,
	deleted_at, created_at`

func scanUser(row pgx.Row, extra ...any) (user.User, error) {
	u := user.User{Roles: []string{}}
	err := row.Scan(append([]any{&u.ID, &u.Username, &u.Email, &u.EmailVerified, &u.DisplayName, &u.Bio, &u.AvatarURL, &u.DeletedAt, &u.CreatedAt}, extra...)...)
	if pgCode(err) == pgUniqueViolation {
		return u, domain.ErrConflict
	}
	return u, notFound(err)
}

func (r *UserRepo) Create(ctx context.Context, username, email string) (user.User, error) {
	return scanUser(r.db.QueryRow(ctx,
		`INSERT INTO users (username, email) VALUES ($1, $2) RETURNING `+userCols, username, email))
}

func (r *UserRepo) Ensure(ctx context.Context, username, email string) (user.User, error) {
	return scanUser(r.db.QueryRow(ctx, `INSERT INTO users (username, email) VALUES ($1, $2)
		ON CONFLICT ((lower(username))) DO UPDATE SET email = COALESCE(users.email, EXCLUDED.email)
		RETURNING `+userCols, username, email))
}

func (r *UserRepo) MarkEmailVerified(ctx context.Context, id int) error {
	return execOne(ctx, r.db, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1`, id)
}

func (r *UserRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM users WHERE id = $1`, id)
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (user.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(username) = lower($1)`, username))
}

func (r *UserRepo) GetByID(ctx context.Context, id int) (user.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (r *UserRepo) GetByLogin(ctx context.Context, login string) (user.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users
		WHERE lower(username) = lower($1) OR lower(email) = lower($1)`, login))
}

func (r *UserRepo) List(ctx context.Context, query string, p domain.Paging) (user.Page, error) {
	page := user.Page{Items: []user.User{}, Page: p.Page, Limit: p.Limit}
	rows, err := r.db.Query(ctx, `SELECT `+userCols+`, count(*) OVER () FROM users
		WHERE $1 = '' OR username ILIKE '%' || $1 || '%' OR email ILIKE '%' || $1 || '%'
		ORDER BY id LIMIT $2 OFFSET $3`, query, p.Limit, p.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows, &page.Total)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, u)
	}
	return page, rows.Err()
}

func (r *UserRepo) Profile(ctx context.Context, username string, viewerID int) (user.Profile, error) {
	var p user.Profile
	err := r.db.QueryRow(ctx, `SELECT u.id, u.username, u.display_name, u.bio, u.avatar_url, u.created_at,
			(SELECT count(*) FROM posts WHERE user_id = u.id AND status = 'published'),
			(SELECT COALESCE(sum(likes), 0) FROM posts WHERE user_id = u.id),
			(SELECT count(*) FROM follows f JOIN users x ON x.id = f.follower_id WHERE f.followee_id = u.id AND x.deleted_at IS NULL),
			(SELECT count(*) FROM follows f JOIN users x ON x.id = f.followee_id WHERE f.follower_id = u.id AND x.deleted_at IS NULL),
			EXISTS (SELECT 1 FROM follows WHERE follower_id = $2 AND followee_id = u.id),
			EXISTS (SELECT 1 FROM email_subscriptions WHERE subscriber_id = $2 AND author_id = u.id),
			COALESCE((SELECT id FROM posts WHERE id = u.pinned_post_id AND status = 'published'), 0)
		FROM users u WHERE lower(u.username) = lower($1) AND u.deleted_at IS NULL`, username, viewerID).
		Scan(&p.ID, &p.Username, &p.DisplayName, &p.Bio, &p.AvatarURL, &p.CreatedAt,
			&p.Posts, &p.Likes, &p.Followers, &p.Following, &p.IsFollowing, &p.IsSubscribed, &p.PinnedPostID)
	return p, notFound(err)
}

func (r *UserRepo) UpdateProfile(ctx context.Context, id int, u user.ProfileUpdate) (user.User, error) {
	return scanUser(r.db.QueryRow(ctx, `UPDATE users SET display_name = $2, bio = $3, avatar_url = $4
		WHERE id = $1 RETURNING `+userCols, id, u.DisplayName, u.Bio, u.AvatarURL))
}

func (r *UserRepo) SetUsername(ctx context.Context, id int, username string) (user.User, error) {
	return scanUser(r.db.QueryRow(ctx, `UPDATE users SET username = $2 WHERE id = $1 RETURNING `+userCols, id, username))
}

func (r *UserRepo) Follow(ctx context.Context, followerID, followeeID int) (bool, error) {
	tag, err := r.db.Exec(ctx, `INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, followerID, followeeID)
	if pgCode(err) == pgForeignKeyViolation {
		return false, domain.ErrNotFound
	}
	return tag.RowsAffected() > 0, err
}

func (r *UserRepo) Unfollow(ctx context.Context, followerID, followeeID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, followerID, followeeID)
	return err
}

func (r *UserRepo) Followers(ctx context.Context, id int, p domain.Paging) (user.SummaryPage, error) {
	return r.summaries(ctx, `f.followee_id = $1`, `f.follower_id`, id, p)
}

func (r *UserRepo) Following(ctx context.Context, id int, p domain.Paging) (user.SummaryPage, error) {
	return r.summaries(ctx, `f.follower_id = $1`, `f.followee_id`, id, p)
}

// summaries lists users joined on col from follows rows matching where,
// newest follow first; cursors are (follow time, user id).
func (r *UserRepo) summaries(ctx context.Context, where, col string, id int, p domain.Paging) (user.SummaryPage, error) {
	page := user.SummaryPage{Items: []user.Summary{}, Page: p.Page, Limit: p.Limit}
	var after *time.Time
	afterID, offset := int64(0), p.Offset()
	if p.Cursor != "" {
		c, err := domain.DecodeCursor(p.Cursor, 2)
		if err != nil {
			return page, err
		}
		at := time.UnixMicro(c[0]).UTC()
		after, afterID, offset, page.Page = &at, c[1], 0, 0
	}
	// total counts the whole list; the cursor filters afterwards (and one extra row says whether a next page exists)
	rows, err := r.db.Query(ctx, `SELECT id, username, display_name, avatar_url, at, total FROM (
			SELECT u.id, u.username, u.display_name, u.avatar_url, f.created_at AS at, count(*) OVER () AS total
			FROM follows f JOIN users u ON u.id = `+col+` WHERE `+where+` AND u.deleted_at IS NULL) s
		WHERE ($4::timestamptz IS NULL OR (at, id) < ($4, $5))
		ORDER BY at DESC, id DESC LIMIT $2 OFFSET $3`, id, p.Limit+1, offset, after, afterID)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	var lastAt, keptAt time.Time
	for rows.Next() {
		var s user.Summary
		if err := rows.Scan(&s.ID, &s.Username, &s.DisplayName, &s.AvatarURL, &lastAt, &page.Total); err != nil {
			return page, err
		}
		page.Items = append(page.Items, s)
		if len(page.Items) <= p.Limit {
			keptAt = lastAt
		}
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > p.Limit {
		page.Items = page.Items[:p.Limit]
		page.NextCursor = domain.EncodeCursor(keptAt.UnixMicro(), int64(page.Items[p.Limit-1].ID))
	}
	return page, nil
}

func (r *UserRepo) FollowerIDs(ctx context.Context, id int) ([]int, error) {
	rows, err := r.db.Query(ctx, `SELECT f.follower_id FROM follows f JOIN users u ON u.id = f.follower_id
		WHERE f.followee_id = $1 AND u.deleted_at IS NULL`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

func (r *UserRepo) MarkDeleted(ctx context.Context, id int, at time.Time) error {
	return execOne(ctx, r.db, `UPDATE users SET deleted_at = $2 WHERE id = $1`, id, at)
}

func (r *UserRepo) Restore(ctx context.Context, id int) error {
	return execOne(ctx, r.db, `UPDATE users SET deleted_at = NULL WHERE id = $1`, id)
}

func (r *UserRepo) DuePurges(ctx context.Context, cutoff time.Time) ([]int, error) {
	rows, err := r.db.Query(ctx, `SELECT id FROM users WHERE deleted_at <= $1`, cutoff)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

// Purge deletes the user's comments and posts, then the user; follows,
// likes, bookmarks, notifications and Guard rows cascade.
func (r *UserRepo) Purge(ctx context.Context, id int, cutoff time.Time) (bool, error) {
	var purged bool
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var due bool
		err := tx.QueryRow(ctx, `SELECT COALESCE(deleted_at <= $2, false) FROM users WHERE id = $1 FOR UPDATE`, id, cutoff).Scan(&due)
		if err != nil || !due {
			return ignoreNoRows(err)
		}
		for _, q := range []string{
			// a publication this user alone owned must not be left without an owner: the longest-standing
			// other member takes over, and one nobody else belongs to goes with its owner
			`UPDATE publication_members pm SET role = 'owner'
			   WHERE (pm.publication_id, pm.user_id) IN (
			     SELECT DISTINCT ON (m.publication_id) m.publication_id, m.user_id
			       FROM publication_members m
			      WHERE m.user_id <> $1 AND m.publication_id IN (
			            SELECT publication_id FROM publication_members WHERE user_id = $1 AND role = 'owner')
			        AND NOT EXISTS (SELECT 1 FROM publication_members o
			                         WHERE o.publication_id = m.publication_id AND o.role = 'owner' AND o.user_id <> $1)
			      ORDER BY m.publication_id, (m.role = 'editor') DESC, m.created_at, m.user_id)`,
			`DELETE FROM publications p WHERE EXISTS (SELECT 1 FROM publication_members WHERE publication_id = p.id AND user_id = $1)
			   AND NOT EXISTS (SELECT 1 FROM publication_members WHERE publication_id = p.id AND user_id <> $1)`,
			`DELETE FROM comments WHERE user_id = $1`,
			`DELETE FROM posts WHERE user_id = $1`,
			`DELETE FROM users WHERE id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return err
			}
		}
		purged = true
		return nil
	})
	return purged, err
}

func (r *UserRepo) SetPinned(ctx context.Context, id, postID int) error {
	return execOne(ctx, r.db, `UPDATE users SET pinned_post_id = NULLIF($2, 0) WHERE id = $1`, id, postID)
}

func (r *UserRepo) Subscribe(ctx context.Context, subscriberID, authorID int) error {
	_, err := r.db.Exec(ctx, `INSERT INTO email_subscriptions (subscriber_id, author_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, subscriberID, authorID)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *UserRepo) Unsubscribe(ctx context.Context, subscriberID, authorID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM email_subscriptions WHERE subscriber_id = $1 AND author_id = $2`, subscriberID, authorID)
	return err
}

func (r *UserRepo) Subscribers(ctx context.Context, authorID int) ([]user.Subscriber, error) {
	rows, err := r.db.Query(ctx, `SELECT u.id, u.email FROM email_subscriptions s JOIN users u ON u.id = s.subscriber_id
		WHERE s.author_id = $1 AND u.deleted_at IS NULL AND u.email IS NOT NULL AND u.email_verified_at IS NOT NULL
		ORDER BY u.id`, authorID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[user.Subscriber])
}
