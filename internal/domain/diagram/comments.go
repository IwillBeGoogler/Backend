package diagram

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Comments: a thread is a root comment pinned to the canvas (an anchor) and
// flat replies to it. Rules and wire shapes are those of the Node server.

// PrefixComment is the id prefix of a comment ("cmt_" + 16 hex chars).
const PrefixComment = "cmt"

// MaxCommentChars caps the body of one comment.
const MaxCommentChars = 4000

// Comment is one comment. Anchor is set on a root only.
type Comment struct {
	ID        string
	FileID    string
	ProjectID string
	ParentID  *string
	AuthorID  int // 0 once the account is gone
	Body      string
	Anchor    json.RawMessage
	// ResolvedAt and ResolvedBy are set on a resolved root.
	ResolvedAt *time.Time
	ResolvedBy int
	// Mentions are the members mentioned when it was written (not the
	// author), in order of appearance.
	Mentions  []int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CommentRepository stores comments. Like Repository it returns
// domain.ErrNotFound for a missing comment.
type CommentRepository interface {
	Comment(ctx context.Context, id string) (Comment, error)
	// Comments lists a file's comments oldest first, then by id, with their
	// mentions.
	Comments(ctx context.Context, fileID string) ([]Comment, error)
	// InsertComment stores the comment and its mentions.
	InsertComment(ctx context.Context, c Comment) error
	// UpdateComment stores the update time and only the parts being patched
	// (body, resolved state), so concurrent edits of the other part survive.
	UpdateComment(ctx context.Context, c Comment, setBody, setResolved bool) error
	// DeleteComment deletes the comment; a root takes its replies with it.
	// It returns the ids deleted (the comment first).
	DeleteComment(ctx context.Context, id string) ([]string, error)
	// ReadAt is when the user last read the file's comments (zero: never).
	ReadAt(ctx context.Context, fileID string, userID int) (time.Time, error)
	MarkRead(ctx context.Context, fileID string, userID int, at time.Time) error
}

// Comment errors, with the codes the Node server reports.
func AnchorRequired() error {
	return invalid("ANCHOR_REQUIRED", "A comment needs an anchor: a canvas point or a node")
}
func ReplyToReply() error {
	return invalid("REPLY_TO_REPLY", "Replies go to the first comment of a thread")
}
func ResolveRootOnly() error {
	return invalid("RESOLVE_ROOT_ONLY", "Resolve the thread, not a reply")
}
func NothingToChange() error { return invalid("BAD_REQUEST", "Nothing to change") }

// CheckCommentBody applies the body rules: 1 to 4000 characters, not trimmed.
func CheckCommentBody(body string) error {
	n := utf8.RuneCountInString(body)
	if n < 1 || n > MaxCommentChars {
		return Validation("body: must be 1-4000 characters")
	}
	return nil
}

// ParseAnchor validates an anchor and returns it with only its known keys:
// {"type":"point","x","y"} or {"type":"node","nodeId","dx","dy"}.
func ParseAnchor(raw json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Type   string   `json:"type"`
		X      *float64 `json:"x"`
		Y      *float64 `json:"y"`
		NodeID *string  `json:"nodeId"`
		DX     *float64 `json:"dx"`
		DY     *float64 `json:"dy"`
	}
	bad := func(msg string) (json.RawMessage, error) { return nil, Validation("anchor: " + msg) }
	if json.Unmarshal(raw, &a) != nil {
		return bad("expected a point or node anchor")
	}
	finite := func(v *float64) bool { return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) }
	var out any
	switch a.Type {
	case "point":
		if !finite(a.X) || !finite(a.Y) {
			return bad("x and y must be numbers")
		}
		out = map[string]any{"type": "point", "x": *a.X, "y": *a.Y}
	case "node":
		if a.NodeID == nil || *a.NodeID == "" || utf8.RuneCountInString(*a.NodeID) > 100 {
			return bad("nodeId must be 1-100 characters")
		}
		if !finite(a.DX) || !finite(a.DY) {
			return bad("dx and dy must be numbers")
		}
		out = map[string]any{"type": "node", "nodeId": *a.NodeID, "dx": *a.DX, "dy": *a.DY}
	default:
		return bad("type must be point or node")
	}
	b, err := json.Marshal(out)
	return b, err
}

var mentionRe = regexp.MustCompile(`@\[([^\]]{1,100})\]\(user:([A-Za-z0-9_-]{1,64})\)`)

var mentionBlank = strings.NewReplacer("[", " ", "]", " ", "(", " ", ")", " ", "\n", " ", "\r", " ")

// FormatMention is the token the editor's @-autocomplete inserts,
// "@[Display Name](user:<id>)"; brackets in the name are blanked so it
// cannot break out of the token.
func FormatMention(name string, userID int) string {
	n := mentionBlank.Replace(name)
	if r := []rune(n); len(r) > 100 {
		n = string(r[:100])
	}
	return "@[" + n + "](user:" + strconv.Itoa(userID) + ")"
}

// ParseMentions returns the user ids mentioned in a body, in order and
// without duplicates. Tokens whose id is not a user id are ignored.
func ParseMentions(body string) []int {
	var out []int
	seen := map[int]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(body, -1) {
		id, err := strconv.Atoi(m[2])
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// Unread counts the comments the user has not seen: other people's,
// created after readAt.
func Unread(cs []Comment, userID int, readAt time.Time) int {
	n := 0
	for _, c := range cs {
		if c.AuthorID != userID && c.CreatedAt.After(readAt) {
			n++
		}
	}
	return n
}
