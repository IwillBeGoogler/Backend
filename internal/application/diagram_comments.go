package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
)

// Comments on a diagram file. Reading needs the viewer role, writing the
// commenter role; strangers get not found. Share links never comment: they
// carry no account.

// errNoComments is a Diagrams built without a comment store.
var errNoComments = errors.New("diagram comments are not configured")

// Person is an account as shown next to a comment; Name is "Someone" once
// the account is gone (ID is then 0).
type Person struct {
	ID   int
	Name string
}

// CommentView is a comment with its people resolved.
type CommentView struct {
	diagram.Comment
	Author     Person
	ResolvedBy *Person
}

// CommentThread is a root comment with its replies, oldest first.
type CommentThread struct {
	Root    CommentView
	Replies []CommentView
}

// CommentList is a file's threads (oldest root first), the number of
// comments the caller has not read and when they last read them.
type CommentList struct {
	Threads []CommentThread
	Unread  int
	ReadAt  time.Time
}

// NewComment is a comment to add: a root (Anchor, a point or a node) or a
// reply (ParentID, the root of a thread).
type NewComment struct {
	Body     string
	Anchor   json.RawMessage
	ParentID string
}

// people resolves accounts to how they are shown.
func (d *Diagrams) people(ctx context.Context, ids ...int) map[int]Person {
	out := map[int]Person{}
	for _, id := range ids {
		if _, done := out[id]; done || id == 0 {
			continue
		}
		p := Person{ID: id, Name: "Someone"}
		if d.Users != nil {
			if u, err := d.Users.GetByID(ctx, id); err == nil {
				p.Name = accountName(u)
			}
		}
		out[id] = p
	}
	return out
}

func (d *Diagrams) commentViews(ctx context.Context, cs []diagram.Comment) []CommentView {
	var ids []int
	for _, c := range cs {
		ids = append(ids, c.AuthorID, c.ResolvedBy)
	}
	who := d.people(ctx, ids...)
	person := func(id int) Person {
		if p, ok := who[id]; ok {
			return p
		}
		return Person{Name: "Someone"}
	}
	out := make([]CommentView, len(cs))
	for i, c := range cs {
		out[i] = CommentView{Comment: c, Author: person(c.AuthorID)}
		if c.ResolvedBy != 0 {
			p := person(c.ResolvedBy)
			out[i].ResolvedBy = &p
		}
	}
	return out
}

// FileComments lists a file's threads and how many comments the caller has not
// read. Viewers and up.
func (d *Diagrams) FileComments(ctx context.Context, actor int, fileID string) (CommentList, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.FileComments")
	defer span.End()
	if d.Comments == nil {
		return CommentList{}, errNoComments
	}
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleViewer); err != nil {
		return CommentList{}, err
	}
	cs, err := d.Comments.Comments(ctx, fileID)
	if err != nil {
		return CommentList{}, err
	}
	readAt, err := d.Comments.ReadAt(ctx, fileID, actor)
	if err != nil {
		return CommentList{}, err
	}
	views := d.commentViews(ctx, cs)
	list := CommentList{Threads: []CommentThread{}, Unread: diagram.Unread(cs, actor, readAt), ReadAt: readAt}
	at := map[string]int{}
	for _, v := range views {
		if v.ParentID == nil {
			at[v.ID] = len(list.Threads)
			list.Threads = append(list.Threads, CommentThread{Root: v, Replies: []CommentView{}})
		}
	}
	for _, v := range views {
		if v.ParentID == nil {
			continue
		}
		if i, ok := at[*v.ParentID]; ok {
			list.Threads[i].Replies = append(list.Threads[i].Replies, v)
		}
	}
	return list, nil
}

