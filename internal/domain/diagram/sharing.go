package diagram

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

// PrefixShare is the id prefix of a share link ("shr_" + 16 hex chars).
const PrefixShare = "shr"

// Member is a project member (or a per-file role override). Name and Email
// are filled from the account by the use case, not by the repository.
type Member struct {
	UserID    int
	Email     string
	Name      string
	Role      Role
	CreatedAt time.Time
	// IsOriginalOwner marks the project's owner in a member list.
	IsOriginalOwner bool
}

// ShareLink opens a whole project (FileID nil) or one file with a role. The
// token is never stored, only its hash.
type ShareLink struct {
	ID        string
	ProjectID string
	FileID    *string
	Role      Role // viewer | editor
	ExpiresAt *time.Time
	RevokedAt *time.Time
	CreatedBy int // 0 when the creator's account is gone
	CreatedAt time.Time
	TokenHash string
}

// Expired reports whether the link has run out at now.
func (s ShareLink) Expired(now time.Time) bool { return s.ExpiresAt != nil && !s.ExpiresAt.After(now) }

// Share limits.
const (
	MinShareSeconds = 60
	MaxShareSeconds = 365 * 24 * 3600
)

var shareTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// ValidShareToken reports whether s can be a share token (shape only).
func ValidShareToken(s string) bool { return shareTokenRe.MatchString(s) }

// NewShareToken returns a random 128-bit token (base64url, 22 characters)
// and its hash, the only part that is stored.
func NewShareToken() (token, hash string) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashToken(token)
}

// HashToken is the lowercase hex sha256 of a token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// HashEqual compares two token hashes in constant time.
func HashEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// Asset is an uploaded image, addressed by the sha256 of its bytes.
type Asset struct {
	Hash       string
	Mime       string
	Size       int
	UploadedBy int
	CreatedAt  time.Time
}

// Image types accepted for uploads. DetectMime only sniffs rasters; SVG is
// accepted by the application layer after it has been sanitized (and only
// when a sanitizer is configured).
const (
	MimePNG  = "image/png"
	MimeJPEG = "image/jpeg"
	MimeWebP = "image/webp"
	MimeSVG  = "image/svg+xml"
)

var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidHash reports whether s is a lowercase hex sha256.
func ValidHash(s string) bool { return hashRe.MatchString(s) }

// DetectMime sniffs the real type from the magic bytes (the client's
// declared type is never trusted); "" when it is not an accepted image.
func DetectMime(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return MimePNG
	case len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff:
		return MimeJPEG
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return MimeWebP
	}
	return ""
}

// HashBytes is the lowercase hex sha256 of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Library is a project's shared components, styles and tokens. Rev 0 with a
// nil Doc means never saved.
type Library struct {
	Rev int
	Doc json.RawMessage
}

// MaxLibraryBytes caps one library document.
const MaxLibraryBytes = 2 << 20

// EmptyLibrary is the document of a project that never saved one.
var EmptyLibrary = json.RawMessage(`{"components":{},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{}}`)

// LibraryConflict is a save against a revision that is no longer current;
// Current is what to merge with.
type LibraryConflict struct{ Current Library }

func (*LibraryConflict) Error() string {
	return "The library was changed by someone else. Merge with the returned document and retry."
}

// Code is the stable error code the API reports.
func (*LibraryConflict) Code() string { return "LIBRARY_CONFLICT" }

// ErrLibraryConflict matches a *LibraryConflict with errors.Is.
var ErrLibraryConflict = errors.New("library conflict")

func (*LibraryConflict) Is(target error) bool { return target == ErrLibraryConflict }

// Private stickies.

// MaxPrivateBytes caps one user's private stickies of a file.
const MaxPrivateBytes = 1 << 20

// Private is a person's private stickies of a file.
type Private struct {
	Data      json.RawMessage // {"nodes": [...], "edges": [...]}
	UpdatedAt time.Time       // zero when there is none
}

// EmptyPrivate is what a person with no private stickies has.
var EmptyPrivate = json.RawMessage(`{"nodes":[],"edges":[]}`)

