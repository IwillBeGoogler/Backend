package application

// Comments, notifications, the commenter role, private-sticky scrubbing on
// every save path, share-link attribution and project thumbnails, on the
// in-memory repository.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/diagramtest"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
)

const secret = "my-secret-text"

var point = json.RawMessage(`{"type":"point","x":10,"y":20}`)

type commentEnv struct {
	sharingEnv
	notices *diagramtest.Notices
	pid     string
	fid     string
}

// newComments is a project owned by alice with bob (editor), cara (commenter)
// and vic (viewer) as members.
func newComments(t *testing.T) commentEnv {
	t.Helper()
	e := newSharing(t)
	e.users.Add(cara, "cara", "cara@example.com", "")
	e.users.Add(vic, "vic", "vic@example.com", "")
	n := diagramtest.NewNotices()
	e.Notices = n
	res := project(t, e.Diagrams, alice, "Notes")
	for u, role := range map[string]struct {
		id   int
		role diagram.Role
	}{"bob": {bob, diagram.RoleEditor}, "cara": {cara, diagram.RoleCommenter}, "vic": {vic, diagram.RoleViewer}} {
		must1[diagram.Member](t)(e.AddMember(dctx, alice, res.Project.ID, u, role.role))
	}
	return commentEnv{e, n, res.Project.ID, res.File.ID}
}

const (
	cara = 5
	vic  = 6
)

func (e commentEnv) add(t *testing.T, actor int, in NewComment) CommentView {
	t.Helper()
	return must1[CommentView](t)(e.AddComment(dctx, actor, e.fid, in))
}

func TestDiagramCommentRoles(t *testing.T) {
	e := newComments(t)
	root := e.add(t, cara, NewComment{Body: "Why a single instance?", Anchor: point})
	if root.ParentID != nil || root.Author.ID != cara || root.Author.Name != "cara" || string(root.Anchor) != `{"type":"point","x":10,"y":20}` {
		t.Errorf("root = %+v", root)
	}
	// viewers read but never comment; the commenter role is enough to write
	if _, err := e.AddComment(dctx, vic, e.fid, NewComment{Body: "hi", Anchor: point}); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("viewer comment: %v", err)
	}
	for _, u := range []int{cara, bob, alice} {
		e.add(t, u, NewComment{Body: "ok", Anchor: point})
	}
	if _, err := e.FileComments(dctx, vic, e.fid); err != nil {
		t.Errorf("viewer read: %v", err)
	}
	for _, f := range []func() error{
		func() error { _, err := e.FileComments(dctx, eve, e.fid); return err },
		func() error {
			_, err := e.AddComment(dctx, eve, e.fid, NewComment{Body: "hi", Anchor: point})
			return err
		},
		func() error {
			_, err := e.UpdateComment(dctx, eve, root.ID, CommentPatch{Resolved: ptr(true)})
			return err
		},
		func() error { _, err := e.DeleteComment(dctx, eve, root.ID); return err },
		func() error { return e.MarkCommentsRead(dctx, eve, e.fid) },
	} {
		if err := f(); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("stranger: %v", err)
		}
	}
	// a commenter cannot edit the diagram
	if _, err := e.SaveFile(dctx, cara, e.fid, ptr("x"), nil, ifMatch(1)); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("commenter save: %v", err)
	}
	if _, err := e.PutPrivate(dctx, cara, e.fid, json.RawMessage(`{"nodes":[],"edges":[]}`)); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("commenter private: %v", err)
	}
	if _, err := e.GetFile(dctx, cara, e.fid); err != nil {
		t.Errorf("commenter read: %v", err)
	}
	// a viewer cannot resolve, the editor can
	if _, err := e.UpdateComment(dctx, vic, root.ID, CommentPatch{Resolved: ptr(true)}); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("viewer resolve: %v", err)
	}
}

