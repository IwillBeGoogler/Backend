package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// The rows of one project as they go into the dg_* tables, with user ids
// already mapped to the blog's. Nullable user columns are *int (nil: the
// user has no account here).

type dgProject struct {
	ID        string
	OwnerID   int
	Name      string
	TreeRev   int
	CreatedAt time.Time
	UpdatedAt time.Time
	UpdatedBy *int
}

type dgFolder struct {
	ID, ProjectID string
	ParentID      *string
	Name          string
	Position      float64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type dgFile struct {
	ID, ProjectID string
	FolderID      *string
	Name          string
	Kind          string
	FileKind      string
	Position      float64
	Data          json.RawMessage
	Version       int
	PageCount     int
	ThumbnailHash *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	UpdatedBy     *int
}

type dgVersion struct {
	ID, FileID string
	Version    int
	Name       string
	Data       json.RawMessage
	Label      *string
	Named      bool
	CreatedBy  *int
	CreatedAt  time.Time
}

type dgMember struct {
	ParentID  string // project or file id
	UserID    int
	Role      string
	CreatedAt time.Time
}

type dgShare struct {
	ID, ProjectID string
	FileID        *string
	TokenHash     string
	Role          string
	ExpiresAt     *time.Time
	RevokedAt     *time.Time
	CreatedBy     *int
	CreatedAt     time.Time
}

type dgLibrary struct {
	ProjectID string
	Rev       int
	Data      json.RawMessage
	UpdatedAt time.Time
	UpdatedBy *int
}

type dgPrivate struct {
	FileID    string
	UserID    int
	Data      json.RawMessage
	UpdatedAt time.Time
}

type dgComment struct {
	ID, FileID, ProjectID string
	ParentID              *string
	AuthorID              *int
	Body                  string
	Anchor                json.RawMessage // nil: none
	ResolvedAt            *time.Time
	ResolvedBy            *int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type dgMention struct {
	CommentID string
	UserID    int
}

type dgRead struct {
	FileID string
	UserID int
	ReadAt time.Time
}

// dgNotification goes into the blog's notifications table.
type dgNotification struct {
	UserID    int
	Type      string // diagram_mention | diagram_reply | diagram_comment
	ActorID   *int
	ProjectID string
	FileID    string
	CommentID *string
	Text      string
	ReadAt    *time.Time
	CreatedAt time.Time
}

type dgWebhook struct {
	ID, ProjectID, URL, Secret string
	Events                     []string
	Active                     bool
	CreatedBy                  *int
	CreatedAt                  time.Time
}

type dgYDoc struct {
	FileID    string
	Snapshot  []byte
	Seq       int64
	UpdatedAt time.Time
}

type dgYUpdate struct {
	FileID    string
	Seq       int64
	Data      []byte
	CreatedAt time.Time
}

// projectBundle is everything of one project, imported in one transaction.
type projectBundle struct {
	Project     dgProject
	Folders     []dgFolder // parents before children
	Files       []dgFile
	Versions    []dgVersion
	Members     []dgMember
	FileMembers []dgMember
	Shares      []dgShare
	Library     *dgLibrary
	Private     []dgPrivate
	Comments    []dgComment // parents before replies
	Mentions    []dgMention
	Reads       []dgRead
	Notifs      []dgNotification
	Webhooks    []dgWebhook
	YDocs       []dgYDoc
	YUpdates    []dgYUpdate

	counts  map[string]int // what the bundle holds, by entity
	skipped map[string]int
	issues  []problem
}

// globalBundle is what is not owned by a project.
type globalBundle struct {
	Assets []dgAsset
	Prefs  []dgPrefs
	Tokens []dgToken
	Audit  []dgAudit
}

type dgAsset struct {
	Hash, Mime string
	Size       int
	UploadedBy *int
	CreatedAt  time.Time
}

type dgPrefs struct {
	UserID    int
	Data      json.RawMessage
	UpdatedAt time.Time
}

type dgToken struct {
	ID         string
	UserID     int
	Name       string
	Hash       string
	Prefix     string
	Scopes     []string
	ProjectIDs []string // nil: every project of the owner
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

type dgAudit struct {
	ID         string
	UserID     int
	TokenID    *string
	Tool       string
	ProjectID  *string
	FileID     *string
	Status     string
	Detail     *string
	DurationMs *int
	CreatedAt  time.Time
}

// newUser is an account to make for an old user without one here.
type newUser struct {
	Username, Email, Name string
	// PasswordHash is the old argon2id hash, kept so the password still works.
	PasswordHash string
}

// store is the target: the blog's Postgres. pgStore writes; dryStore only
// answers lookups and records (dry run, tests).
type store interface {
	// Users maps lower(email) to the blog user id.
	Users(ctx context.Context) (map[string]int, error)
	// CreateUser returns domain.ErrConflict for a taken username or email.
	CreateUser(ctx context.Context, u newUser) (int, error)
	ProjectExists(ctx context.Context, id string) (bool, error)
	// ImportProject inserts the bundle in one transaction.
	ImportProject(ctx context.Context, b *projectBundle) error
	// ImportGlobal inserts what it can and returns the rows really inserted
	// by entity (existing rows are left alone).
	ImportGlobal(ctx context.Context, g *globalBundle) (map[string]int, error)
}

type problem struct{ Entity, ID, Msg string }

func (b *projectBundle) add(entity string) { b.counts[entity]++ }

func (b *projectBundle) problem(entity, id, msg string) {
	b.skipped[entity]++
	b.issues = append(b.issues, problem{entity, id, msg})
}

// warn records an issue that did not cost a row.
func (b *projectBundle) warn(entity, id, msg string) {
	b.issues = append(b.issues, problem{entity, id, msg})
}

// report accumulates the run.
type report struct {
	imported map[string]int
	existing map[string]int
	skipped  map[string]int
	problems []problem
	failed   []string // projects whose transaction failed
}

func newReport() *report {
	return &report{imported: map[string]int{}, existing: map[string]int{}, skipped: map[string]int{}}
}

func (r *report) problem(entity, id, msg string) {
	r.skipped[entity]++
	r.problems = append(r.problems, problem{entity, id, msg})
}

func (r *report) warn(entity, id, msg string) {
	r.problems = append(r.problems, problem{entity, id, msg})
}

func (r *report) merge(b *projectBundle) {
	for k, n := range b.counts {
		r.imported[k] += n
	}
	for k, n := range b.skipped {
		r.skipped[k] += n
	}
	r.problems = append(r.problems, b.issues...)
}

// print writes the summary; verbose lists every problem, else the first 40.
func (r *report) print(w io.Writer, dry bool, verbose bool) {
	verb := "imported"
	if dry {
		verb = "would import"
	}
	names := map[string]bool{}
	for _, m := range []map[string]int{r.imported, r.existing, r.skipped} {
		for k := range m {
			names[k] = true
		}
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(w, "\n%-22s %12s %10s %10s\n", "entity", verb, "existing", "skipped")
	for _, k := range keys {
		fmt.Fprintf(w, "%-22s %12d %10d %10d\n", k, r.imported[k], r.existing[k], r.skipped[k])
	}
	if len(r.failed) > 0 {
		fmt.Fprintf(w, "\n%d project(s) FAILED and were rolled back (rerun to retry): %v\n", len(r.failed), r.failed)
	}
	fmt.Fprintf(w, "\n%d problem(s)\n", len(r.problems))
	for i, p := range r.problems {
		if !verbose && i == 40 {
			fmt.Fprintf(w, "  ... %d more (use -v)\n", len(r.problems)-i)
			break
		}
		fmt.Fprintf(w, "  %s %s: %s\n", p.Entity, p.ID, p.Msg)
	}
}