// SharingRepository stores sharing: members, share links, assets, libraries
// and private stickies. Like Repository it returns domain.ErrNotFound for a
// missing row where a method says so.
type SharingRepository interface {
	// Members lists a project's members (not its owner) oldest first;
	// Email and Name are empty.
	Members(ctx context.Context, projectID string) ([]Member, error)
	// SetMember adds the member or changes its role.
	SetMember(ctx context.Context, projectID string, userID int, role Role, at time.Time) error
	// UpdateMember changes a role; false when the user is not a member.
	UpdateMember(ctx context.Context, projectID string, userID int, role Role) (bool, error)
	// RemoveMember removes the member and its per-file overrides in the
	// project; false when the user is not a member.
	RemoveMember(ctx context.Context, projectID string, userID int) (bool, error)
	FileMembers(ctx context.Context, fileID string) ([]Member, error)
	SetFileMember(ctx context.Context, fileID string, userID int, role Role, at time.Time) error
	// RemoveFileMember reports whether there was an override.
	RemoveFileMember(ctx context.Context, fileID string, userID int) (bool, error)

	CreateShare(ctx context.Context, s ShareLink) error
	// Shares lists a project's links newest first.
	Shares(ctx context.Context, projectID string) ([]ShareLink, error)
	// RevokeShare reports whether the project has such a link.
	RevokeShare(ctx context.Context, projectID, id string, at time.Time) (bool, error)
	// SharesExpiredBetween lists links that expired in (from, to].
	SharesExpiredBetween(ctx context.Context, from, to time.Time) ([]ShareLink, error)
	// ShareByHash returns domain.ErrNotFound for an unknown hash.
	ShareByHash(ctx context.Context, hash string) (ShareLink, error)

	Asset(ctx context.Context, hash string) (Asset, error)
	// InsertAsset reports whether the row is new (false: it existed).
	InsertAsset(ctx context.Context, a Asset) (bool, error)
	// SetThumbnail points a file at a preview asset; it does not touch the
	// file's version.
	SetThumbnail(ctx context.Context, fileID, hash string) error

	// Library returns Rev 0 when the project never saved one.
	Library(ctx context.Context, projectID string) (Library, error)
	// SaveLibrary stores doc as revision baseRev+1 only if the library is
	// still at baseRev (0: none yet); false means someone else saved first.
	SaveLibrary(ctx context.Context, projectID string, baseRev int, doc json.RawMessage, by int, at time.Time) (bool, error)

	// Private returns domain.ErrNotFound when the user has none.
	Private(ctx context.Context, fileID string, userID int) (Private, error)
	PutPrivate(ctx context.Context, fileID string, userID int, data json.RawMessage, at time.Time) error
	DeletePrivate(ctx context.Context, fileID string, userID int) error
}

// More errors.

// ErrGone is a resource that existed and has run out (an expired share).
var ErrGone = errors.New("gone")

// ErrUnsupportedMedia is content of a type the API does not accept.
var ErrUnsupportedMedia = errors.New("unsupported media type")

// ShareExpired is a share link past its expiry (SHARE_EXPIRED).
func ShareExpired() error {
	return &Error{Kind: ErrGone, Code: "SHARE_EXPIRED", Msg: "This share link has expired"}
}

// Forbidden is a caller who may not do this, with a message of its own.
func Forbidden(msg string) error {
	return &Error{Kind: domain.ErrForbidden, Code: "FORBIDDEN", Msg: msg}
}

// ReadOnlyShare is a write through a viewer link.
func ReadOnlyShare() error {
	return &Error{Kind: domain.ErrForbidden, Code: "FORBIDDEN", Msg: "This share link is read-only"}
}

// UnsupportedMedia is a body of a type that is not accepted.
func UnsupportedMedia(msg string) error {
	return &Error{Kind: ErrUnsupportedMedia, Code: "UNSUPPORTED_MEDIA_TYPE", Msg: msg}
}

// UserNotFound is an invitation of an unknown account.
func UserNotFound() error {
	return &Error{Kind: domain.ErrNotFound, Code: "USER_NOT_FOUND", Msg: "No account with that email or username. Ask them to sign up first."}
}