func TestDiagramCommentRules(t *testing.T) {
	e := newComments(t)
	for name, in := range map[string]NewComment{
		"no anchor":     {Body: "no anchor"},
		"null anchor":   {Body: "x", Anchor: json.RawMessage("null")},
		"empty":         {Body: "", Anchor: point},
		"huge":          {Body: strings.Repeat("x", 4001), Anchor: point},
		"bad anchor":    {Body: "x", Anchor: json.RawMessage(`{"type":"point","x":"a","y":1}`)},
		"unknown type":  {Body: "x", Anchor: json.RawMessage(`{"type":"line"}`)},
		"missing reply": {Body: "x", ParentID: "cmt_nope"},
	} {
		_, err := e.AddComment(dctx, bob, e.fid, in)
		want := "VALIDATION_ERROR"
		switch name {
		case "no anchor", "null anchor":
			want = "ANCHOR_REQUIRED"
		case "missing reply":
			if !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		var de *diagram.Error
		if !errors.As(err, &de) || de.Code != want || !isValidation(err) && name != "" && de.Kind == nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	root := e.add(t, bob, NewComment{Body: "root", Anchor: point})
	reply := e.add(t, bob, NewComment{Body: "reply", ParentID: root.ID, Anchor: point})
	if reply.ParentID == nil || *reply.ParentID != root.ID || len(reply.Anchor) != 0 {
		t.Errorf("a reply has a parent and no anchor: %+v", reply)
	}
	if _, err := e.AddComment(dctx, bob, e.fid, NewComment{Body: "to a reply", ParentID: reply.ID}); !hasCode(err, "REPLY_TO_REPLY") && isValidation(err) {
		t.Errorf("reply to a reply: %v", err)
	}
	// a parent of another file is not found
	other := newFile(t, e, bob, "Second")
	if _, err := e.AddComment(dctx, bob, other.ID, NewComment{Body: "x", ParentID: root.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("parent of another file: %v", err)
	}

	// resolve and reopen on the root only
	done := must1[CommentView](t)(e.UpdateComment(dctx, bob, root.ID, CommentPatch{Resolved: ptr(true)}))
	if done.ResolvedAt == nil || done.ResolvedBy == nil || done.ResolvedBy.ID != bob {
		t.Errorf("resolved = %+v", done)
	}
	again := must1[CommentView](t)(e.UpdateComment(dctx, cara, root.ID, CommentPatch{Resolved: ptr(false)}))
	if again.ResolvedAt != nil || again.ResolvedBy != nil {
		t.Errorf("reopened = %+v", again)
	}
	if _, err := e.UpdateComment(dctx, bob, reply.ID, CommentPatch{Resolved: ptr(true)}); !hasCode(err, "RESOLVE_ROOT_ONLY") && isValidation(err) {
		t.Errorf("resolve a reply: %v", err)
	}
	if _, err := e.UpdateComment(dctx, bob, root.ID, CommentPatch{}); !isValidation(err) {
		t.Errorf("nothing to change: %v", err)
	}
	// only the author edits the text
	if _, err := e.UpdateComment(dctx, cara, root.ID, CommentPatch{Body: ptr("hijack")}); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("edit by another: %v", err)
	}
	e.advance(time.Second)
	edited := must1[CommentView](t)(e.UpdateComment(dctx, bob, root.ID, CommentPatch{Body: ptr("edited")}))
	if edited.Body != "edited" || !edited.UpdatedAt.After(edited.CreatedAt) {
		t.Errorf("edited = %+v", edited)
	}
	if _, err := e.UpdateComment(dctx, bob, root.ID, CommentPatch{Body: ptr("")}); !isValidation(err) {
		t.Errorf("empty edit: %v", err)
	}

	// threads list roots with their replies
	l := must1[CommentList](t)(e.FileComments(dctx, vic, e.fid))
	if len(l.Threads) != 1 || l.Threads[0].Root.ID != root.ID || len(l.Threads[0].Replies) != 1 || l.Threads[0].Replies[0].ID != reply.ID {
		t.Errorf("threads = %+v", l.Threads)
	}

	// delete: the author or an owner; a root takes its replies
	if _, err := e.DeleteComment(dctx, cara, root.ID); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("delete by another: %v", err)
	}
	if _, err := e.DeleteComment(dctx, bob, "cmt_nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete missing: %v", err)
	}
	gone := must1[[]string](t)(e.DeleteComment(dctx, alice, root.ID)) // the owner, not the author
	if len(gone) != 2 {
		t.Errorf("deleted = %v", gone)
	}
	if l := must1[CommentList](t)(e.FileComments(dctx, vic, e.fid)); len(l.Threads) != 0 {
		t.Errorf("threads after delete = %+v", l.Threads)
	}
	mine := e.add(t, cara, NewComment{Body: "mine", Anchor: point})
	if _, err := e.DeleteComment(dctx, cara, mine.ID); err != nil {
		t.Errorf("author delete: %v", err)
	}
}

