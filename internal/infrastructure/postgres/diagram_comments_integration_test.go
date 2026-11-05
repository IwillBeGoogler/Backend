package postgres_test

// Diagram comments, notifications (the blog's table), the commenter role,
// share-link attribution, private-sticky scrubbing and project previews
// against a real Postgres, with the cascades that keep rows from outliving
// their comment, file or project. Skipped with -short or without Docker.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
)

// socialNotices is the blog's notification system without the blog: what
// Blog.NotifyDiagram does (never the actor), over the same repository.
type socialNotices struct{ repo *postgres.SocialRepo }

func (s socialNotices) NotifyDiagram(ctx context.Context, n social.Notification, recipients ...int) {
	var to []int
	for _, id := range recipients {
		if id != n.ActorID {
			to = append(to, id)
		}
	}
	if len(to) > 0 {
		_, _ = s.repo.Notify(ctx, n, to)
	}
}

func (s socialNotices) DiagramNotifications(ctx context.Context, userID int, unreadOnly bool, limit int) ([]social.Notification, int, error) {
	return s.repo.DiagramNotifications(ctx, userID, unreadOnly, limit)
}

func (s socialNotices) MarkDiagramRead(ctx context.Context, userID int, ids []int64, all bool) error {
	return s.repo.MarkDiagramRead(ctx, userID, ids, all)
}

var cmtPoint = json.RawMessage(`{"type":"point","x":10,"y":20}`)

func cmtSticky(id string, author int, label string) string {
	return fmt.Sprintf(`{"id":%q,"type":"sticky","position":{"x":5,"y":5},"data":{"label":%q,"private":true,"authorId":"%d"}}`, id, label, author)
}

func TestDiagramCommentsRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	mk := func(name string) int {
		u, err := users.Create(ctx, name, name+"@example.com")
		must(t, err)
		return u.ID
	}
	ali, bob, cara, vic, eve := mk("dgc_ali"), mk("dgc_bob"), mk("dgc_cara"), mk("dgc_vic"), mk("dgc_eve")
	repo := postgres.NewDiagramRepo(db)
	socialRepo := postgres.NewSocialRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Coalesce = 0 // these tests count every save
	d.Users, d.Blobs = users, &blobStore{data: map[string][]byte{}}
	d.Notices = socialNotices{socialRepo}

	made, err := d.CreateProject(ctx, ali, application.NewDiagramProject{Name: "Notes", Data: dgDoc("a")})
	must(t, err)
	pid, fid := made.Project.ID, made.File.ID
	for login, role := range map[string]diagram.Role{"dgc_bob": diagram.RoleEditor, "dgc_cara": diagram.RoleCommenter, "dgc_vic": diagram.RoleViewer} {
		_, err := d.AddMember(ctx, ali, pid, login, role)
		must(t, err)
	}

	t.Run("the commenter role is stored on members, overrides and links", func(t *testing.T) {
		if f, err := d.GetFile(ctx, cara, fid); err != nil || f.Role != diagram.RoleCommenter {
			t.Errorf("member role: %v %s", err, f.Role)
		}
		must(t, d.SetFileMember(ctx, ali, fid, vic, diagram.RoleCommenter))
		if f, err := d.GetFile(ctx, vic, fid); err != nil || f.Role != diagram.RoleCommenter {
			t.Errorf("override: %v %s", err, f.Role)
		}
		must(t, d.RemoveFileMember(ctx, ali, fid, vic))
		l, _, err := d.CreateShare(ctx, ali, pid, application.NewShare{Role: diagram.RoleCommenter, FileID: &fid})
		must(t, err)
		if shares, err := d.Shares(ctx, ali, pid); err != nil || len(shares) != 1 || shares[0].ID != l.ID || shares[0].Role != diagram.RoleCommenter {
			t.Errorf("shares = %+v %v", shares, err)
		}
		if err := repo.SetMember(ctx, pid, eve, "owner", time.Now()); err == nil {
			t.Error("CHECK (role IN ('viewer','commenter','editor')) must refuse owner")
		}
		if _, err := d.AddComment(ctx, vic, fid, application.NewComment{Body: "no", Anchor: cmtPoint}); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("viewer comment: %v", err)
		}
	})

	t.Run("threads, mentions, notifications and unread", func(t *testing.T) {
		root, err := d.AddComment(ctx, cara, fid, application.NewComment{
			Body: "look " + diagram.FormatMention("Bob", bob) + " " + diagram.FormatMention("Eve", eve) + " " + diagram.FormatMention("Me", cara), Anchor: cmtPoint})
		must(t, err)
		if len(root.Mentions) != 1 || root.Mentions[0] != bob || root.Author.Name != "dgc_cara" {
			t.Errorf("root = %+v", root)
		}
		time.Sleep(3 * time.Millisecond) // comments and read marks are timestamped in ms
		reply, err := d.AddComment(ctx, bob, fid, application.NewComment{Body: "answer", ParentID: root.ID})
		must(t, err)
		if reply.ParentID == nil || *reply.ParentID != root.ID || len(reply.Anchor) != 0 {
			t.Errorf("reply = %+v", reply)
		}
		l, err := d.FileComments(ctx, bob, fid)
		must(t, err)
		if len(l.Threads) != 1 || len(l.Threads[0].Replies) != 1 || l.Unread != 1 || string(l.Threads[0].Root.Anchor) == "" ||
			len(l.Threads[0].Root.Mentions) != 1 {
			t.Errorf("threads = %+v unread %d", l.Threads, l.Unread)
		}
		must(t, d.MarkCommentsRead(ctx, bob, fid))
		if l, err := d.FileComments(ctx, bob, fid); err != nil || l.Unread != 0 || l.ReadAt.IsZero() {
			t.Errorf("after read: %+v %v", l, err)
		}

		// notifications are rows of the blog's table with diagram references
		got, err := d.Notifications(ctx, bob, false, 0)
		must(t, err)
		if len(got.Items) != 1 || got.Unread != 1 || got.Items[0].Type != social.NotifyDiagramMention || got.Items[0].DiagramCommentID != root.ID ||
			got.Items[0].DiagramFileID != fid || got.Items[0].DiagramProjectID != pid || got.Items[0].By == nil || got.Items[0].By.ID != cara ||
			got.Items[0].Text != "dgc_cara mentioned you in Notes" {
			t.Errorf("bob's notifications = %+v", got)
		}
		cr, err := d.Notifications(ctx, cara, false, 0)
		must(t, err)
		if len(cr.Items) != 1 || cr.Items[0].Type != social.NotifyDiagramReply || cr.Items[0].DiagramCommentID != reply.ID {
			t.Errorf("cara's notifications = %+v", cr.Items)
		}
		// ... so the blog's own listing (and its SSE) carries them
		page, err := socialRepo.Notifications(ctx, bob, domain.Paging{Page: 1, Limit: 20})
		must(t, err)
		if len(page.Items) != 1 || page.Items[0].Type != social.NotifyDiagramMention || page.Items[0].DiagramFileID != fid || page.Items[0].Text == "" || page.Unread != 1 {
			t.Errorf("blog listing = %+v", page)
		}
		raw, _ := json.Marshal(page.Items[0])
		if !strings.Contains(string(raw), `"diagram_file_id":"`+fid+`"`) || !strings.Contains(string(raw), `"type":"diagram_mention"`) {
			t.Errorf("wire shape = %s", raw)
		}
	})

	t.Run("the diagram view leaves the blog's notifications alone", func(t *testing.T) {
		_, err := socialRepo.Notify(ctx, social.Notification{Type: social.NotifyFollow, ActorID: cara}, []int{bob})
		must(t, err)
		got, err := d.Notifications(ctx, bob, false, 0)
		must(t, err)
		for _, n := range got.Items {
			if !n.Type.Diagram() {
				t.Errorf("a blog notification in the diagram view: %+v", n)
			}
		}
		must(t, d.ReadNotifications(ctx, bob, nil, true))
		if got, err := d.Notifications(ctx, bob, true, 0); err != nil || got.Unread != 0 || len(got.Items) != 0 {
			t.Errorf("after read all: %+v %v", got, err)
		}
		page, err := socialRepo.Notifications(ctx, bob, domain.Paging{Page: 1, Limit: 20})
		must(t, err)
		if page.Unread != 1 { // the follow is still unread
			t.Errorf("blog unread = %d, want 1", page.Unread)
		}
		// ids of other users or of the blog's own are no-ops
		for _, n := range page.Items {
			if n.Type == social.NotifyFollow {
				must(t, d.ReadNotifications(ctx, bob, []string{fmt.Sprint(n.ID)}, false))
			}
		}
		if page, err := socialRepo.Notifications(ctx, bob, domain.Paging{Page: 1, Limit: 20}); err != nil || page.Unread != 1 {
			t.Errorf("a diagram call marked a blog notification: %+v %v", page.Unread, err)
		}
	})

	t.Run("editing, resolving and deleting", func(t *testing.T) {
		root, err := d.AddComment(ctx, bob, fid, application.NewComment{Body: "r", Anchor: cmtPoint})
		must(t, err)
		rep, err := d.AddComment(ctx, cara, fid, application.NewComment{Body: "reply " + diagram.FormatMention("Bob", bob), ParentID: root.ID})
		must(t, err)
		done, err := d.UpdateComment(ctx, cara, root.ID, application.CommentPatch{Resolved: ptrTo(true)})
		must(t, err)
		if done.ResolvedAt == nil || done.ResolvedBy == nil || done.ResolvedBy.ID != cara {
			t.Errorf("resolved = %+v", done)
		}
		reopened, err := d.UpdateComment(ctx, bob, root.ID, application.CommentPatch{Resolved: ptrTo(false), Body: ptrTo("edited")})
		must(t, err)
		if reopened.ResolvedAt != nil || reopened.ResolvedBy != nil || reopened.Body != "edited" {
			t.Errorf("reopened = %+v", reopened)
		}
		if _, err := d.UpdateComment(ctx, cara, rep.ID, application.CommentPatch{Resolved: ptrTo(true)}); err == nil {
			t.Error("a reply cannot be resolved")
		}
		before := rowCount(t, db, "notifications WHERE dg_comment_id IN ('"+root.ID+"', '"+rep.ID+"')")
		if before == 0 {
			t.Fatal("expected notifications for the thread")
		}
		ids, err := d.DeleteComment(ctx, ali, root.ID) // an owner, not the author
		must(t, err)
		if len(ids) != 2 || ids[0] != root.ID {
			t.Errorf("deleted = %v", ids)
		}
		for _, q := range []string{
			"dg_comments WHERE id IN ('" + root.ID + "', '" + rep.ID + "')",
			"dg_comment_mentions WHERE comment_id IN ('" + root.ID + "', '" + rep.ID + "')",
			"notifications WHERE dg_comment_id IN ('" + root.ID + "', '" + rep.ID + "')",
		} {
			if n := rowCount(t, db, q); n != 0 {
				t.Errorf("%d rows of %s outlived the thread", n, q)
			}
		}
		if _, err := d.DeleteComment(ctx, ali, root.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("second delete: %v", err)
		}
	})

	t.Run("a comment outlives its author's account", func(t *testing.T) {
		gone := mk("dgc_gone")
		_, err := d.AddMember(ctx, ali, pid, "dgc_gone", diagram.RoleCommenter)
		must(t, err)
		c, err := d.AddComment(ctx, gone, fid, application.NewComment{Body: "I will leave", Anchor: cmtPoint})
		must(t, err)
		_, err = db.Exec(ctx, `DELETE FROM users WHERE id = $1`, gone)
		must(t, err)
		l, err := d.FileComments(ctx, ali, fid)
		must(t, err)
		found := false
		for _, th := range l.Threads {
			if th.Root.ID == c.ID {
				found = true
				if th.Root.Author.Name != "Someone" || th.Root.Author.ID != 0 {
					t.Errorf("author = %+v", th.Root.Author)
				}
			}
		}
		if !found {
			t.Error("the comment went with the account")
		}
	})

	t.Run("deleting the file deletes its comments, reads and notifications", func(t *testing.T) {
		f2, _, err := d.CreateFile(ctx, ali, pid, application.NewDiagramFile{Name: "temp", Data: dgDoc("a")})
		must(t, err)
		c, err := d.AddComment(ctx, cara, f2.ID, application.NewComment{Body: "hi " + diagram.FormatMention("Bob", bob), Anchor: cmtPoint})
		must(t, err)
		must(t, d.MarkCommentsRead(ctx, bob, f2.ID))
		must(t, d.DeleteFile(ctx, ali, f2.ID))
		for _, q := range []string{"dg_comments WHERE id = '" + c.ID + "'", "dg_comment_reads WHERE file_id = '" + f2.ID + "'",
			"notifications WHERE dg_file_id = '" + f2.ID + "'"} {
			if n := rowCount(t, db, q); n != 0 {
				t.Errorf("%d rows of %s outlived the file", n, q)
			}
		}
		// ... and the project takes the rest
		f3, _, err := d.CreateFile(ctx, ali, pid, application.NewDiagramFile{Name: "temp3", Data: dgDoc("a")})
		must(t, err)
		_, err = d.AddComment(ctx, cara, f3.ID, application.NewComment{Body: "bye " + diagram.FormatMention("Bob", bob), Anchor: cmtPoint})
		must(t, err)
		other, err := d.CreateProject(ctx, ali, application.NewDiagramProject{Name: "Temp project", Data: dgDoc("a")})
		must(t, err)
		_, err = d.AddMember(ctx, ali, other.Project.ID, "dgc_cara", diagram.RoleCommenter)
		must(t, err)
		_, err = d.AddMember(ctx, ali, other.Project.ID, "dgc_bob", diagram.RoleViewer)
		must(t, err)
		_, err = d.AddComment(ctx, cara, other.File.ID, application.NewComment{Body: "x " + diagram.FormatMention("Bob", bob), Anchor: cmtPoint})
		must(t, err)
		must(t, d.DeleteProject(ctx, ali, other.Project.ID))
		if n := rowCount(t, db, "notifications WHERE dg_project_id = '"+other.Project.ID+"'"); n != 0 {
			t.Errorf("%d notifications outlived the project", n)
		}
		if n := rowCount(t, db, "dg_comments WHERE project_id = '"+other.Project.ID+"'"); n != 0 {
			t.Errorf("%d comments outlived the project", n)
		}
	})
}

