package application

import (
	"context"
	"errors"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// GetTranslation returns a post's translation if the viewer may read the post.
func (b *Blog) GetTranslation(ctx context.Context, postID, viewerID int, lang string) (post.Translation, error) {
	ctx, span := tracer.Start(ctx, "Blog.GetTranslation")
	defer span.End()
	if !post.ValidLang(lang) {
		return post.Translation{}, domain.Invalid("lang: uz, ru or en")
	}
	if _, err := b.readable(ctx, postID, viewerID); err != nil {
		return post.Translation{}, err
	}
	if b.Translations == nil {
		return post.Translation{}, domain.ErrNotFound
	}
	t, err := b.Translations.GetTranslation(ctx, postID, lang)
	if err != nil {
		return t, err
	}
	if b.Markdown != nil {
		t.BodyHTML = b.rendered.render(b.Markdown, t.Body)
	}
	return t, nil
}

// SaveTranslation creates or replaces a translation of a post the actor may edit.
func (b *Blog) SaveTranslation(ctx context.Context, actor user.User, postID int, lang string, in post.TranslationInput) (post.Translation, error) {
	ctx, span := tracer.Start(ctx, "Blog.SaveTranslation")
	defer span.End()
	if err := b.requireVerified(actor); err != nil {
		return post.Translation{}, err
	}
	if b.Translations == nil {
		return post.Translation{}, errors.New("translations not configured")
	}
	p, err := b.translationTarget(ctx, actor, postID, lang)
	if err != nil {
		return post.Translation{}, err
	}
	if lang == p.Lang {
		return post.Translation{}, domain.Invalid("that is the original language")
	}
	in, err = in.Normalize()
	if err != nil {
		return post.Translation{}, err
	}
	t, err := b.Translations.SaveTranslation(ctx, postID, lang, in)
	if err != nil {
		return t, err
	}
	if b.Markdown != nil {
		t.BodyHTML = b.rendered.render(b.Markdown, t.Body)
	}
	return t, nil
}

// DeleteTranslation removes a translation of a post the actor may edit.
func (b *Blog) DeleteTranslation(ctx context.Context, actor user.User, postID int, lang string) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteTranslation")
	defer span.End()
	if b.Translations == nil {
		return domain.ErrNotFound
	}
	if _, err := b.translationTarget(ctx, actor, postID, lang); err != nil {
		return err
	}
	return b.Translations.DeleteTranslation(ctx, postID, lang)
}

// translationTarget loads the post and checks lang and the update permission.
func (b *Blog) translationTarget(ctx context.Context, actor user.User, postID int, lang string) (post.Post, error) {
	if !post.ValidLang(lang) {
		return post.Post{}, domain.Invalid("lang: uz, ru or en")
	}
	p, err := b.Posts.Get(ctx, postID, 0)
	if err != nil {
		return p, err
	}
	if err := b.Authz.Can(ctx, "post", "update", postID, p.UserID); err != nil {
		if !errors.Is(err, domain.ErrForbidden) || !b.isPublicationEditor(ctx, actor, p.PublicationID) {
			return post.Post{}, err
		}
	}
	return p, nil
}
