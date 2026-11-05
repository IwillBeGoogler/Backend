package application

// Realtime events raised by the diagram use cases, and the access check of
// a realtime channel.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

type recorder struct {
	mu     sync.Mutex
	events []DiagramEvent
}

func (r *recorder) Emit(_ context.Context, e DiagramEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// take returns and forgets the events so far.
func (r *recorder) take() []DiagramEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

func kinds(es []DiagramEvent) []string {
	var out []string
	for _, e := range es {
		if e.Kind == EventTree {
			out = append(out, "tree:"+e.Reason)
		} else {
			out = append(out, e.Kind)
		}
	}
	return out
}

func TestDiagramEvents(t *testing.T) {
	e := newComments(t)
	rec := &recorder{}
	e.Events = rec
	d, pid, fid := e.Diagrams, e.pid, e.fid
	ctx := WithClientID(dctx, "client-1")

	t.Run("tree events carry the reason, ids, revision, actor and the connection to skip", func(t *testing.T) {
		folder, _, err := d.CreateFolder(ctx, alice, pid, NewFolder{Name: "Docs"})
		if err != nil {
			t.Fatal(err)
		}
		got := rec.take()
		if len(got) != 1 || got[0].Kind != EventTree || got[0].Reason != "folder.created" || got[0].FolderID != folder.ID ||
			got[0].ProjectID != pid || got[0].ClientID != "client-1" || got[0].By.UserID != alice || got[0].By.Name != "Alice A" || got[0].Rev < 2 {
			t.Errorf("events = %+v", got)
		}
		nf, _, err := d.CreateFile(dctx, bob, pid, NewDiagramFile{Name: "x", FolderID: &folder.ID})
		if err != nil {
			t.Fatal(err)
		}
		if got := rec.take(); len(got) != 1 || got[0].Reason != "file.created" || got[0].FileID != nf.ID || got[0].ClientID != "" || got[0].By.UserID != bob {
			t.Errorf("events = %+v", got)
		}
		if _, err := d.UpdateFolder(dctx, alice, folder.ID, FolderPatch{Name: ptr("Docs2")}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := d.UpdateFileMeta(dctx, alice, nf.ID, FilePatch{Name: ptr("y")}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := d.UpdateFileMeta(dctx, alice, nf.ID, FilePatch{Name: ptr("y")}); err != nil { // no change: no event
			t.Fatal(err)
		}
		if _, err := d.DuplicateFile(dctx, alice, nf.ID, diagram.Opt[*string]{}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.DuplicateFolder(dctx, alice, folder.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.RenameProject(dctx, alice, pid, "Renamed"); err != nil {
			t.Fatal(err)
		}
		if got := kinds(rec.take()); !slices.Equal(got, []string{"tree:folder.updated", "tree:file.updated", "tree:file.created", "tree:folder.created", "tree:project.renamed"}) {
			t.Errorf("events = %v", got)
		}
		// deleting a folder closes the files inside it, then announces the tree
		if _, _, err := d.DeleteFolder(dctx, alice, folder.ID); err != nil {
			t.Fatal(err)
		}
		got = rec.take()
		var closed []string
		for _, ev := range got {
			if ev.Kind == EventFileDeleted {
				closed = append(closed, ev.FileID)
			}
		}
		if len(closed) != 2 || !slices.Contains(closed, nf.ID) || got[len(got)-1].Reason != "folder.deleted" {
			t.Errorf("events = %+v", got)
		}
	})

	t.Run("a save announces file.updated; a rename also the tree", func(t *testing.T) {
		f, err := d.SaveFile(ctx, bob, fid, nil, docOf("a"), ifMatch(1))
		if err != nil {
			t.Fatal(err)
		}
		got := rec.take()
		if len(got) != 1 || got[0].Kind != EventFile || got[0].FileID != fid || got[0].Version != f.Version || got[0].ClientID != "client-1" ||
			got[0].By.UserID != bob || got[0].By.Name != "bob" || got[0].FileKind == "" || got[0].UpdatedAt == 0 || got[0].Restored {
			t.Errorf("events = %+v", got)
		}
		if _, err := d.SaveFile(dctx, bob, fid, ptr("Renamed"), nil, ifMatch(f.Version)); err != nil {
			t.Fatal(err)
		}
		if got := kinds(rec.take()); !slices.Equal(got, []string{EventFile, "tree:file.renamed"}) {
			t.Errorf("events = %v", got)
		}
		// a failed save says nothing
		if _, err := d.SaveFile(dctx, bob, fid, nil, docOf("b"), ifMatch(1)); err == nil {
			t.Fatal("stale save accepted")
		}
		if _, err := d.SaveFile(dctx, e.vicID(), fid, nil, docOf("b"), ifMatch(3)); err == nil {
			t.Fatal("viewer save accepted")
		}
		if got := rec.take(); len(got) != 0 {
			t.Errorf("events after failures = %+v", got)
		}
	})

	t.Run("a save through a share link is attributed to the link", func(t *testing.T) {
		_, token, err := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleEditor, FileID: &fid})
		if err != nil {
			t.Fatal(err)
		}
		cur, _ := d.GetFile(dctx, alice, fid)
		if _, err := d.SaveShare(dctx, token, "", nil, docOf("c"), ifMatch(cur.Version)); err != nil {
			t.Fatal(err)
		}
		got := rec.take()
		if len(got) != 1 || got[0].Kind != EventFile || !got[0].By.Share || got[0].By.UserID != 0 {
			t.Errorf("events = %+v", got)
		}
	})

	t.Run("a restore resets live editing and says so", func(t *testing.T) {
		if _, err := d.RestoreVersion(dctx, alice, fid, 1); err != nil {
			t.Fatal(err)
		}
		got := rec.take()
		if !slices.Equal(kinds(got), []string{EventRestored, EventFile}) || !got[1].Restored {
			t.Errorf("events = %+v", got)
		}
	})

	t.Run("a restore forgets the stored Yjs state", func(t *testing.T) {
		mem := d.Y
		if _, err := mem.YAppend(dctx, fid, []byte{0, 0}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.RestoreVersion(dctx, alice, fid, 1); err != nil {
			t.Fatal(err)
		}
		if st, _ := mem.YLoad(dctx, fid); len(st.Updates) != 0 {
			t.Errorf("state = %+v", st)
		}
		// an ordinary save keeps it: with live editing the client saves the document through REST
		if _, err := mem.YAppend(dctx, fid, []byte{0, 0}); err != nil {
			t.Fatal(err)
		}
		cur, _ := d.GetFile(dctx, alice, fid)
		if _, err := d.SaveFile(dctx, alice, fid, nil, docOf("d"), ifMatch(cur.Version)); err != nil {
			t.Fatal(err)
		}
		if st, _ := mem.YLoad(dctx, fid); len(st.Updates) != 1 {
			t.Errorf("a save dropped the Yjs state: %+v", st)
		}
		rec.take()
	})

	t.Run("comments", func(t *testing.T) {
		c := e.add(t, bob, NewComment{Body: "hi", Anchor: point})
		got := rec.take()
		if len(got) != 1 || got[0].Kind != EventCommentCreated || got[0].FileID != fid || got[0].ProjectID != pid || got[0].Comment == nil || got[0].Comment.ID != c.ID || got[0].Comment.Author.Name != "bob" {
			t.Errorf("events = %+v", got)
		}
		reply := e.add(t, alice, NewComment{Body: "re", ParentID: c.ID})
		rec.take()
		resolved := true
		if _, err := d.UpdateComment(dctx, alice, c.ID, CommentPatch{Resolved: &resolved}); err != nil {
			t.Fatal(err)
		}
		if got := rec.take(); len(got) != 1 || got[0].Kind != EventCommentUpdated || got[0].Comment.ResolvedAt == nil || got[0].FileID != fid {
			t.Errorf("events = %+v", got)
		}
		if _, err := d.DeleteComment(dctx, bob, c.ID); err != nil {
			t.Fatal(err)
		}
		got = rec.take()
		var gone []string
		for _, ev := range got {
			if ev.Kind == EventCommentDeleted && ev.FileID == fid {
				gone = append(gone, ev.CommentID)
			}
		}
		if len(gone) != 2 || !slices.Contains(gone, c.ID) || !slices.Contains(gone, reply.ID) {
			t.Errorf("events = %+v", got)
		}
		// a refused comment says nothing
		if _, err := d.AddComment(dctx, e.vicID(), fid, NewComment{Body: "no", Anchor: point}); err == nil {
			t.Fatal("viewer comment accepted")
		}
		if got := rec.take(); len(got) != 0 {
			t.Errorf("events = %+v", got)
		}
	})

	t.Run("library", func(t *testing.T) {
		if _, err := d.SaveLibrary(dctx, alice, pid, 0, libDoc("Card")); err != nil {
			t.Fatal(err)
		}
		if got := rec.take(); len(got) != 1 || got[0].Kind != EventLibrary || got[0].Rev != 1 || got[0].By.UserID != alice {
			t.Errorf("events = %+v", got)
		}
	})

	t.Run("deleting a file and a project", func(t *testing.T) {
		nf, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "gone"})
		rec.take()
		if err := d.DeleteFile(dctx, alice, nf.ID); err != nil {
			t.Fatal(err)
		}
		got := rec.take()
		if !slices.Equal(kinds(got), []string{EventFileDeleted, "tree:file.deleted"}) || got[0].FileID != nf.ID {
			t.Errorf("events = %+v", got)
		}
		if err := d.DeleteProject(dctx, bob, pid); err == nil {
			t.Fatal("an editor deleted the project")
		}
		if got := rec.take(); len(got) != 0 {
			t.Errorf("events = %+v", got)
		}
		if err := d.DeleteProject(dctx, alice, pid); err != nil {
			t.Fatal(err)
		}
		got = rec.take()
		if len(got) != 1 || got[0].Kind != EventProjectDeleted || got[0].ProjectID != pid || !slices.Contains(got[0].FileIDs, fid) {
			t.Errorf("events = %+v", got)
		}
	})
}

func (e commentEnv) vicID() int { return vic }

func TestWithClientID(t *testing.T) {
	if got := clientID(WithClientID(dctx, "abc")); got != "abc" {
		t.Errorf("id = %q", got)
	}
	long := string(make([]byte, 65))
	if got := clientID(WithClientID(dctx, long)); got != "" {
		t.Errorf("an id over 64 characters was kept")
	}
	if got := clientID(WithClientID(dctx, "")); got != "" {
		t.Errorf("empty id = %q", got)
	}
}

func TestOpenChannel(t *testing.T) {
	e := newComments(t)
	d, pid, fid := e.Diagrams, e.pid, e.fid
	second, _, err := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	_, fileTok, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleEditor, FileID: &fid})
	_, projTok, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer})
	exp := 60
	_, expTok, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer, ExpiresInSeconds: &exp})

	open := func(actor int, token, scope, id string) (ChannelAccess, error) {
		return d.OpenChannel(dctx, actor, token, scope, id)
	}

	// members
	for _, c := range []struct {
		actor int
		role  diagram.Role
		name  string
	}{{alice, diagram.RoleOwner, "Alice A"}, {bob, diagram.RoleEditor, "bob"}, {cara, diagram.RoleCommenter, "cara"}, {vic, diagram.RoleViewer, "vic"}} {
		a, err := open(c.actor, "", "file", fid)
		if err != nil || a.Role != c.role || a.UserID != c.actor || a.Name != c.name || a.ProjectID != pid || a.FileID != fid || a.Version != 1 {
			t.Errorf("file as %d = %+v, %v", c.actor, a, err)
		}
		p, err := open(c.actor, "", "project", pid)
		if err != nil || p.Role != c.role || p.FileID != "" || p.TreeRev < 1 || p.UserID != c.actor {
			t.Errorf("project as %d = %+v, %v", c.actor, p, err)
		}
	}
	// a per-file override applies on the file channel
	e.Diagrams.Repo.(interface {
		SetOverride(string, int, diagram.Role)
	}).SetOverride(fid, vic, diagram.RoleEditor)
	if a, err := open(vic, "", "file", fid); err != nil || a.Role != diagram.RoleEditor {
		t.Errorf("override: %+v, %v", a, err)
	}
	// strangers
	if _, err := open(eve, "", "file", fid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger file: %v", err)
	}
	if _, err := open(eve, "", "project", pid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger project: %v", err)
	}
	if _, err := open(alice, "", "file", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown file: %v", err)
	}
	if _, err := open(0, "", "file", fid); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("anonymous: %v", err)
	}
	if _, err := open(0, "", "project", pid); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("anonymous: %v", err)
	}
	// share links: guests, with the link's role
	a, err := open(0, fileTok, "file", fid)
	if err != nil || a.Role != diagram.RoleEditor || a.UserID != 0 || a.Name != "Guest" {
		t.Errorf("file link: %+v, %v", a, err)
	}
	if _, err := open(0, fileTok, "file", second.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("file link on another file: %v", err)
	}
	if _, err := open(0, fileTok, "project", pid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("file link on the tree: %v", err)
	}
	if a, err := open(0, projTok, "project", pid); err != nil || a.Role != diagram.RoleViewer || a.UserID != 0 {
		t.Errorf("project link: %+v, %v", a, err)
	}
	if a, err := open(0, projTok, "file", second.ID); err != nil || a.Role != diagram.RoleViewer {
		t.Errorf("project link on a file: %+v, %v", a, err)
	}
	// a signed-in stranger holding a link is a guest; a member is a member whatever the link says
	if a, err := open(eve, projTok, "file", fid); err != nil || a.UserID != 0 || a.Name != "Guest" {
		t.Errorf("stranger with a link: %+v, %v", a, err)
	}
	if a, err := open(alice, projTok, "file", fid); err != nil || a.UserID != alice || a.Role != diagram.RoleOwner {
		t.Errorf("member with a link: %+v, %v", a, err)
	}
	if _, err := open(0, "short", "file", fid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("malformed token: %v", err)
	}
	if err := d.CheckShare(dctx, projTok); err != nil {
		t.Errorf("CheckShare: %v", err)
	}
	if err := d.CheckShare(dctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("CheckShare unknown: %v", err)
	}
	// an expired link is gone
	if _, err := open(0, expTok, "project", pid); err != nil {
		t.Errorf("a live link: %v", err)
	}
	base := d.now
	d.now = func() time.Time { return base().Add(time.Hour) }
	if _, err := open(0, expTok, "project", pid); !errors.Is(err, diagram.ErrGone) {
		t.Errorf("an expired link: %v", err)
	}
}