// OwnerImmutable is an attempt to change, remove or override the owner.
func OwnerImmutable(msg string) error { return invalid("OWNER_IMMUTABLE", msg) }

// FileNotInProject is a share link for a file of another project.
func FileNotInProject() error { return invalid("FILE_NOT_FOUND", "File not found in this project") }

// MissingFile is an upload without a "file" field.
func MissingFile() error { return invalid("MISSING_FILE", `Missing "file" field`) }

// Validation of the library and private stickies documents. Unknown keys are
// kept (the frontend adds fields over time); known ones are type-checked.

type checker struct{ err error }

func (c *checker) fail(path, msg string) {
	if c.err == nil {
		c.err = Validation(path + ": " + msg)
	}
}

func (c *checker) object(path string, v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		c.fail(path, "expected an object")
	}
	return m
}

func (c *checker) array(path string, v any, max int) []any {
	a, ok := v.([]any)
	switch {
	case !ok:
		c.fail(path, "expected an array")
	case len(a) > max:
		c.fail(path, fmt.Sprintf("at most %d items", max))
	}
	return a
}

// str checks a string of 1..max characters (min 0 when empty is fine).
func (c *checker) str(path string, m map[string]any, key string, min, max int, required bool) {
	v, has := m[key]
	if !has {
		if required {
			c.fail(path+"."+key, "Required")
		}
		return
	}
	s, ok := v.(string)
	if !ok {
		c.fail(path+"."+key, "expected a string")
		return
	}
	if n := len([]rune(s)); n < min || n > max {
		c.fail(path+"."+key, fmt.Sprintf("must be %d-%d characters", min, max))
	}
}

func (c *checker) num(path string, m map[string]any, key string, required bool) {
	v, has := m[key]
	if !has {
		if required {
			c.fail(path+"."+key, "Required")
		}
		return
	}
	if f, ok := v.(float64); !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		c.fail(path+"."+key, "expected a number")
	}
}

func (c *checker) obj(path string, m map[string]any, key string, required bool) map[string]any {
	v, has := m[key]
	if !has {
		if required {
			c.fail(path+"."+key, "Required")
		}
		return nil
	}
	return c.object(path+"."+key, v)
}