func newFile(t *testing.T, e commentEnv, actor int, name string) DiagramFile {
	t.Helper()
	f, _, err := e.CreateFile(dctx, actor, e.pid, NewDiagramFile{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func isValidation(err error) bool {
	var v *domain.ValidationError
	return errors.As(err, &v)
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestDiagramCommentMentionsAndNotifications(t *testing.T) {
	e := newComments(t)
	body := fmt.Sprintf("Please look %s %s %s %s", diagram.FormatMention("Bob", bob), diagram.FormatMention("Eve", eve),
		diagram.FormatMention("Me", cara), diagram.FormatMention("Bob again", bob))
	c := e.add(t, cara, NewComment{Body: body, Anchor: point})
	if len(c.Mentions) != 1 || c.Mentions[0] != bob { // eve is no member, cara is the author, bob once
		t.Errorf("mentions = %v", c.Mentions)
	}
	all := e.notices.All()
	if len(all) != 1 {
		t.Fatalf("notifications = %+v", all)
	}
	n := all[0]
	if n.UserID != bob || n.Type != social.NotifyDiagramMention || n.ActorID != cara || n.DiagramProjectID != e.pid ||
		n.DiagramFileID != e.fid || n.DiagramCommentID != c.ID || n.Text != "cara mentioned you in Notes" {
		t.Errorf("notification = %+v", n)
	}
	// the owner can be mentioned too
	e.add(t, bob, NewComment{Body: "cc " + diagram.FormatMention("Alice", alice), Anchor: point})
	if got := len(e.notices.All()); got != 2 {
		t.Errorf("owner mention: %d notifications", got)
	}

	// a reply notifies the thread author; a mention in it wins, nobody twice
	root := e.add(t, cara, NewComment{Body: "question", Anchor: point})
	before := len(e.notices.All())
	e.add(t, bob, NewComment{Body: "answer", ParentID: root.ID})
	reply := e.notices.All()[before]
	if len(e.notices.All()) != before+1 || reply.UserID != cara || reply.Type != social.NotifyDiagramReply ||
		reply.Text != "bob replied to your comment in Notes" || reply.DiagramCommentID == root.ID {
		t.Errorf("reply notification = %+v", reply)
	}
	before = len(e.notices.All())
	e.add(t, bob, NewComment{Body: "again " + diagram.FormatMention("Cara", cara), ParentID: root.ID})
	if got := e.notices.All()[before:]; len(got) != 1 || got[0].Type != social.NotifyDiagramMention {
		t.Errorf("mention and reply to the same person: %+v", got)
	}
	before = len(e.notices.All())
	e.add(t, cara, NewComment{Body: "self reply", ParentID: root.ID})
	if len(e.notices.All()) != before {
		t.Errorf("replying to yourself notified")
	}

	// the Node-shaped view over them
	l := must1[NotificationList](t)(e.Notifications(dctx, bob, false, 0))
	if len(l.Items) != 1 || l.Unread != 1 || l.Items[0].By == nil || l.Items[0].By.Name != "cara" {
		t.Errorf("notifications = %+v", l)
	}
	if l := must1[NotificationList](t)(e.Notifications(dctx, eve, false, 0)); len(l.Items) != 0 || l.Unread != 0 {
		t.Errorf("eve's notifications = %+v", l)
	}
	must1[NotificationList](t)(e.Notifications(dctx, cara, true, 500)) // the limit is clamped, not refused
	if err := e.ReadNotifications(dctx, bob, []string{strconv.FormatInt(l.Items[0].ID, 10), "junk", "-4"}, false); err != nil {
		t.Fatal(err)
	}
	if l := must1[NotificationList](t)(e.Notifications(dctx, bob, true, 0)); len(l.Items) != 0 || l.Unread != 0 {
		t.Errorf("after read = %+v", l)
	}
	// someone else's ids are not yours to mark
	cl := must1[NotificationList](t)(e.Notifications(dctx, cara, false, 0))
	ok(t, e.ReadNotifications(dctx, bob, []string{strconv.FormatInt(cl.Items[0].ID, 10)}, false))
	if after := must1[NotificationList](t)(e.Notifications(dctx, cara, false, 0)); after.Unread != cl.Unread {
		t.Error("marked another user's notification")
	}
	ok(t, e.ReadNotifications(dctx, cara, nil, true))
	if l := must1[NotificationList](t)(e.Notifications(dctx, cara, true, 0)); l.Unread != 0 || len(l.Items) != 0 {
		t.Errorf("read all = %+v", l)
	}
}

func TestDiagramCommentUnreadCounts(t *testing.T) {
	e := newComments(t)
	e.add(t, alice, NewComment{Body: "one", Anchor: point})
	e.add(t, alice, NewComment{Body: "two", Anchor: point})
	if l := must1[CommentList](t)(e.FileComments(dctx, bob, e.fid)); l.Unread != 2 || !l.ReadAt.IsZero() {
		t.Errorf("bob unread = %d readAt %v", l.Unread, l.ReadAt)
	}
	if l := must1[CommentList](t)(e.FileComments(dctx, alice, e.fid)); l.Unread != 0 { // your own are not unread
		t.Errorf("alice unread = %d", l.Unread)
	}
	if err := e.MarkCommentsRead(dctx, bob, e.fid); err != nil {
		t.Fatal(err)
	}
	if l := must1[CommentList](t)(e.FileComments(dctx, bob, e.fid)); l.Unread != 0 || l.ReadAt.IsZero() {
		t.Errorf("after read = %d", l.Unread)
	}
	e.advance(5 * time.Millisecond) // comments and read marks are timestamped in ms
	e.add(t, alice, NewComment{Body: "three", Anchor: point})
	if l := must1[CommentList](t)(e.FileComments(dctx, bob, e.fid)); l.Unread != 1 {
		t.Errorf("after a new one = %d", l.Unread)
	}
}

func TestDiagramCommenterRoleOnMembersOverridesAndLinks(t *testing.T) {
	e := newComments(t)
	if _, err := e.AddComment(dctx, vic, e.fid, NewComment{Body: "no", Anchor: point}); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Fatalf("viewer: %v", err)
	}
	ok(t, e.SetFileMember(dctx, alice, e.fid, vic, diagram.RoleCommenter))
	e.add(t, vic, NewComment{Body: "now I can", Anchor: point})
	if _, err := e.SaveFile(dctx, vic, e.fid, ptr("x"), nil, ifMatch(1)); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("commenter override edits: %v", err)
	}
	ok(t, e.RemoveFileMember(dctx, alice, e.fid, vic))
	if _, err := e.AddComment(dctx, vic, e.fid, NewComment{Body: "again?", Anchor: point}); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("after the override is gone: %v", err)
	}
	// a commenter member can be demoted below an editor override and back
	ok(t, e.SetMemberRole(dctx, alice, e.pid, vic, diagram.RoleCommenter))
	e.add(t, vic, NewComment{Body: "member commenter", Anchor: point})

	// a commenter link reads like a viewer's and never writes (it has no account to comment with)
	link, token, err := e.CreateShare(dctx, alice, e.pid, NewShare{Role: diagram.RoleCommenter})
	ok(t, err)
	if link.Role != diagram.RoleCommenter {
		t.Errorf("link = %+v", link)
	}
	if _, err := e.ShareFile(dctx, token, e.fid); err != nil {
		t.Errorf("read through the link: %v", err)
	}
	if _, err := e.SaveShare(dctx, token, e.fid, ptr("x"), nil, ifMatch(1)); !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
		t.Errorf("write through a commenter link: %v", err)
	}
}

