package diagram

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

// Admin views of the workspace for moderators. They look at every project
// whoever owns it, and never carry a document or a share token.

// AdminStats are the workspace totals.
type AdminStats struct {
	Projects        int
	Files           int
	FilesByKind     map[string]int
	ActiveEditors7d int
	StorageBytes    int64
}

// AdminUser is an account as the admin shows it.
type AdminUser struct {
	ID       int
	Username string
}

// AdminProject is a row of the project list.
type AdminProject struct {
	ID            string
	Name          string
	Owner         AdminUser
	Files         int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ThumbFileID   string
	ThumbnailHash *string
	Shared        bool // at least one active share link
}

// AdminFile is a file of a project, without its document.
type AdminFile struct {
	ID            string
	Name          string
	Kind          string
	Version       int
	UpdatedAt     time.Time
	ThumbnailHash *string
}

// AdminMember is a project member.
type AdminMember struct {
	User     AdminUser
	Role     Role
	JoinedAt time.Time
}

// AdminShare is a share link; revoked and expired ones are listed too.
type AdminShare struct {
	ID        string
	Role      Role
	CreatedBy string // username, "" when the account is gone
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

// AdminProjectDetail is a project with its files, members and links.
type AdminProjectDetail struct {
	AdminProject
	FileList []AdminFile
	Members  []AdminMember
	Shares   []AdminShare
}

// AdminProjectQuery filters the project list, newest update first. The
// cursor is the number of rows already seen.
type AdminProjectQuery struct {
	Search string // case-insensitive substring of the name; % and _ are literal
	Owner  string // exact username (case-insensitive)
	Offset int
	Limit  int
}

// AdminRepository reads the whole workspace. It returns domain.ErrNotFound
// for a missing project or share link.
type AdminRepository interface {
	Stats(ctx context.Context, since time.Time) (AdminStats, error)
	// Projects returns up to q.Limit+1 rows, so the caller can tell whether
	// more follow.
	Projects(ctx context.Context, q AdminProjectQuery) ([]AdminProject, error)
	ProjectDetail(ctx context.Context, id string) (AdminProjectDetail, error)
	// RevokeShareByID revokes a link whatever its project; it keeps an
	// earlier revocation time and returns the link's project.
	RevokeShareByID(ctx context.Context, id string, at time.Time) (projectID string, err error)
}

// Templates

// Template kinds: the lens of the gallery.
const (
	TemplateSystem = "system"
	TemplateDB     = "db"
	TemplateOOD    = "ood"
)

// ValidTemplateKind reports whether k is a gallery kind.
func ValidTemplateKind(k string) bool {
	return k == TemplateSystem || k == TemplateDB || k == TemplateOOD
}

// PrefixTemplate is the id prefix of a template ("tpl_" + 16 hex chars).
const PrefixTemplate = "tpl"

// Template limits.
const (
	MaxTemplateTitle = 120
	MaxTemplateDesc  = 500
)

// Template is a published (or draft) entry of the gallery: a pointer at a
// diagram file that users fork into their own project.
type Template struct {
	ID          string
	Title       string
	Kind        string
	Description string
	FileID      string
	Published   bool
	Position    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// ThumbnailHash and PageCount come from the source file; the repository
	// fills them on reads and ignores them on writes.
	ThumbnailHash *string
	PageCount     int
}

// CleanTemplate validates a template's own fields (not that the file exists).
func CleanTemplate(t Template) (Template, error) {
	t.Title = strings.TrimSpace(t.Title)
	t.Description = strings.TrimSpace(t.Description)
	switch {
	case t.Title == "" || utf8.RuneCountInString(t.Title) > MaxTemplateTitle:
		return t, Validation("title: 1-120 characters")
	case controlChars.MatchString(t.Title):
		return t, Validation("title: no control characters")
	case utf8.RuneCountInString(t.Description) > MaxTemplateDesc:
		return t, Validation("description: at most 500 characters")
	case !ValidTemplateKind(t.Kind):
		return t, Validation("kind: system, db or ood")
	case t.FileID == "":
		return t, Validation("file_id is required")
	case t.Position < 0:
		return t, Validation("position: 0 or more")
	}
	return t, nil
}

// TemplateRepository stores the gallery. It returns domain.ErrNotFound for a
// missing template and domain.ErrConflict when a template's file is missing.
type TemplateRepository interface {
	// List returns templates ordered by position, then title; onlyPublished
	// leaves out the drafts.
	List(ctx context.Context, onlyPublished bool) ([]Template, error)
	Get(ctx context.Context, id string) (Template, error)
	Create(ctx context.Context, t Template) error
	// Update stores every field but id and created_at.
	Update(ctx context.Context, t Template) error
	Delete(ctx context.Context, id string) error
	// Reorder puts the templates named by ids first, in that order, then the
	// others in their current order, and renumbers all from 0, in one
	// transaction. An unknown id is domain.ErrNotFound.
	Reorder(ctx context.Context, ids []string, at time.Time) error
}
