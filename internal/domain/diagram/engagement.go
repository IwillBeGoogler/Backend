package diagram

import "context"

// MaxClaps is how many claps one reader may give one diagram (as for posts).
const MaxClaps = 50

// ValidateClaps checks how many claps one request adds.
func ValidateClaps(n int) error {
	if n < 1 || n > MaxClaps {
		return Validation("count: 1-50")
	}
	return nil
}

// Viewer is what one reader did to a published diagram.
type Viewer struct {
	MyClaps    int
	Bookmarked bool
}

// EngagementRepository stores claps and bookmarks of published diagrams
// (unlisted ones too: they open by link). Methods take the file id of a
// published file; the application resolves the slug with PublicListing.
// domain.ErrNotFound means a missing or private file.
type EngagementRepository interface {
	// PublicListing is the page of an unlisted or public file by slug, without its document.
	PublicListing(ctx context.Context, slug string) (Listing, error)
	// Clap adds n claps (capped at MaxClaps per reader). It returns the
	// file's claps total, the reader's claps and whether this was the
	// reader's first.
	Clap(ctx context.Context, fileID string, userID, n int) (total, mine int, created bool, err error)
	Bookmark(ctx context.Context, fileID string, userID int) error
	Unbookmark(ctx context.Context, fileID string, userID int) error
	// Viewer reads what userID did to the file.
	Viewer(ctx context.Context, fileID string, userID int) (Viewer, error)
	// ListBookmarked returns one page of the public and unlisted files the
	// user bookmarked, newest bookmark first, and the total.
	ListBookmarked(ctx context.Context, userID, page, limit int) ([]Listing, int, error)
}