// private stickies

// privDoc is a page with a service, the given nodes and an edge from the
// service to s1 (so s1 must be among the nodes).
func privDoc(nodes ...string) json.RawMessage {
	return privDocEdges(`{"id":"e9","source":"a","target":"s1"}`, nodes...)
}

func privDocEdges(edges string, nodes ...string) json.RawMessage {
	return json.RawMessage(`{"version":2,"activePageId":"pg","pages":[{"id":"pg","name":"P","nodes":[{"id":"a","type":"service"},` +
		strings.Join(nodes, ",") + `],"edges":[` + edges + `]}]}`)
}

func priv(id string, author int, label string) string {
	return fmt.Sprintf(`{"id":%q,"type":"sticky","position":{"x":5,"y":5},"data":{"label":%q,"private":true,"authorId":"%d"}}`, id, label, author)
}

// everywhere is everything a reader of the shared file can get: the file and
// every snapshot.
func everywhere(t *testing.T, e commentEnv, reader int, fid string) string {
	t.Helper()
	var parts []string
	f := must1[DiagramFile](t)(e.GetFile(dctx, reader, fid))
	parts = append(parts, string(f.Data))
	for _, v := range must1[[]diagram.Version](t)(e.Versions(dctx, reader, fid)) {
		full := must1[diagram.Version](t)(e.Version(dctx, reader, fid, v.Version))
		parts = append(parts, string(full.Data))
	}
	return strings.Join(parts, "\n")
}

