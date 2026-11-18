package post

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

// Langs are the languages a post can be written or translated in.
var Langs = []string{"uz", "ru", "en"}

// ValidLang tells whether s is a supported language code.
func ValidLang(s string) bool { return slices.Contains(Langs, s) }

// Translation is a post's title, subtitle and body in a language other than its original.
type Translation struct {
	PostID      int       `json:"post_id"`
	Lang        string    `json:"lang"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	Body        string    `json:"body"`
	BodyHTML    string    `json:"body_html,omitempty"` // sanitized; filled on reads
	ReadingTime int       `json:"reading_time"`        // minutes
	UpdatedAt   time.Time `json:"updated_at"`
}

// TranslationInput is the editable part of a translation.
type TranslationInput struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Body     string `json:"body"`
}

// Normalize validates in and trims it.
func (in TranslationInput) Normalize() (TranslationInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Subtitle = strings.TrimSpace(in.Subtitle)
	switch {
	case in.Title == "":
		return in, domain.Invalid("title required")
	case len(in.Title) > maxTitle:
		return in, domain.Invalid("title too long")
	case len(in.Subtitle) > maxSub:
		return in, domain.Invalid("subtitle too long")
	case len(in.Body) > maxBody:
		return in, domain.Invalid("body too long")
	}
	if err := ValidateLive(in.Body); err != nil {
		return in, err
	}
	return in, nil
}

// TranslationRepository stores translations; missing ones give domain.ErrNotFound.
type TranslationRepository interface {
	GetTranslation(ctx context.Context, postID int, lang string) (Translation, error)
	SaveTranslation(ctx context.Context, postID int, lang string, in TranslationInput) (Translation, error)
	DeleteTranslation(ctx context.Context, postID int, lang string) error
}
