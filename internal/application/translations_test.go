package application

import (
	"context"
	"errors"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
)

type fakeTranslations struct {
	saved map[string]post.TranslationInput
}

func (f *fakeTranslations) GetTranslation(_ context.Context, id int, lang string) (post.Translation, error) {
	in, ok := f.saved[lang]
	if !ok {
		return post.Translation{}, domain.ErrNotFound
	}
	return post.Translation{PostID: id, Lang: lang, Title: in.Title}, nil
}

func (f *fakeTranslations) SaveTranslation(_ context.Context, id int, lang string, in post.TranslationInput) (post.Translation, error) {
	f.saved[lang] = in
	return post.Translation{PostID: id, Lang: lang, Title: in.Title}, nil
}

func (f *fakeTranslations) DeleteTranslation(_ context.Context, _ int, lang string) error {
	if _, ok := f.saved[lang]; !ok {
		return domain.ErrNotFound
	}
	delete(f.saved, lang)
	return nil
}

func TestSaveTranslation(t *testing.T) {
	w := newWorld(1)
	tr := &fakeTranslations{saved: map[string]post.TranslationInput{}}
	w.blog.Translations = tr
	w.posts.posts[1] = post.Post{ID: 1, UserID: 1, Lang: "uz", Status: post.StatusPublished}
	in := post.TranslationInput{Title: "Hello", Body: "body"}

	if _, err := w.blog.SaveTranslation(ctx, ali, 1, "uz", in); !isInvalid(err) {
		t.Fatalf("original lang: err = %v, want invalid", err)
	}
	if _, err := w.blog.SaveTranslation(ctx, ali, 1, "xx", in); !isInvalid(err) {
		t.Fatalf("bad lang: err = %v, want invalid", err)
	}
	if _, err := w.blog.SaveTranslation(ctx, ali, 1, "en", post.TranslationInput{}); !isInvalid(err) {
		t.Fatalf("empty title: err = %v, want invalid", err)
	}
	if got, err := w.blog.SaveTranslation(ctx, ali, 1, "en", in); err != nil || got.Title != "Hello" {
		t.Fatalf("owner save: %v %+v", err, got)
	}
	if got, err := w.blog.GetTranslation(ctx, 1, 0, "en"); err != nil || got.Title != "Hello" {
		t.Fatalf("get: %v %+v", err, got)
	}

	other := newWorld(2)
	other.blog.Translations = tr
	other.posts.posts[1] = w.posts.posts[1]
	if _, err := other.blog.SaveTranslation(ctx, vali, 1, "en", in); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stranger save: err = %v, want forbidden", err)
	}
	if err := other.blog.DeleteTranslation(ctx, vali, 1, "en"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stranger delete: err = %v, want forbidden", err)
	}
	if err := w.blog.DeleteTranslation(ctx, ali, 1, "en"); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func isInvalid(err error) bool {
	var v *domain.ValidationError
	return errors.As(err, &v)
}