func mine(t *testing.T, e commentEnv, user int, fid string) string {
	t.Helper()
	return string(must1[diagram.Private](t)(e.GetPrivate(dctx, user, fid)).Data)
}

func TestDiagramSaveScrubsPrivateStickies(t *testing.T) {
	e := newComments(t)
	docs := privDoc(priv("s1", alice, secret), priv("s2", bob, "bob "+secret), priv("s3", 999, "unknown "+secret),
		`{"id":"open","type":"sticky","position":{"x":1,"y":1},"data":{"label":"visible note"}}`)
	f := must1[DiagramFile](t)(e.SaveFile(dctx, alice, e.fid, nil, docs, ifMatch(1)))
	if strings.Contains(string(f.Data), secret) || !strings.Contains(string(f.Data), "visible note") {
		t.Fatalf("the saved document: %s", f.Data)
	}
	for _, reader := range []int{alice, bob, cara, vic} {
		all := everywhere(t, e, reader, e.fid)
		if strings.Contains(all, secret) || !strings.Contains(all, "visible note") {
			t.Errorf("reader %d sees the secret (or lost the note): %s", reader, all)
		}
	}
	// the edge to a private sticky went with it
	if strings.Contains(string(f.Data), `"e9"`) { // quoted: the random file id may contain e9
		t.Errorf("edge to a private sticky stayed in the shared document: %s", f.Data)
	}
	a, b := mine(t, e, alice, e.fid), mine(t, e, bob, e.fid)
	// another author's sticky stays with the writer: a save never reaches into bob's private store
	if !strings.Contains(a, `"s1"`) || !strings.Contains(a, `"e9"`) || !strings.Contains(a, `"s3"`) || !strings.Contains(a, `"s2"`) {
		t.Errorf("alice's stickies (own + other authors', who are the writer): %s", a)
	}
	if strings.Contains(b, secret) {
		t.Errorf("alice's save wrote into bob's stickies: %s", b)
	}
	if got := mine(t, e, cara, e.fid); strings.Contains(got, secret) {
		t.Errorf("cara sees others' stickies: %s", got)
	}
	if !strings.Contains(a, `"authorId":"1"`) {
		t.Errorf("an unknown author is replaced by the writer: %s", a)
	}

	// a later save merges into what a user already has
	must1[DiagramFile](t)(e.SaveFile(dctx, alice, e.fid, nil, privDocEdges("", priv("s4", alice, "second")), ifMatch(2)))
	if a := mine(t, e, alice, e.fid); !strings.Contains(a, `"s1"`) || !strings.Contains(a, `"s4"`) {
		t.Errorf("merge: %s", a)
	}
}

