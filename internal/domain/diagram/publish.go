package diagram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Visibility is who may open a file without being a member: nobody
// (private), anyone with the link (unlisted) or anyone, with the file shown
// on its author's profile and in search (public).
type Visibility string

const (
	VisPrivate  Visibility = "private"
	VisUnlisted Visibility = "unlisted"
	VisPublic   Visibility = "public"
)

// Published reports whether the file can be opened by its slug.
func (v Visibility) Published() bool { return v == VisUnlisted || v == VisPublic }

// Valid reports whether v can be asked for when publishing.
func (v Visibility) Valid() bool { return v == VisUnlisted || v == VisPublic }

// Limits of the published page.
const (
	MaxPublishTitle = 120
	MaxPublishDesc  = 500
	MaxPublishTags  = 10
	MaxTagLen       = 40
)

// Publication is a file's blog page: what the author chose when publishing.
type Publication struct {
	Visibility  Visibility
	Slug        string
	Title       string
	Description string
	Tags        []string
	PublishedAt time.Time
}

// Listing is a file's blog page with the file's metadata and its author;
// it carries no document. Visibility is private for a file never published
// or since unpublished (the slug is kept so the link survives republishing);
// PublishedAt is then zero and Slug may be empty.
type Listing struct {
	Publication
	FileID        string
	ProjectID     string
	Name          string
	Kind          string
	Version       int
	PageCount     int
	ThumbnailHash *string
	UpdatedAt     time.Time
	// Claps is the total claps of all readers.
	Claps int

	AuthorID       int
	AuthorUsername string
	AuthorName     string
	AuthorAvatar   string
}

// PublicDoc is a published file with its document.
type PublicDoc struct {
	Listing
	Data json.RawMessage
}

// PublicQuery filters the public listing, newest published first.
type PublicQuery struct {
	Q        string // case-insensitive substring of the title or description; % and _ are literal
	Tag      string
	AuthorID int
	Page     int
	Limit    int
}

// PublishRepository stores the blog pages of files. It returns
// domain.ErrNotFound for a missing file or slug and domain.ErrConflict for
// a slug already in use.
type PublishRepository interface {
	// SetPublication publishes the file (or updates its page).
	SetPublication(ctx context.Context, fileID string, p Publication) error
	// ClearPublication makes the file private again; it keeps the slug,
	// title, description and tags.
	ClearPublication(ctx context.Context, fileID string) error
	// Listing returns the file's page whatever its visibility.
	Listing(ctx context.Context, fileID string) (Listing, error)
	// PublicBySlug returns an unlisted or public file with its document.
	PublicBySlug(ctx context.Context, slug string) (PublicDoc, error)
	// ListPublic returns one page of public files (newest published first)
	// and the total.
	ListPublic(ctx context.Context, q PublicQuery) ([]Listing, int, error)
	// ListOwned returns one page of the unlisted and public files of the
	// projects userID owns (newest published first) and the total.
	ListOwned(ctx context.Context, userID, page, limit int) ([]Listing, int, error)
	// ReferencedPublic returns the unlisted and public files among ids
	// (file ids or slugs), keyed by the id as given.
	ReferencedPublic(ctx context.Context, refs []string) (map[string]Listing, error)
}

// NormalizeTag lower-cases and trims a tag.
func NormalizeTag(t string) string { return strings.ToLower(strings.TrimSpace(t)) }

// CleanTags normalizes, de-duplicates and validates tags.
func CleanTags(tags []string) ([]string, error) {
	out := []string{}
	for _, t := range tags {
		t = NormalizeTag(t)
		if t == "" || slices.Contains(out, t) {
			continue
		}
		if utf8.RuneCountInString(t) > MaxTagLen || controlChars.MatchString(t) {
			return nil, Validation("tags: each tag is 1-40 characters without control characters")
		}
		out = append(out, t)
	}
	if len(out) > MaxPublishTags {
		return nil, Validation("tags: at most 10")
	}
	return out, nil
}

// CleanPublication validates what an author sends when publishing; the
// title falls back to the file's name.
func CleanPublication(vis Visibility, title, description string, tags []string, fileName string) (Publication, error) {
	if !vis.Valid() {
		return Publication{}, Validation("visibility: unlisted or public")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = fileName
	}
	description = strings.TrimSpace(description)
	switch {
	case controlChars.MatchString(title):
		return Publication{}, Validation("title: no control characters")
	case title == "":
		return Publication{}, Validation("title is required")
	case utf8.RuneCountInString(title) > MaxPublishTitle:
		return Publication{}, Validation("title: at most 120 characters")
	case utf8.RuneCountInString(description) > MaxPublishDesc:
		return Publication{}, Validation("description: at most 500 characters")
	}
	tags, err := CleanTags(tags)
	if err != nil {
		return Publication{}, err
	}
	return Publication{Visibility: vis, Title: title, Description: description, Tags: tags}, nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,118}[a-z0-9]$`)

// ValidSlug reports whether s is a slug we hand out.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// NewSlug builds a slug from a title: lower-case ASCII words joined by
// dashes plus a random suffix, so equal titles never collide for long.
func NewSlug(title string) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(title) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if len(base) > 60 {
		base = strings.Trim(base[:60], "-")
	}
	var r [4]byte
	_, _ = rand.Read(r[:])
	suffix := hex.EncodeToString(r[:])
	if base == "" {
		return "diagram-" + suffix
	}
	return base + "-" + suffix
}