// mentionable keeps the ids that are the project's owner or a member, in
// order: only they can be mentioned.
func (d *Diagrams) mentionable(ctx context.Context, p diagram.Project, ids []int) ([]int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ok := map[int]bool{p.OwnerID: true}
	if d.Sharing != nil {
		members, err := d.Sharing.Members(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			ok[m.UserID] = true
		}
	}
	var out []int
	for _, id := range ids {
		if ok[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// AddComment adds a root comment pinned to a canvas point or node, or a
// reply to the root of a thread. Members mentioned (@[Name](user:<id>)) are
// notified, and so is the author of the thread a reply goes to. Needs the
// commenter role.
func (d *Diagrams) AddComment(ctx context.Context, actor int, fileID string, in NewComment) (CommentView, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.AddComment")
	defer span.End()
	if d.Comments == nil {
		return CommentView{}, errNoComments
	}
	if err := diagram.CheckCommentBody(in.Body); err != nil {
		return CommentView{}, err
	}
	var anchor json.RawMessage
	if len(in.Anchor) > 0 && string(in.Anchor) != "null" {
		var err error
		if anchor, err = diagram.ParseAnchor(in.Anchor); err != nil {
			return CommentView{}, err
		}
	}
	if in.ParentID != "" && len(in.ParentID) > 64 {
		return CommentView{}, diagram.Validation("parentId: must be 1-64 characters")
	}
	a, f, err := d.fileAccess(ctx, actor, fileID, diagram.RoleCommenter)
	if err != nil {
		return CommentView{}, err
	}
	var parent *diagram.Comment
	if in.ParentID != "" {
		p, err := d.Comments.Comment(ctx, in.ParentID)
		if errors.Is(err, domain.ErrNotFound) || err == nil && p.FileID != f.ID {
			return CommentView{}, diagram.NotFound("Comment")
		}
		if err != nil {
			return CommentView{}, err
		}
		if p.ParentID != nil {
			return CommentView{}, diagram.ReplyToReply()
		}
		parent, anchor = &p, nil
	} else if anchor == nil {
		return CommentView{}, diagram.AnchorRequired()
	}
	mentioned, err := d.mentionable(ctx, a.project, diagram.ParseMentions(in.Body))
	if err != nil {
		return CommentView{}, err
	}
	mentioned = slices.DeleteFunc(mentioned, func(id int) bool { return id == actor })
	now := d.clock()
	c := diagram.Comment{ID: diagram.NewID(diagram.PrefixComment), FileID: f.ID, ProjectID: a.project.ID, AuthorID: actor,
		Body: in.Body, Anchor: anchor, Mentions: mentioned, CreatedAt: now, UpdatedAt: now}
	if parent != nil {
		c.ParentID = &parent.ID
	}
	if err := d.Comments.InsertComment(ctx, c); err != nil {
		return CommentView{}, err
	}
	view := d.commentViews(ctx, []diagram.Comment{c})[0]
	d.notifyComment(ctx, c, f.Name, view.Author.Name, parent)
	d.emit(ctx, DiagramEvent{Kind: EventCommentCreated, ProjectID: a.project.ID, FileID: f.ID, Comment: &view})
	return view, nil
}

// notifyComment tells the members a comment mentions, then the author of
// the thread it replies to; nobody twice, never the author of the comment.
func (d *Diagrams) notifyComment(ctx context.Context, c diagram.Comment, file, author string, parent *diagram.Comment) {
	if d.Notices == nil {
		return
	}
	var told []int
	notify := func(user int, kind social.NotificationType, text string) {
		if slices.Contains(told, user) {
			return
		}
		told = append(told, user)
		d.Notices.NotifyDiagram(ctx, social.Notification{Type: kind, ActorID: c.AuthorID, DiagramProjectID: c.ProjectID,
			DiagramFileID: c.FileID, DiagramCommentID: c.ID, Text: text}, user)
	}
	for _, id := range c.Mentions {
		notify(id, social.NotifyDiagramMention, author+" mentioned you in "+file)
	}
	if parent != nil && parent.AuthorID != 0 && parent.AuthorID != c.AuthorID {
		notify(parent.AuthorID, social.NotifyDiagramReply, author+" replied to your comment in "+file)
	}
}

// CommentPatch is an edit of a comment: its text (only the author may) and/or
// the resolved state of a thread root (any commenter).
type CommentPatch struct {
	Body     *string
	Resolved *bool
}

// commentAccess loads a comment and checks the caller's role on its file.
func (d *Diagrams) commentAccess(ctx context.Context, actor int, id string, min diagram.Role) (diagram.Comment, access, error) {
	if d.Comments == nil {
		return diagram.Comment{}, access{}, errNoComments
	}
	c, err := d.Comments.Comment(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return c, access{}, diagram.NotFound("Comment")
	}
	if err != nil {
		return c, access{}, err
	}
	a, _, err := d.fileAccess(ctx, actor, c.FileID, min)
	return c, a, err
}

// UpdateComment edits your comment, or resolves or reopens a thread (its
// root). Needs the commenter role.
func (d *Diagrams) UpdateComment(ctx context.Context, actor int, id string, in CommentPatch) (CommentView, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.UpdateComment")
	defer span.End()
	if in.Body != nil {
		if err := diagram.CheckCommentBody(*in.Body); err != nil {
			return CommentView{}, err
		}
	}
	c, a, err := d.commentAccess(ctx, actor, id, diagram.RoleCommenter)
	if err != nil {
		return CommentView{}, err
	}
	setBody, setResolved := in.Body != nil, in.Resolved != nil
	changed := false
	if in.Body != nil {
		if c.AuthorID != actor {
			return CommentView{}, diagram.Forbidden("Only the author can edit a comment")
		}
		c.Body, changed = *in.Body, true
	}
	now := d.clock()
	if in.Resolved != nil {
		if c.ParentID != nil {
			return CommentView{}, diagram.ResolveRootOnly()
		}
		if *in.Resolved {
			c.ResolvedAt, c.ResolvedBy = &now, actor
		} else {
			c.ResolvedAt, c.ResolvedBy = nil, 0
		}
		changed = true
	}
	if !changed {
		return CommentView{}, diagram.NothingToChange()
	}
	c.UpdatedAt = now
	if err := d.Comments.UpdateComment(ctx, c, setBody, setResolved); err != nil {
		return CommentView{}, err
	}
	view := d.commentViews(ctx, []diagram.Comment{c})[0]
	d.emit(ctx, DiagramEvent{Kind: EventCommentUpdated, ProjectID: a.project.ID, FileID: c.FileID, Comment: &view})
	return view, nil
}

// DeleteComment deletes your comment (a project owner may delete any); a
// thread root takes its replies with it. It returns the ids deleted.
func (d *Diagrams) DeleteComment(ctx context.Context, actor int, id string) ([]string, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.DeleteComment")
	defer span.End()
	c, a, err := d.commentAccess(ctx, actor, id, diagram.RoleViewer)
	if err != nil {
		return nil, err
	}
	if c.AuthorID != actor && a.role != diagram.RoleOwner {
		return nil, diagram.Forbidden("Only the author or a project owner can delete a comment")
	}
	ids, err := d.Comments.DeleteComment(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, diagram.NotFound("Comment")
	}
	if err == nil {
		for _, gone := range ids {
			d.emit(ctx, DiagramEvent{Kind: EventCommentDeleted, ProjectID: a.project.ID, FileID: c.FileID, CommentID: gone})
		}
	}
	return ids, err
}

// MarkCommentsRead marks a file's comments read for the caller (clears the
// unread count). Viewers and up.
func (d *Diagrams) MarkCommentsRead(ctx context.Context, actor int, fileID string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.MarkCommentsRead")
	defer span.End()
	if d.Comments == nil {
		return errNoComments
	}
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleViewer); err != nil {
		return err
	}
	return d.Comments.MarkRead(ctx, fileID, actor, d.clock())
}

// Notifications

// DiagramNotification is a diagram notification of the blog's system, with
// its actor resolved.
type DiagramNotification struct {
	social.Notification
	By *Person // who did it; nil when the account is gone
}

// NotificationList is a user's diagram notifications and the unread count
// among all of them.
type NotificationList struct {
	Items  []DiagramNotification
	Unread int
}

// MaxNotifications caps one listing; DefaultNotifications is its default.
const (
	MaxNotifications     = 200
	DefaultNotifications = 50
)

// Notifications lists the caller's diagram notifications (mentions,
// replies), newest first. limit 0 means the default.
func (d *Diagrams) Notifications(ctx context.Context, actor int, unreadOnly bool, limit int) (NotificationList, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.Notifications")
	defer span.End()
	if d.Notices == nil {
		return NotificationList{Items: []DiagramNotification{}}, nil
	}
	if limit <= 0 {
		limit = DefaultNotifications
	}
	items, unread, err := d.Notices.DiagramNotifications(ctx, actor, unreadOnly, min(limit, MaxNotifications))
	if err != nil {
		return NotificationList{}, err
	}
	out := NotificationList{Items: make([]DiagramNotification, len(items)), Unread: unread}
	who := map[int]*Person{}
	for i, n := range items {
		p, seen := who[n.ActorID]
		if !seen && n.ActorID != 0 {
			if pp, ok := d.people(ctx, n.ActorID)[n.ActorID]; ok {
				p = &pp
			}
			who[n.ActorID] = p
		}
		out.Items[i] = DiagramNotification{Notification: n, By: p}
	}
	return out, nil
}

// ReadNotifications marks the caller's diagram notifications read: the
// given ids (ones that are not theirs, or not numeric, are ignored), or all.
func (d *Diagrams) ReadNotifications(ctx context.Context, actor int, ids []string, all bool) error {
	ctx, span := tracer.Start(ctx, "Diagrams.ReadNotifications")
	defer span.End()
	if d.Notices == nil {
		return nil
	}
	var nums []int64
	for _, s := range ids {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
			nums = append(nums, n)
		}
	}
	return d.Notices.MarkDiagramRead(ctx, actor, nums, all)
}