func TestDiagramSaveThroughShareLinkScrubsAndAttributes(t *testing.T) {
	e := newComments(t)
	link, token, err := e.CreateShare(dctx, alice, e.pid, NewShare{Role: diagram.RoleEditor})
	if err != nil {
		t.Fatal(err)
	}
	doc := privDoc(priv("s1", bob, "bob "+secret), priv("s2", 999, "stranger "+secret))
	f := must1[DiagramFile](t)(e.SaveShare(dctx, token, e.fid, nil, doc, ifMatch(1)))
	if strings.Contains(string(f.Data), secret) {
		t.Fatalf("saved through a link: %s", f.Data)
	}
	if all := everywhere(t, e, cara, e.fid); strings.Contains(all, secret) {
		t.Errorf("readers see the secret: %s", all)
	}
	if b := mine(t, e, bob, e.fid); strings.Contains(b, secret) {
		t.Errorf("a link wrote into bob's stickies: %s", b)
	}
	if a := mine(t, e, alice, e.fid); !strings.Contains(a, `"s1"`) || !strings.Contains(a, `"s2"`) || !strings.Contains(a, `"authorId":"1"`) {
		t.Errorf("stickies saved through a link go to the project owner: %s", a)
	}
	// attribution: no user, the link
	if f.UpdatedBy != 0 || f.UpdatedByShare != link.ID {
		t.Errorf("updated by user %d share %q, want the link %q", f.UpdatedBy, f.UpdatedByShare, link.ID)
	}
	vs := must1[[]diagram.Version](t)(e.Versions(dctx, alice, e.fid))
	if vs[0].Version != 2 || vs[0].CreatedBy != 0 || vs[0].CreatedByShare != link.ID || vs[1].CreatedByShare != "" {
		t.Errorf("versions = %+v", vs)
	}
	// a person saving next takes the attribution back
	g := must1[DiagramFile](t)(e.SaveFile(dctx, bob, e.fid, nil, docOf("n"), ifMatch(2)))
	if g.UpdatedBy != bob || g.UpdatedByShare != "" {
		t.Errorf("after a person saves: user %d share %q", g.UpdatedBy, g.UpdatedByShare)
	}
	// a rename through the link is attributed to it as well
	h := must1[DiagramFile](t)(e.SaveShare(dctx, token, e.fid, ptr("Renamed"), nil, ifMatch(3)))
	if h.UpdatedByShare != link.ID || h.Name != "Renamed" {
		t.Errorf("rename: %+v", h.File)
	}
}

