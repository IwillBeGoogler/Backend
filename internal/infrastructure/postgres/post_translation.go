package postgres

import (
	"context"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
)

var _ post.TranslationRepository = (*PostRepo)(nil)

func (r *PostRepo) GetTranslation(ctx context.Context, postID int, lang string) (post.Translation, error) {
	t := post.Translation{PostID: postID, Lang: lang}
	err := r.db.QueryRow(ctx, `SELECT title, subtitle, body, updated_at FROM post_translations
		WHERE post_id = $1 AND lang = $2`, postID, lang).Scan(&t.Title, &t.Subtitle, &t.Body, &t.UpdatedAt)
	if err != nil {
		return post.Translation{}, notFound(err)
	}
	t.ReadingTime = post.ReadingTime(t.Body)
	return t, nil
}

func (r *PostRepo) SaveTranslation(ctx context.Context, postID int, lang string, in post.TranslationInput) (post.Translation, error) {
	t := post.Translation{PostID: postID, Lang: lang}
	err := r.db.QueryRow(ctx, `INSERT INTO post_translations (post_id, lang, title, subtitle, body)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (post_id, lang) DO UPDATE
		SET title = EXCLUDED.title, subtitle = EXCLUDED.subtitle, body = EXCLUDED.body, updated_at = now()
		RETURNING title, subtitle, body, updated_at`, postID, lang, in.Title, in.Subtitle, in.Body).
		Scan(&t.Title, &t.Subtitle, &t.Body, &t.UpdatedAt)
	if err != nil {
		if pgCode(err) == pgForeignKeyViolation {
			return post.Translation{}, domain.ErrNotFound
		}
		return post.Translation{}, err
	}
	t.ReadingTime = post.ReadingTime(t.Body)
	return t, nil
}

func (r *PostRepo) DeleteTranslation(ctx context.Context, postID int, lang string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM post_translations WHERE post_id = $1 AND lang = $2`, postID, lang)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