// CheckLibrary validates a library document (the shape of the frontend's
// library: components, styles and tokens).
func CheckLibrary(raw json.RawMessage) error {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Validation("doc: expected an object")
	}
	c := &checker{}
	root := c.object("doc", doc)
	if c.err != nil {
		return c.err
	}
	for k, v := range c.obj("doc", root, "components", true) {
		p := "doc.components." + k
		comp := c.object(p, v)
		if comp == nil {
			break
		}
		c.str(p, comp, "id", 1, 64, true)
		c.str(p, comp, "name", 0, 200, true)
		for _, f := range []string{"rev", "width", "height"} {
			c.num(p, comp, f, true)
		}
		c.str(p, comp, "mainFileId", 1, 64, false)
		c.str(p, comp, "mainNodeId", 1, 64, false)
		nodes, ok := comp["nodes"]
		if !ok {
			c.fail(p+".nodes", "Required")
		} else {
			for i, nv := range c.array(p+".nodes", nodes, 2000) {
				np := fmt.Sprintf("%s.nodes.%d", p, i)
				if n := c.object(np, nv); n != nil {
					c.str(np, n, "key", 1, 64, true)
					c.str(np, n, "type", 1, 32, true)
					for _, f := range []string{"x", "y"} {
						c.num(np, n, f, true)
					}
					c.num(np, n, "width", false)
					c.num(np, n, "height", false)
					c.str(np, n, "parentKey", 1, 64, false)
					c.obj(np, n, "data", true)
				}
			}
		}
		if edges, ok := comp["edges"]; ok {
			for i, ev := range c.array(p+".edges", edges, 4000) {
				ep := fmt.Sprintf("%s.edges.%d", p, i)
				if e := c.object(ep, ev); e != nil {
					for _, f := range []string{"key", "source", "target"} {
						c.str(ep, e, f, 1, 64, true)
					}
					c.obj(ep, e, "data", false)
					c.str(ep, e, "label", 0, 2000, false)
				}
			}
		}
	}
	if styles := c.obj("doc", root, "styles", true); styles != nil {
		each := func(group string, check func(p string, m map[string]any)) {
			for k, v := range c.obj("doc.styles", styles, group, true) {
				p := "doc.styles." + group + "." + k
				if m := c.object(p, v); m != nil {
					c.str(p, m, "id", 1, 64, true)
					c.str(p, m, "name", 0, 200, true)
					check(p, m)
				}
			}
		}
		each("colors", func(p string, m map[string]any) {
			c.str(p, m, "value", 0, 2000, true)
			c.str(p, m, "dark", 0, 2000, false)
		})
		each("texts", func(p string, m map[string]any) {
			c.num(p, m, "fontSize", false)
			if v, ok := m["bold"]; ok {
				if _, isBool := v.(bool); !isBool {
					c.fail(p+".bold", "expected a boolean")
				}
			}
			c.str(p, m, "align", 0, 2000, false)
			c.str(p, m, "color", 0, 2000, false)
		})
		each("effects", func(p string, m map[string]any) {
			sh, has := m["shadow"]
			if !has {
				c.fail(p+".shadow", "Required")
			} else if sh != nil {
				if s := c.object(p+".shadow", sh); s != nil {
					for _, f := range []string{"x", "y", "blur"} {
						c.num(p+".shadow", s, f, true)
					}
					c.str(p+".shadow", s, "color", 0, 2000, true)
				}
			}
		})
	}
	for k, v := range c.obj("doc", root, "tokens", true) {
		p := "doc.tokens." + k
		if m := c.object(p, v); m != nil {
			c.str(p, m, "id", 1, 64, true)
			c.str(p, m, "name", 0, 200, true)
			c.str(p, m, "light", 0, 2000, true)
			c.str(p, m, "dark", 0, 2000, true)
		}
	}
	return c.err
}

// PreparePrivate validates a person's private stickies ({"nodes", "edges"})
// and stamps every sticky as private and written by author, whatever the
// client sent. It returns the document to store.
func PreparePrivate(raw json.RawMessage, author string) (json.RawMessage, error) {
	var in struct {
		Nodes any `json:"nodes"`
		Edges any `json:"edges"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, Validation("body: expected an object")
	}
	c := &checker{}
	nodes := c.array("nodes", in.Nodes, 500)
	edges := c.array("edges", in.Edges, 1000)
	for i, v := range nodes {
		p := fmt.Sprintf("nodes.%d", i)
		n := c.object(p, v)
		if n == nil {
			break
		}
		c.str(p, n, "id", 1, 64, true)
		if t, _ := n["type"].(string); t != "sticky" {
			c.fail(p+".type", `must be "sticky"`)
		}
		if pos := c.obj(p, n, "position", true); pos != nil {
			c.num(p+".position", pos, "x", true)
			c.num(p+".position", pos, "y", true)
		}
		for _, f := range []string{"width", "height"} {
			if v, ok := n[f]; ok {
				if x, isNum := v.(float64); !isNum || x <= 0 || x > 10000 {
					c.fail(p+"."+f, "must be a positive number up to 10000")
				}
			}
		}
		if data := c.obj(p, n, "data", true); data != nil {
			c.str(p+".data", data, "label", 0, 10_000, true)
			data["private"], data["authorId"] = true, author
		}
	}
	for i, v := range edges {
		p := fmt.Sprintf("edges.%d", i)
		if e := c.object(p, v); e != nil {
			for _, f := range []string{"id", "source", "target"} {
				c.str(p, e, f, 1, 64, true)
			}
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	out, err := json.Marshal(map[string]any{"nodes": nodes, "edges": edges})
	if err != nil {
		return nil, err
	}
	if len(out) > MaxPrivateBytes {
		return nil, TooLarge("Private stickies may be at most 1 MB per file")
	}
	return out, nil
}
