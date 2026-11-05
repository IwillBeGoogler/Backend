// Package diagram holds the diagram workspace: projects of folders and
// files, where a file is one JSON document with optimistic versioning and a
// snapshot history.
package diagram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
)

// Role is what a user may do in a project (or, with a per-file override, in
// one file). The project's owner is always an owner.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	// RoleCommenter reads and comments but cannot edit.
	RoleCommenter Role = "commenter"
	RoleViewer    Role = "viewer"
)

// Rank orders roles; 0 means no access.
func (r Role) Rank() int {
	switch r {
	case RoleOwner:
		return 4
	case RoleEditor:
		return 3
	case RoleCommenter:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// Allows reports whether r is at least min.
func (r Role) Allows(min Role) bool { return r.Rank() >= min.Rank() && r.Rank() > 0 }

// OverrideRole reports whether r may be set as a per-file override.
func (r Role) OverrideRole() bool { return r == RoleViewer || r == RoleCommenter || r == RoleEditor }

// ID prefixes, as in the Node server ("prj_" + 16 hex chars).
const (
	PrefixProject = "prj"
	PrefixFolder  = "fld"
	PrefixFile    = "fil"
	PrefixVersion = "ver"
)

// NewID returns a fresh id such as "prj_9f86d081884c7d65".
func NewID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

// Opt is an optional request field that tells "absent" from null: Set is
// false when the key was missing; Val is the zero value for null.
type Opt[T any] struct {
	Set bool
	Val T
}

func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	return json.Unmarshal(b, &o.Val)
}

// Project is a container of folders and files.
type Project struct {
	ID      string
	OwnerID int
	Name    string
	// TreeRev is bumped on every tree mutation (create, move, rename,
	// delete) so clients can reconcile.
	TreeRev   int
	CreatedAt time.Time
	UpdatedAt time.Time
	UpdatedBy int // 0 when unknown
}

// ProjectSummary is a project as listed: with the caller's role and counts.
type ProjectSummary struct {
	Project
	Role        Role
	FileCount   int
	FolderCount int
	// ThumbFileID and ThumbnailHash are the preview of the project's first
	// file (by position, then name); the hash is nil when it has none.
	ThumbFileID   string
	ThumbnailHash *string
}

// ProjectQuery filters and orders the project list.
type ProjectQuery struct {
	Page, PageSize int
	Search         string // case-insensitive substring of the name; % and _ are literal
	SortByName     bool   // else by last update
	Asc            bool
}

type Folder struct {
	ID        string
	ProjectID string
	ParentID  *string
	Name      string
	// Position sorts among the siblings; folders and files share one sequence.
	Position  float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// File is a file's metadata: the tree never carries documents.
type File struct {
	ID            string
	ProjectID     string
	FolderID      *string
	Name          string
	Kind          string // sdd | erd | uml | board
	Position      float64
	Version       int
	PageCount     int
	ThumbnailHash *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	UpdatedBy     int
	// UpdatedByShare is the id of the share link that made the last save
	// (UpdatedBy is then 0); "" for a person.
	UpdatedByShare string
	// Restored says the head was produced by restoring an old version, not
	// by an ordinary save.
	Restored bool
}

// Doc is a file with its document (JSON, the frontend's Project v2).
type Doc struct {
	File
	Data json.RawMessage
}

// Version is a snapshot of a file. Data is empty in listings.
type Version struct {
	Version   int
	Name      string
	Label     *string
	Named     bool
	CreatedBy int
	// CreatedByShare is the share link that saved it (CreatedBy is then 0).
	CreatedByShare string
	// CreatedByName is the author's name, filled only by Versions (empty when the account is gone).
	CreatedByName string
	CreatedAt     time.Time
	Data          json.RawMessage
}

// NewFile is a file to insert together with its first snapshot.
type NewFile struct {
	Doc   Doc
	First Version
}

// Repository returns domain.ErrNotFound for a missing project, folder, file
// or version. Ids are unique across all projects.
type Repository interface {
	// CreateProject returns domain.ErrConflict when the id is taken.
	CreateProject(ctx context.Context, p Project) error
	Project(ctx context.Context, id string) (Project, error)
	// ListProjects returns one page of the projects userID owns or belongs
	// to, and the total.
	ListProjects(ctx context.Context, userID int, q ProjectQuery) ([]ProjectSummary, int, error)
	Counts(ctx context.Context, projectID string) (files, folders int, err error)
	// ProjectThumb is the preview of the project's first file: its id and
	// thumbnail hash ("" ids and nil hash for a project without files).
	ProjectThumb(ctx context.Context, projectID string) (fileID string, hash *string, err error)
	RenameProject(ctx context.Context, id, name string, at time.Time) error
	// TouchTree bumps tree_rev and returns the new value.
	TouchTree(ctx context.Context, id string, by int, at time.Time) (int, error)
	// DeleteProject removes the project with everything in it.
	DeleteProject(ctx context.Context, id string) error
	// MemberRole is the user's role as a member, not counting ownership
	// ("" for a stranger).
	MemberRole(ctx context.Context, projectID string, userID int) (Role, error)
	// FileRole is the user's per-file override ("" for none).
	FileRole(ctx context.Context, fileID string, userID int) (Role, error)

	// Tree lists a project's folders and files ordered by position, name.
	Tree(ctx context.Context, projectID string) ([]Folder, []File, error)
	Folder(ctx context.Context, id string) (Folder, error)
	// CreateFolder returns domain.ErrConflict when the id is taken.
	CreateFolder(ctx context.Context, f Folder) error
	UpdateFolder(ctx context.Context, f Folder) error
	// NextPosition is the sort key after the last sibling in parentID (nil:
	// the project root).
	NextPosition(ctx context.Context, projectID string, parentID *string) (float64, error)
	// DeleteFolders removes the folders, the files inside them and those
	// files' history; it returns the number of files removed.
	DeleteFolders(ctx context.Context, projectID string, ids []string) (int, error)
	// InsertTree stores folders and files in one transaction.
	InsertTree(ctx context.Context, folders []Folder, files []NewFile) error

	File(ctx context.Context, id string) (Doc, error)
	// InsertFile returns domain.ErrConflict when the id is taken.
	InsertFile(ctx context.Context, f NewFile) error
	// UpdateFileMeta stores name, kind, folder, position and updated_at.
	UpdateFileMeta(ctx context.Context, f File) error
	// SaveFile stores a new document and its snapshot only if the file is
	// still at version expected; false means someone else saved first.
	SaveFile(ctx context.Context, f Doc, expected int, v Version) (bool, error)
	// SaveFileOver is SaveFile, but the snapshot of version expected is
	// rewritten into v instead of adding a new one (one snapshot per
	// editing burst). It inserts v when that snapshot no longer exists.
	SaveFileOver(ctx context.Context, f Doc, expected int, v Version) (bool, error)
	DeleteFile(ctx context.Context, id string) error

	// Versions lists snapshots newest first, without data.
	Versions(ctx context.Context, fileID string) ([]Version, error)
	GetVersion(ctx context.Context, fileID string, version int) (Version, error)
	// VersionInfo is GetVersion without the data.
	VersionInfo(ctx context.Context, fileID string, version int) (Version, error)
	// CountVersions is the number of stored snapshots of the file.
	CountVersions(ctx context.Context, fileID string) (int, error)
	// NameHead names the file's current version: it labels the head's
	// snapshot (creating it when pruning or an import left none) and
	// exempts it from pruning.
	NameHead(ctx context.Context, fileID, label string, by int, at time.Time) (Version, error)
	DeleteVersions(ctx context.Context, fileID string, versions []int) error
}

// PrefsRepository keeps one small JSON object per user.
type PrefsRepository interface {
	// Prefs returns the object, {} when the user has none.
	Prefs(ctx context.Context, userID int) (json.RawMessage, error)
	// UpdatePrefs runs apply on the current object under a row lock and
	// stores what it returns.
	UpdatePrefs(ctx context.Context, userID int, at time.Time, apply func(cur json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error)
}

// Error is a domain error with the stable code the API reports. Kind says
// what went wrong in general: domain.ErrNotFound, domain.ErrForbidden,
// domain.ErrConflict, ErrPrecondition, ErrTooLarge or a domain.ValidationError.
type Error struct {
	Kind error
	Code string
	Msg  string
	// Current is the file's version on a failed save (ErrPrecondition).
	Current int
	// Restored says the current version came from a restore (VERSION_CONFLICT).
	Restored bool
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Kind }

// ErrTooLarge is a payload over its size limit.
var ErrTooLarge = errors.New("too large")

// TooLarge is a payload over its size limit (PAYLOAD_TOO_LARGE).
func TooLarge(msg string) error {
	return &Error{Kind: ErrTooLarge, Code: "PAYLOAD_TOO_LARGE", Msg: msg}
}

// ErrPrecondition is a failed If-Match: the file changed or the caller did
// not say which version it saw.
var ErrPrecondition = errors.New("precondition failed")

func invalid(code, msg string) error {
	return &Error{Kind: domain.Invalid(msg), Code: code, Msg: msg}
}

// Validation is a request that breaks a rule (VALIDATION_ERROR).
func Validation(msg string) error { return invalid("VALIDATION_ERROR", msg) }

func conflict(code, msg string) error {
	return &Error{Kind: domain.ErrConflict, Code: code, Msg: msg}
}

// NotFound is a missing (or invisible) resource: "<what> not found".
func NotFound(what string) error {
	return &Error{Kind: domain.ErrNotFound, Code: "NOT_FOUND", Msg: what + " not found"}
}

// NeedRole is a caller whose role is too low.
func NeedRole(min Role) error {
	return &Error{Kind: domain.ErrForbidden, Code: "FORBIDDEN", Msg: `This action needs the "` + string(min) + `" role`}
}

var (
	ErrIDTaken            = conflict("ID_TAKEN", "That id is already in use")
	ErrCycle              = conflict("CYCLE", "A folder cannot be moved into itself or one of its subfolders")
	ErrParentNotFound     = invalid("PARENT_NOT_FOUND", "Parent folder not found in this project")
	ErrFolderNotInProject = invalid("PARENT_NOT_FOUND", "Folder not found in this project")
)

// VersionConflict is a save against a version that is no longer the head.
// restored says the current version is a restore, which must never be
// overwritten by a stale copy.
func VersionConflict(current int, restored bool) error {
	return &Error{Kind: ErrPrecondition, Code: "VERSION_CONFLICT", Current: current, Restored: restored,
		Msg: "The file was changed by someone else. Fetch the latest version and merge."}
}

// IfMatchRequired is a save without an If-Match header.
func IfMatchRequired(current int) error {
	return &Error{Kind: ErrPrecondition, Code: "IF_MATCH_REQUIRED", Current: current,
		Msg: "This request needs an If-Match header with the version you last saw"}
}