func TestDiagramRestoreScrubsPrivateStickies(t *testing.T) {
	e := newComments(t)
	// a snapshot stored before private stickies moved out (written under the use cases' feet)
	old := must1[diagram.Doc](t)(e.mem.File(dctx, e.fid))
	legacy, err := diagram.Prepare(privDoc(priv("s1", alice, secret)), old.ID, old.Name, old.CreatedAt, old.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.Version, next.Data = 2, legacy.Data
	if ok, err := e.mem.SaveFile(dctx, next, 1, diagram.Version{Version: 2, Name: old.Name, CreatedBy: alice, Data: legacy.Data}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	must1[DiagramFile](t)(e.SaveFile(dctx, alice, e.fid, nil, docOf("clean"), ifMatch(2))) // v3, clean

	restored := must1[DiagramFile](t)(e.RestoreVersion(dctx, alice, e.fid, 2))
	if restored.Version != 4 || strings.Contains(string(restored.Data), secret) {
		t.Fatalf("restored = v%d %s", restored.Version, restored.Data)
	}
	head := must1[diagram.Version](t)(e.Version(dctx, bob, e.fid, 4))
	if strings.Contains(string(head.Data), secret) {
		t.Errorf("the restored snapshot has the secret: %s", head.Data)
	}
	if a := mine(t, e, alice, e.fid); !strings.Contains(a, secret) {
		t.Errorf("the sticky went to its author: %s", a)
	}
}

func TestDiagramCreateScrubsPrivateStickies(t *testing.T) {
	e := newComments(t)
	f, _, err := e.CreateFile(dctx, bob, e.pid, NewDiagramFile{Name: "Imported", Data: privDoc(priv("s1", bob, secret))})
	ok(t, err)
	if strings.Contains(everywhere(t, e, alice, f.ID), secret) {
		t.Errorf("a created file holds the secret")
	}
	if b := mine(t, e, bob, f.ID); !strings.Contains(b, secret) {
		t.Errorf("bob's stickies: %s", b)
	}
	made := must1[DiagramProject](t)(e.CreateProject(dctx, cara, NewDiagramProject{Name: "Fresh", Data: privDoc(priv("s1", cara, secret))}))
	if strings.Contains(string(made.File.Data), secret) {
		t.Errorf("a created project holds the secret: %s", made.File.Data)
	}
	if c := mine(t, e, cara, made.File.ID); !strings.Contains(c, secret) {
		t.Errorf("cara's stickies: %s", c)
	}
	// duplicates copy the clean document
	dup := must1[DiagramFile](t)(e.DuplicateFile(dctx, bob, f.ID, diagram.Opt[*string]{}))
	if strings.Contains(string(dup.Data), secret) {
		t.Errorf("a duplicate holds the secret")
	}
}

func TestDiagramInvalidSaveFilesNoPrivateStickies(t *testing.T) {
	e := newComments(t)
	bad := json.RawMessage(`{"version":2,"activePageId":"nope","pages":[{"id":"pg","name":"P","nodes":[` + priv("s1", alice, secret) + `],"edges":[]}]}`)
	if _, err := e.SaveFile(dctx, alice, e.fid, nil, bad, ifMatch(1)); err == nil {
		t.Fatal("invalid document accepted")
	}
	if a := mine(t, e, alice, e.fid); strings.Contains(a, secret) {
		t.Errorf("a rejected save still filed stickies: %s", a)
	}
}

func TestDiagramProjectListHasThumbnailOfFirstFile(t *testing.T) {
	e := newComments(t)
	if p := must1[DiagramProjectPage](t)(e.ListProjects(dctx, alice, diagram.ProjectQuery{Page: 1, PageSize: 10})); p.Items[0].ThumbnailHash != nil || p.Items[0].ThumbFileID == "" {
		t.Errorf("no preview yet: %+v", p.Items[0])
	}
	second, _, err := e.CreateFile(dctx, alice, e.pid, NewDiagramFile{Name: "A second", Position: ptr(-1.0)})
	ok(t, err)
	hash := must1[string](t)(e.SetThumbnail(dctx, alice, second.ID, pngBytes))
	p := must1[DiagramProjectPage](t)(e.ListProjects(dctx, alice, diagram.ProjectQuery{Page: 1, PageSize: 10}))
	if it := p.Items[0]; it.ThumbFileID != second.ID || it.ThumbnailHash == nil || *it.ThumbnailHash != hash {
		t.Errorf("preview is the first file by position: %+v", it)
	}
	sum := must1[diagram.ProjectSummary](t)(e.GetProject(dctx, alice, e.pid))
	if sum.ThumbFileID != second.ID || sum.ThumbnailHash == nil {
		t.Errorf("get: %+v", sum)
	}
}