func ptrTo[T any](v T) *T { return &v }

func TestDiagramPhase4Repo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	ali, err := users.Create(ctx, "dgp_ali", "dgp_ali@example.com")
	must(t, err)
	bob, err := users.Create(ctx, "dgp_bob", "dgp_bob@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Coalesce = 0 // these tests count every save
	d.Users, d.Blobs = users, &blobStore{data: map[string][]byte{}}
	made, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Phase 4", Data: dgDoc("a")})
	must(t, err)
	pid, fid := made.Project.ID, made.File.ID
	_, err = d.AddMember(ctx, ali.ID, pid, "dgp_bob", diagram.RoleEditor)
	must(t, err)

	t.Run("a save through a share link is attributed to the link", func(t *testing.T) {
		link, token, err := d.CreateShare(ctx, ali.ID, pid, application.NewShare{Role: diagram.RoleEditor})
		must(t, err)
		saved, err := d.SaveShare(ctx, token, fid, nil, dgDoc("a", "b"), `"1"`)
		must(t, err)
		if saved.UpdatedBy != 0 || saved.UpdatedByShare != link.ID {
			t.Errorf("saved = %+v", saved.File)
		}
		got, err := d.GetFile(ctx, ali.ID, fid)
		must(t, err)
		if got.UpdatedBy != 0 || got.UpdatedByShare != link.ID {
			t.Errorf("stored = by %d share %q, want the link", got.UpdatedBy, got.UpdatedByShare)
		}
		vs, err := d.Versions(ctx, ali.ID, fid)
		must(t, err)
		if vs[0].CreatedBy != 0 || vs[0].CreatedByShare != link.ID || vs[1].CreatedByShare != "" || vs[1].CreatedBy != ali.ID {
			t.Errorf("versions = %+v", vs)
		}
		if n := rowCount(t, db, "dg_files WHERE id = '"+fid+"' AND updated_by IS NULL AND updated_by_share = '"+link.ID+"'"); n != 1 {
			t.Errorf("the row keeps a null user and the link id")
		}
		next, err := d.SaveFile(ctx, bob.ID, fid, nil, dgDoc("a", "b", "c"), `"2"`)
		must(t, err)
		if next.UpdatedBy != bob.ID || next.UpdatedByShare != "" {
			t.Errorf("a person's save keeps the old attribution: %+v", next.File)
		}
		again, err := d.GetFile(ctx, ali.ID, fid)
		must(t, err)
		if again.UpdatedBy != bob.ID || again.UpdatedByShare != "" {
			t.Errorf("stored = by %d share %q", again.UpdatedBy, again.UpdatedByShare)
		}
	})

	t.Run("private stickies never reach the stored document, its snapshots or a restore", func(t *testing.T) {
		const secret = "my-secret-text"
		doc := func(nodes ...string) json.RawMessage {
			edges := ""
			if len(nodes) > 0 {
				edges = `{"id":"e9","source":"a","target":"s1"}`
			}
			return json.RawMessage(`{"version":2,"activePageId":"pg","pages":[{"id":"pg","name":"P","nodes":[{"id":"a","type":"service"}` +
				joinComma(nodes) + `],"edges":[` + edges + `]}]}`)
		}
		cur, err := d.GetFile(ctx, ali.ID, fid)
		must(t, err)
		_, err = d.SaveFile(ctx, ali.ID, fid, nil, doc(cmtSticky("s1", ali.ID, secret), cmtSticky("s2", bob.ID, "bob "+secret), cmtSticky("s3", 987654, "ghost "+secret)),
			fmt.Sprintf(`"%d"`, cur.Version))
		must(t, err)
		var stored int
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM dg_files WHERE id = $1 AND data::text LIKE '%'||$2||'%'`, fid, secret).Scan(&stored))
		var snaps int
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM dg_file_versions WHERE file_id = $1 AND data::text LIKE '%'||$2||'%'`, fid, secret).Scan(&snaps))
		if stored != 0 || snaps != 0 {
			t.Errorf("the secret is in the database: %d file rows, %d snapshots", stored, snaps)
		}
		mine, err := d.GetPrivate(ctx, ali.ID, fid)
		must(t, err)
		if !strings.Contains(string(mine.Data), `"s1"`) || !strings.Contains(string(mine.Data), `"e9"`) || !strings.Contains(string(mine.Data), `"s2"`) || !strings.Contains(string(mine.Data), `"s3"`) {
			t.Errorf("alice's stickies (every author's: a save never reaches into another's store): %s", mine.Data)
		}
		theirs, err := d.GetPrivate(ctx, bob.ID, fid)
		must(t, err)
		if strings.Contains(string(theirs.Data), secret) {
			t.Errorf("alice's save wrote into bob's stickies: %s", theirs.Data)
		}
		// a snapshot from before the scrub, written under the use cases' feet, is cleaned when restored
		_, err = db.Exec(ctx, `INSERT INTO dg_file_versions (id, file_id, version, name, data, created_at) VALUES ('ver_legacy_p4', $1, 900, 'legacy', $2, now())`,
			fid, doc(cmtSticky("s1", ali.ID, "legacy "+secret)))
		must(t, err)
		restored, err := d.RestoreVersion(ctx, ali.ID, fid, 900)
		must(t, err)
		if strings.Contains(string(restored.Data), secret) {
			t.Errorf("restore: %s", restored.Data)
		}
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM dg_file_versions WHERE file_id = $1 AND version = $2 AND data::text LIKE '%'||$3||'%'`,
			fid, restored.Version, secret).Scan(&snaps))
		if snaps != 0 {
			t.Error("the restored snapshot holds the secret")
		}
		// a created file is scrubbed before its first snapshot
		f2, _, err := d.CreateFile(ctx, bob.ID, pid, application.NewDiagramFile{Name: "imp", Data: doc(cmtSticky("s1", bob.ID, "imp "+secret))})
		must(t, err)
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM dg_file_versions WHERE file_id = $1 AND data::text LIKE '%'||$2||'%'`, f2.ID, secret).Scan(&snaps))
		if snaps != 0 {
			t.Error("a created file's snapshot holds the secret")
		}
		if p, err := d.GetPrivate(ctx, bob.ID, f2.ID); err != nil || !strings.Contains(string(p.Data), "imp") {
			t.Errorf("created file stickies: %s %v", p.Data, err)
		}
	})

	t.Run("the project list previews the first file", func(t *testing.T) {
		list := func() diagram.ProjectSummary {
			p, err := d.ListProjects(ctx, ali.ID, diagram.ProjectQuery{Page: 1, PageSize: 50, Search: "Phase 4"})
			must(t, err)
			if len(p.Items) != 1 {
				t.Fatalf("items = %+v", p.Items)
			}
			return p.Items[0]
		}
		if it := list(); it.ThumbFileID == "" || it.ThumbnailHash != nil {
			t.Errorf("no preview yet: %+v", it)
		}
		first, _, err := d.CreateFile(ctx, ali.ID, pid, application.NewDiagramFile{Name: "AAA first", Position: ptrTo(-100.0)})
		must(t, err)
		hash, err := d.SetThumbnail(ctx, ali.ID, first.ID, itPNG)
		must(t, err)
		it := list()
		if it.ThumbFileID != first.ID || it.ThumbnailHash == nil || *it.ThumbnailHash != hash {
			t.Errorf("preview = %+v, want file %s hash %s", it, first.ID, hash)
		}
		sum, err := d.GetProject(ctx, ali.ID, pid)
		must(t, err)
		if sum.ThumbFileID != first.ID || sum.ThumbnailHash == nil || *sum.ThumbnailHash != hash {
			t.Errorf("get = %+v", sum)
		}
	})
}

func joinComma(nodes []string) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString("," + n)
	}
	return b.String()
}
