package application

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// Realtime events of the diagram workspace. Use cases tell DiagramEvents
// what changed; the realtime interface decides who hears about it (the
// project's tree channel, a file's channel, connections on other API
// instances) and how it looks on the wire. An event never fails the use
// case that raised it.

// DiagramEvent kinds.
const (
	// EventTree: folders or files were created, moved, renamed or deleted
	// (Rev is the project's new tree revision, Reason says what happened).
	EventTree = "tree.updated"
	// EventFile: a document was saved.
	EventFile = "file.updated"
	// EventFileDeleted: a file is gone; its channels close.
	EventFileDeleted = "file.deleted"
	// EventProjectDeleted: a project is gone; every channel of it closes.
	EventProjectDeleted = "project.deleted"
	// EventLibrary: the project's component library changed.
	EventLibrary = "library.updated"
	// EventRestored: the document was replaced by an old version; live
	// editing sessions must start over.
	EventRestored = "file.restored"
	// EventCommentCreated, EventCommentUpdated, EventCommentDeleted.
	EventCommentCreated = "comment.created"
	EventCommentUpdated = "comment.updated"
	EventCommentDeleted = "comment.deleted"
)

// DiagramActor is who caused an event: a person, or a share link.
type DiagramActor struct {
	UserID int // 0 for a share link
	Name   string
	Share  bool
}

// DiagramEvent describes a change.
type DiagramEvent struct {
	Kind      string
	ProjectID string
	FileID    string // file events; comment events
	FolderID  string // tree events
	FileIDs   []string
	Reason    string // tree events: "file.created", "folder.deleted", ...
	Rev       int    // tree and library events
	By        DiagramActor
	// ClientID is the connection that made the change (X-Client-Id); it
	// does not hear its own event.
	ClientID string

	// file.updated
	Version   int
	Name      string
	FileKind  string
	UpdatedAt int64
	Restored  bool

	// comment events
	Comment   *CommentView
	CommentID string
}

// DiagramEvents receives events (nil on Diagrams: none are sent).
type DiagramEvents interface {
	Emit(ctx context.Context, e DiagramEvent)
}

type clientIDKey struct{}

// WithClientID marks ctx with the realtime connection id a request came
// from (the X-Client-Id header), so its own events skip that connection.
func WithClientID(ctx context.Context, id string) context.Context {
	if id == "" || len(id) > 64 {
		return ctx
	}
	return context.WithValue(ctx, clientIDKey{}, id)
}

func clientID(ctx context.Context) string {
	id, _ := ctx.Value(clientIDKey{}).(string)
	return id
}

// actorOf names who is acting for an event.
func (d *Diagrams) actorOf(ctx context.Context, w writer) DiagramActor {
	if w.user == 0 {
		return DiagramActor{Share: true}
	}
	return DiagramActor{UserID: w.user, Name: d.people(ctx, w.user)[w.user].Name}
}

// emit sends e, filling in the connection id.
func (d *Diagrams) emit(ctx context.Context, e DiagramEvent) {
	if d.Events == nil {
		return
	}
	e.ClientID = clientID(ctx)
	d.Events.Emit(ctx, e)
}

// treeChanged tells a project's channel that its tree changed (rev is what
// TouchTree returned).
func (d *Diagrams) treeChanged(ctx context.Context, projectID string, actor, rev int, reason, folderID, fileID string) {
	if d.Events == nil {
		return
	}
	d.emit(ctx, DiagramEvent{Kind: EventTree, ProjectID: projectID, Rev: rev, Reason: reason, FolderID: folderID, FileID: fileID,
		By: d.actorOf(ctx, writer{user: actor})})
}

// touchFor bumps the tree revision and announces it.
func (d *Diagrams) touchFor(ctx context.Context, projectID string, actor int, reason, folderID, fileID string) (int, error) {
	rev, err := d.touch(ctx, projectID, actor)
	if err == nil {
		d.treeChanged(ctx, projectID, actor, rev, reason, folderID, fileID)
	}
	return rev, err
}

// ChannelAccess is what a realtime connection to a file or a project is
// allowed: its role, who it is shown as, and where the document stands.
type ChannelAccess struct {
	ProjectID string
	FileID    string // "" for a project channel
	Role      diagram.Role
	Version   int // file channels
	TreeRev   int // project channels
	// UserID is 0 for a share-link guest.
	UserID int
	Name   string
}

// OpenChannel checks that the caller may open the realtime channel of a file
// (scope "file", id a file id) or a project (scope "project"), with the same
// rules as the REST routes: the viewer role at least, a member first and a
// share link only for someone who is not a member (a file link opens its own
// file, a project link the project and every file in it; a file link never
// opens a project channel). actor is 0 for an anonymous caller.
func (d *Diagrams) OpenChannel(ctx context.Context, actor int, token, scope, id string) (ChannelAccess, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.OpenChannel")
	defer span.End()
	out := ChannelAccess{UserID: actor, Name: "Guest"}
	var err error
	if actor != 0 { // a member first; the token only for someone who is not one
		out, err = d.channelAs(ctx, actor, "", scope, id)
		if err == nil {
			out.Name = d.people(ctx, actor)[actor].Name
			return out, nil
		}
		if token == "" || !errors.Is(err, domain.ErrNotFound) {
			return ChannelAccess{}, err
		}
	} else if token == "" {
		return ChannelAccess{}, domain.ErrUnauthorized
	}
	out, err = d.channelAs(ctx, 0, token, scope, id)
	out.UserID, out.Name = 0, "Guest"
	return out, err
}

func (d *Diagrams) channelAs(ctx context.Context, actor int, token, scope, id string) (ChannelAccess, error) {
	if scope == "file" {
		a, f, err := d.fileAccessVia(ctx, actor, token, id, diagram.RoleViewer)
		return ChannelAccess{ProjectID: f.ProjectID, FileID: f.ID, Role: a.role, Version: f.Version, UserID: actor}, err
	}
	a, err := d.projectAccessVia(ctx, actor, token, id, diagram.RoleViewer)
	return ChannelAccess{ProjectID: a.project.ID, Role: a.role, TreeRev: a.project.TreeRev, UserID: actor}, err
}

// CheckShare reports whether token is a live share link.
func (d *Diagrams) CheckShare(ctx context.Context, token string) error {
	_, err := d.resolveShare(ctx, token)
	return err
}

// ShareLinkOf returns the project and id of a live share link by token.
func (d *Diagrams) ShareLinkOf(ctx context.Context, token string) (projectID, shareID string, err error) {
	sa, err := d.resolveShare(ctx, token)
	return sa.link.ProjectID, sa.link.ID, err
}

// resetY forgets the stored Yjs state of a file whose document was replaced,
// trying twice. It reports whether the state is gone.
func (d *Diagrams) resetY(ctx context.Context, fileID string) bool {
	if d.Y == nil {
		return true
	}
	var err error
	for range 2 {
		if err = d.Y.YReset(ctx, fileID); err == nil || errors.Is(err, domain.ErrNotFound) {
			return true
		}
	}
	zap.L().Error("diagram: reset yjs state", zap.String("file", fileID), zap.Error(err))
	return false
}
