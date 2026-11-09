package application

// Sharing use cases on the in-memory repository: members, share links
// (token auth goes through the same role checks as members), assets and
// thumbnails, the library and private stickies.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
)

const dana = 4

// memBlobs is an in-memory Storage.
type memBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memBlobs) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = b
	return err
}

func (m *memBlobs) Get(_ context.Context, key string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return Object{}, domain.ErrNotFound
	}
	return Object{Body: io.NopCloser(bytes.NewReader(b)), Size: int64(len(b))}, nil
}

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 40)...)
	webpBytes = append([]byte("RIFF\x20\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{1}, 24)...)
	jpegBytes = append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{2}, 40)...)
)

type sharingEnv struct {
	*Diagrams
	mem   *diagramtest.Memory
	blobs *memBlobs
	users *diagramtest.Users
	clock *time.Time
}

func newSharing(t *testing.T) sharingEnv {
	t.Helper()
	d, mem := newDiagrams(t)
	now := time.Unix(1_700_000_000, 0)
	d.now = func() time.Time { return now }
	users := diagramtest.NewUsers()
	users.Add(alice, "alice", "alice@example.com", "Alice A")
	users.Add(bob, "bob", "bob@example.com", "")
	users.Add(eve, "eve", "eve@example.com", "Eve")
	users.Add(dana, "dana", "dana@example.com", "Dana")
	d.Users = users
	blobs := &memBlobs{data: map[string][]byte{}}
	d.Blobs = blobs
	return sharingEnv{d, mem, blobs, users, &now}
}

func (e sharingEnv) advance(by time.Duration) { *e.clock = e.clock.Add(by) }

func code(err error, kind error, c string) bool { return isCode(err, kind, c) }

func TestDiagramMembers(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	res := project(t, d, alice, "Team")
	pid := res.Project.ID

	items := must1[[]diagram.Member](t)(d.Members(dctx, alice, pid))
	if len(items) != 1 || items[0].UserID != alice || items[0].Role != diagram.RoleOwner || !items[0].IsOriginalOwner ||
		items[0].Email != "alice@example.com" || items[0].Name != "Alice A" {
		t.Errorf("owner entry = %+v", items)
	}

	// invite by email (any case) and by username; display name falls back to the username
	m := must1[diagram.Member](t)(d.AddMember(dctx, alice, pid, "  BOB@Example.com ", diagram.RoleViewer))
	if m.UserID != bob || m.Name != "bob" || m.Role != diagram.RoleViewer {
		t.Errorf("member = %+v", m)
	}
	must1[diagram.Member](t)(d.AddMember(dctx, alice, pid, "dana", diagram.RoleEditor))
	items = must1[[]diagram.Member](t)(d.Members(dctx, bob, pid))
	if len(items) != 3 || items[0].UserID != alice || items[1].IsOriginalOwner {
		t.Errorf("members = %+v", items)
	}
	// inviting again changes the role instead of duplicating
	must1[diagram.Member](t)(d.AddMember(dctx, alice, pid, "bob@example.com", diagram.RoleEditor))
	if items = must1[[]diagram.Member](t)(d.Members(dctx, alice, pid)); len(items) != 3 {
		t.Errorf("re-invite duplicated: %+v", items)
	}
	if f, _ := d.GetFile(dctx, bob, res.File.ID); f.Role != diagram.RoleEditor {
		t.Errorf("bob's role = %s, want editor", f.Role)
	}

	t.Run("rejected invitations", func(t *testing.T) {
		if _, err := d.AddMember(dctx, alice, pid, "ghost@example.com", diagram.RoleViewer); !code(err, domain.ErrNotFound, "USER_NOT_FOUND") {
			t.Errorf("unknown: %v", err)
		}
		e.users.Delete(eve)
		if _, err := d.AddMember(dctx, alice, pid, "eve", diagram.RoleViewer); !code(err, domain.ErrNotFound, "USER_NOT_FOUND") {
			t.Errorf("deleted account: %v", err)
		}
		if _, err := d.AddMember(dctx, alice, pid, "alice@example.com", diagram.RoleViewer); !hasCode(err, "OWNER_IMMUTABLE") {
			t.Errorf("owner: %v", err)
		}
		for _, role := range []diagram.Role{"owner", "admin", ""} {
			var v *domain.ValidationError
			if _, err := d.AddMember(dctx, alice, pid, "dana", role); !errors.As(err, &v) {
				t.Errorf("role %q: %v", role, err)
			}
		}
		var v *domain.ValidationError
		if _, err := d.AddMember(dctx, alice, pid, "  ", diagram.RoleViewer); !errors.As(err, &v) {
			t.Errorf("blank login: %v", err)
		}
		// only owners manage members; strangers do not even see the project
		if _, err := d.AddMember(dctx, bob, pid, "dana", diagram.RoleViewer); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("editor invite: %v", err)
		}
		if _, err := d.AddMember(dctx, eve, pid, "dana", diagram.RoleViewer); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("stranger invite: %v", err)
		}
		if _, err := d.Members(dctx, eve, pid); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("stranger list: %v", err)
		}
	})

	t.Run("change a role", func(t *testing.T) {
		if err := d.SetMemberRole(dctx, alice, pid, bob, diagram.RoleViewer); err != nil {
			t.Fatal(err)
		}
		if f, _ := d.GetFile(dctx, bob, res.File.ID); f.Role != diagram.RoleViewer {
			t.Errorf("role = %s", f.Role)
		}
		if err := d.SetMemberRole(dctx, alice, pid, alice, diagram.RoleViewer); !hasCode(err, "OWNER_IMMUTABLE") {
			t.Errorf("owner: %v", err)
		}
		if err := d.SetMemberRole(dctx, alice, pid, eve, diagram.RoleViewer); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("non-member: %v", err)
		}
		if err := d.SetMemberRole(dctx, bob, pid, bob, diagram.RoleEditor); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("self-promotion: %v", err)
		}
	})

	t.Run("remove and leave", func(t *testing.T) {
		if err := d.RemoveMember(dctx, bob, pid, dana); !code(err, domain.ErrForbidden, "FORBIDDEN") {
			t.Errorf("non-owner removing another: %v", err)
		}
		if err := d.RemoveMember(dctx, alice, pid, alice); !hasCode(err, "OWNER_IMMUTABLE") {
			t.Errorf("removing the owner: %v", err)
		}
		if err := d.RemoveMember(dctx, alice, pid, eve); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("non-member: %v", err)
		}
		if err := d.RemoveMember(dctx, bob, pid, bob); err != nil { // leave
			t.Fatal(err)
		}
		if _, err := d.GetProject(dctx, bob, pid); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("after leaving: %v", err)
		}
		if err := d.RemoveMember(dctx, alice, pid, dana); err != nil {
			t.Fatal(err)
		}
	})
}

// hasCode reports whether err carries a diagram error code (for errors
// whose kind is a plain validation error).
func hasCode(err error, c string) bool {
	var e *diagram.Error
	return errors.As(err, &e) && e.Code == c
}

func TestDiagramFileMembers(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	res := project(t, d, alice, "P")
	pid, fid := res.Project.ID, res.File.ID
	other, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "other"})
	must1[diagram.Member](t)(d.AddMember(dctx, alice, pid, "bob", diagram.RoleEditor))

	if err := d.SetFileMember(dctx, alice, fid, bob, diagram.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SaveFile(dctx, bob, fid, ptr("x"), nil, ifMatch(1)); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("override viewer save: %v", err)
	}
	if _, err := d.SaveFile(dctx, bob, other.ID, ptr("x"), nil, ifMatch(1)); err != nil {
		t.Errorf("other file stays editable: %v", err)
	}
	items := must1[[]diagram.Member](t)(d.FileMembers(dctx, alice, fid))
	if len(items) != 1 || items[0].UserID != bob || items[0].Role != diagram.RoleViewer || items[0].Email != "bob@example.com" || items[0].IsOriginalOwner {
		t.Errorf("overrides = %+v", items)
	}
	// owners only
	if _, err := d.FileMembers(dctx, bob, fid); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member list: %v", err)
	}
	if _, err := d.FileMembers(dctx, eve, fid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger list: %v", err)
	}
	if err := d.SetFileMember(dctx, alice, fid, alice, diagram.RoleViewer); !hasCode(err, "OWNER_IMMUTABLE") {
		t.Errorf("owner override: %v", err)
	}
	if err := d.SetFileMember(dctx, alice, fid, eve, diagram.RoleViewer); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("non-member override: %v", err)
	}
	if err := d.SetFileMember(dctx, alice, fid, bob, "owner"); err == nil {
		t.Error("owner is not an override role")
	}

	// removing the member removes its overrides in the project
	must1[diagram.Member](t)(d.AddMember(dctx, alice, pid, "dana", diagram.RoleEditor))
	if err := d.SetFileMember(dctx, alice, other.ID, dana, diagram.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveMember(dctx, alice, pid, dana); err != nil {
		t.Fatal(err)
	}
	if items := must1[[]diagram.Member](t)(d.FileMembers(dctx, alice, other.ID)); len(items) != 0 {
		t.Errorf("overrides after removal = %+v", items)
	}

	if err := d.RemoveFileMember(dctx, alice, fid, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SaveFile(dctx, bob, fid, ptr("x"), nil, ifMatch(1)); err != nil {
		t.Errorf("project role applies again: %v", err)
	}
	if err := d.RemoveFileMember(dctx, alice, fid, bob); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("no override left: %v", err)
	}
}

func TestDiagramShareLinks(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	res := project(t, d, alice, "Shared")
	pid, fid := res.Project.ID, res.File.ID
	second, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "second"})

	mk := func(role diagram.Role, file *string, exp *int) (diagram.ShareLink, string) {
		t.Helper()
		s, token, err := d.CreateShare(dctx, alice, pid, NewShare{Role: role, FileID: file, ExpiresInSeconds: exp})
		if err != nil {
			t.Fatal(err)
		}
		return s, token
	}

	t.Run("tokens are random and only their hash is stored", func(t *testing.T) {
		a, ta := mk(diagram.RoleViewer, &fid, nil)
		_, tb := mk(diagram.RoleViewer, &fid, nil)
		if ta == tb || len(ta) != 22 || !diagram.ValidShareToken(ta) {
			t.Errorf("tokens %q %q", ta, tb)
		}
		if a.TokenHash != diagram.HashToken(ta) || strings.Contains(a.TokenHash, ta) {
			t.Errorf("stored hash = %q", a.TokenHash)
		}
		list := must1[[]diagram.ShareLink](t)(d.Shares(dctx, alice, pid))
		for _, s := range list {
			if s.TokenHash == ta || s.TokenHash == tb {
				t.Error("the raw token must never be stored")
			}
		}
		if len(list) < 2 || list[0].CreatedBy != alice {
			t.Errorf("list = %+v", list)
		}
	})

	t.Run("viewer link reads, never writes", func(t *testing.T) {
		_, token := mk(diagram.RoleViewer, &fid, nil)
		v := must1[ShareView](t)(d.OpenShare(dctx, token))
		if v.File == nil || v.File.ID != fid || v.File.Role != diagram.RoleViewer || v.Project.Role != diagram.RoleViewer || v.Link.FileID == nil {
			t.Errorf("view = %+v", v)
		}
		if _, err := d.SaveShare(dctx, token, "", ptr("hack"), nil, ifMatch(1)); !code(err, domain.ErrForbidden, "FORBIDDEN") {
			t.Errorf("viewer save: %v", err)
		}
	})

	t.Run("editor link saves with If-Match", func(t *testing.T) {
		_, token := mk(diagram.RoleEditor, &fid, nil)
		if _, err := d.SaveShare(dctx, token, "", nil, docOf("n1"), ""); !code(err, diagram.ErrPrecondition, "IF_MATCH_REQUIRED") {
			t.Errorf("no If-Match: %v", err)
		}
		f := must1[DiagramFile](t)(d.SaveShare(dctx, token, "", nil, docOf("n1"), ifMatch(1)))
		if f.Version != 2 || f.Role != diagram.RoleEditor || nodeCount(t, f.Data) != 1 {
			t.Errorf("saved = %+v", f.File)
		}
		if _, err := d.SaveShare(dctx, token, "", nil, docOf("n2"), ifMatch(1)); !code(err, diagram.ErrPrecondition, "VERSION_CONFLICT") {
			t.Errorf("stale: %v", err)
		}
		if _, err := d.SaveShare(dctx, token, "", nil, nil, ifMatch(2)); err == nil {
			t.Error("empty save must fail")
		}
		// the owner sees it; the share holder is not an account
		cur := must1[DiagramFile](t)(d.GetFile(dctx, alice, fid))
		if cur.Version != 2 || cur.UpdatedBy != 0 {
			t.Errorf("owner view = v%d by %d", cur.Version, cur.UpdatedBy)
		}
	})

	t.Run("a file link opens only its own file; a project link every file", func(t *testing.T) {
		_, ft := mk(diagram.RoleViewer, &fid, nil)
		if _, err := d.ShareFile(dctx, ft, second.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("file link, other file: %v", err)
		}
		if f, err := d.ShareFile(dctx, ft, fid); err != nil || f.ID != fid {
			t.Errorf("file link, own file: %v", err)
		}
		_, pt := mk(diagram.RoleEditor, nil, nil)
		v := must1[ShareView](t)(d.OpenShare(dctx, pt))
		if v.File != nil || len(v.Files) != 2 || v.Project.FileCount != 2 || v.Link.FileID != nil {
			t.Errorf("project view = %+v", v)
		}
		if f, err := d.ShareFile(dctx, pt, second.ID); err != nil || f.ID != second.ID {
			t.Errorf("project link file: %v", err)
		}
		if f, err := d.SaveShare(dctx, pt, second.ID, ptr("renamed"), nil, ifMatch(1)); err != nil || f.Name != "renamed" {
			t.Errorf("project link save: %v", err)
		}
		// a project link needs a file id to save, and a file of another project is not found
		if _, err := d.SaveShare(dctx, pt, "", ptr("x"), nil, ifMatch(1)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("project link, no file: %v", err)
		}
		foreign := project(t, d, alice, "Foreign")
		if _, err := d.ShareFile(dctx, pt, foreign.File.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("foreign file: %v", err)
		}
		if _, err := d.SaveShare(dctx, pt, foreign.File.ID, ptr("x"), nil, ifMatch(1)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("foreign file save: %v", err)
		}
	})

	t.Run("unknown, malformed, expired and revoked tokens", func(t *testing.T) {
		if _, err := d.OpenShare(dctx, strings.Repeat("A", 22)); !code(err, domain.ErrNotFound, "NOT_FOUND") {
			t.Errorf("unknown: %v", err)
		}
		if _, err := d.OpenShare(dctx, "short"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("malformed: %v", err)
		}
		s, token := mk(diagram.RoleViewer, &fid, ptr(3600))
		if s.ExpiresAt == nil || !s.ExpiresAt.After(d.clock()) {
			t.Errorf("expiry = %v", s.ExpiresAt)
		}
		if _, err := d.OpenShare(dctx, token); err != nil {
			t.Fatal(err)
		}
		e.advance(2 * time.Hour)
		if _, err := d.OpenShare(dctx, token); !code(err, diagram.ErrGone, "SHARE_EXPIRED") {
			t.Errorf("expired open: %v", err)
		}
		if _, err := d.SaveShare(dctx, token, "", ptr("x"), nil, ifMatch(1)); !errors.Is(err, diagram.ErrGone) {
			t.Errorf("expired save: %v", err)
		}
		e.advance(-2 * time.Hour)

		r, rt := mk(diagram.RoleEditor, &fid, nil)
		if err := d.RevokeShare(dctx, alice, pid, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.OpenShare(dctx, rt); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("revoked open: %v", err)
		}
		if _, err := d.SaveShare(dctx, rt, "", ptr("x"), nil, ifMatch(1)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("revoked save: %v", err)
		}
		if err := d.RevokeShare(dctx, alice, pid, r.ID); err != nil {
			t.Errorf("revoking twice: %v", err)
		}
		if err := d.RevokeShare(dctx, alice, pid, "shr_missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("missing: %v", err)
		}
		// a link of another project cannot be revoked here
		foreign := project(t, d, alice, "F2")
		fs, _, _ := d.CreateShare(dctx, alice, foreign.Project.ID, NewShare{Role: diagram.RoleViewer})
		if err := d.RevokeShare(dctx, alice, pid, fs.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("foreign link: %v", err)
		}
	})

	t.Run("validation and permissions", func(t *testing.T) {
		var v *domain.ValidationError
		for name, in := range map[string]NewShare{
			"owner role":    {Role: "owner"},
			"admin":         {Role: "admin"},
			"no role":       {},
			"expiry short":  {Role: diagram.RoleViewer, ExpiresInSeconds: ptr(5)},
			"expiry negtv":  {Role: diagram.RoleViewer, ExpiresInSeconds: ptr(-5)},
			"expiry long":   {Role: diagram.RoleViewer, ExpiresInSeconds: ptr(diagram.MaxShareSeconds + 1)},
			"empty file id": {Role: diagram.RoleViewer, FileID: ptr("")},
		} {
			if _, _, err := d.CreateShare(dctx, alice, pid, in); !errors.As(err, &v) {
				t.Errorf("%s: %v", name, err)
			}
		}
		foreign := project(t, d, alice, "F3")
		if _, _, err := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer, FileID: &foreign.File.ID}); !hasCode(err, "FILE_NOT_FOUND") {
			t.Errorf("file of another project: %v", err)
		}
		if _, _, err := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer, FileID: ptr("fil_missing")}); !hasCode(err, "FILE_NOT_FOUND") {
			t.Errorf("missing file: %v", err)
		}
		e.mem.AddMember(pid, bob, diagram.RoleEditor)
		if _, _, err := d.CreateShare(dctx, bob, pid, NewShare{Role: diagram.RoleViewer}); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("editor create: %v", err)
		}
		if _, err := d.Shares(dctx, bob, pid); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("editor list: %v", err)
		}
		if err := d.RevokeShare(dctx, bob, pid, "x"); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("editor revoke: %v", err)
		}
		if _, _, err := d.CreateShare(dctx, eve, pid, NewShare{Role: diagram.RoleViewer}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("stranger create: %v", err)
		}
	})

	t.Run("deleting a file or the project kills its links", func(t *testing.T) {
		f, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "doomed"})
		_, ft, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer, FileID: &f.ID})
		if _, err := d.OpenShare(dctx, ft); err != nil {
			t.Fatal(err)
		}
		if err := d.DeleteFile(dctx, alice, f.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.OpenShare(dctx, ft); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("file link after file delete: %v", err)
		}
		p2 := project(t, d, alice, "Gone")
		_, pt, _ := d.CreateShare(dctx, alice, p2.Project.ID, NewShare{Role: diagram.RoleViewer})
		if err := d.DeleteProject(dctx, alice, p2.Project.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.OpenShare(dctx, pt); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("project link after project delete: %v", err)
		}
	})
}

func TestDiagramShareTokenAccessToFileRoutes(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	res := project(t, d, alice, "P")
	pid, fid := res.Project.ID, res.File.ID
	other, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "other"})
	_, ft, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer, FileID: &fid})
	_, pt, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer})
	foreign := project(t, d, alice, "Other project")
	_, xt, _ := d.CreateShare(dctx, alice, foreign.Project.ID, NewShare{Role: diagram.RoleEditor})

	// anonymous with a token: the link's role, scoped to its file or project
	if f, err := d.GetFileVia(dctx, 0, ft, fid); err != nil || f.Role != diagram.RoleViewer {
		t.Errorf("file link: %v %s", err, f.Role)
	}
	if _, err := d.GetFileVia(dctx, 0, ft, other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("file link, other file: %v", err)
	}
	if _, err := d.GetFileVia(dctx, 0, pt, other.ID); err != nil {
		t.Errorf("project link: %v", err)
	}
	if _, err := d.GetFileVia(dctx, 0, xt, other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("link of another project: %v", err)
	}
	if _, err := d.GetFileVia(dctx, 0, "", fid); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("anonymous: %v", err)
	}
	// a signed-in stranger may bring a token; a member's own role goes first
	if _, err := d.GetFileVia(dctx, eve, ft, fid); err != nil {
		t.Errorf("stranger with token: %v", err)
	}
	if _, err := d.GetFileVia(dctx, eve, "", fid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger without token: %v", err)
	}
	// the token never lifts a role: a viewer link cannot save, and thumbnails need read only
	if _, err := d.SaveShare(dctx, ft, "", ptr("x"), nil, ifMatch(1)); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer link save: %v", err)
	}
	// library: only project-wide links, read only
	if _, err := d.GetLibrary(dctx, 0, pt, pid); err != nil {
		t.Errorf("project link library: %v", err)
	}
	if _, err := d.GetLibrary(dctx, 0, ft, pid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("file link library: %v", err)
	}
	if _, err := d.GetLibrary(dctx, 0, xt, pid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("foreign link library: %v", err)
	}
	if _, err := d.GetLibrary(dctx, 0, "", pid); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("anonymous library: %v", err)
	}
}

func TestDiagramAssets(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	d.MaxAssetBytes = 1000

	a := must1[StoredAsset](t)(d.StoreAsset(dctx, alice, pngBytes))
	if a.Deduped || a.Mime != "image/png" || a.Size != len(pngBytes) || a.Hash != diagram.HashBytes(pngBytes) {
		t.Errorf("asset = %+v", a)
	}
	if _, ok := e.blobs.data["diagram/"+a.Hash]; !ok {
		t.Error("blob not stored under its hash")
	}
	again := must1[StoredAsset](t)(d.StoreAsset(dctx, bob, pngBytes))
	if !again.Deduped || again.Hash != a.Hash || len(e.blobs.data) != 1 {
		t.Errorf("dedupe = %+v, %d blobs", again, len(e.blobs.data))
	}
	if m := must1[StoredAsset](t)(d.StoreAsset(dctx, alice, jpegBytes)); m.Mime != "image/jpeg" {
		t.Errorf("jpeg = %+v", m)
	}
	if m := must1[StoredAsset](t)(d.StoreAsset(dctx, alice, webpBytes)); m.Mime != "image/webp" {
		t.Errorf("webp = %+v", m)
	}

	for name, data := range map[string][]byte{
		"gif":   []byte("GIF89a......"),
		"html":  []byte("<html><script>alert(1)</script></html>"),
		"svg":   []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><rect width="4" height="4"/></svg>`),
		"exe":   []byte("MZ\x90\x00"),
		"fake":  []byte("this is not a png"),
		"empty": nil,
	} {
		if _, err := d.StoreAsset(dctx, alice, data); !code(err, diagram.ErrUnsupportedMedia, "UNSUPPORTED_MEDIA_TYPE") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := d.StoreAsset(dctx, alice, append(pngBytes, make([]byte, 1000)...)); !code(err, diagram.ErrTooLarge, "PAYLOAD_TOO_LARGE") {
		t.Errorf("too large: %v", err)
	}
	if _, err := d.StoreAsset(dctx, alice, jpegBytes, diagram.MimePNG); !errors.Is(err, diagram.ErrUnsupportedMedia) {
		t.Errorf("only png: %v", err)
	}

	t.Run("a blob that went missing is healed by the next upload", func(t *testing.T) {
		delete(e.blobs.data, "diagram/"+a.Hash)
		if r := must1[StoredAsset](t)(d.StoreAsset(dctx, alice, pngBytes)); !r.Deduped {
			t.Errorf("heal = %+v", r)
		}
		if _, ok := e.blobs.data["diagram/"+a.Hash]; !ok {
			t.Error("blob not restored")
		}
	})

	t.Run("lookup", func(t *testing.T) {
		if meta := must1[diagram.Asset](t)(d.AssetMeta(dctx, a.Hash)); meta.Mime != "image/png" || meta.UploadedBy != alice {
			t.Errorf("meta = %+v", meta)
		}
		obj := must1[Object](t)(d.AssetBlob(dctx, a.Hash))
		got, _ := io.ReadAll(obj.Body)
		obj.Body.Close()
		if !bytes.Equal(got, pngBytes) {
			t.Error("blob differs")
		}
		for _, h := range []string{"nothex", strings.Repeat("a", 64), "../../etc/passwd", strings.ToUpper(a.Hash)} {
			if _, err := d.AssetMeta(dctx, h); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("meta %q: %v", h, err)
			}
			if _, err := d.AssetBlob(dctx, h); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("blob %q: %v", h, err)
			}
		}
	})
}

func TestDiagramThumbnails(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	res := project(t, d, alice, "P")
	pid, fid := res.Project.ID, res.File.ID
	e.mem.AddMember(pid, bob, diagram.RoleViewer)

	if _, err := d.Thumbnail(dctx, alice, "", fid); !code(err, domain.ErrNotFound, "NOT_FOUND") {
		t.Errorf("no thumbnail yet: %v", err)
	}
	hash := must1[string](t)(d.SetThumbnail(dctx, alice, fid, pngBytes))
	if hash != diagram.HashBytes(pngBytes) {
		t.Errorf("hash = %q", hash)
	}
	f := must1[DiagramFile](t)(d.GetFile(dctx, alice, fid))
	if f.ThumbnailHash == nil || *f.ThumbnailHash != hash || f.Version != 1 {
		t.Errorf("file = %+v (the version must not change)", f.File)
	}
	_, files := func() ([]diagram.Folder, []diagram.File) {
		tr := must1[DiagramTree](t)(d.Tree(dctx, alice, pid))
		return tr.Folders, tr.Files
	}()
	if files[0].ThumbnailHash == nil || *files[0].ThumbnailHash != hash {
		t.Errorf("tree file = %+v", files[0])
	}
	if a := must1[diagram.Asset](t)(d.Thumbnail(dctx, bob, "", fid)); a.Hash != hash || a.Mime != "image/png" {
		t.Errorf("viewer thumbnail = %+v", a)
	}
	_, token, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer})
	if _, err := d.Thumbnail(dctx, 0, token, fid); err != nil {
		t.Errorf("share thumbnail: %v", err)
	}
	if _, err := d.Thumbnail(dctx, eve, "", fid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}
	if _, err := d.Thumbnail(dctx, 0, "", fid); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("anonymous: %v", err)
	}

	if _, err := d.SetThumbnail(dctx, bob, fid, pngBytes); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer set: %v", err)
	}
	if _, err := d.SetThumbnail(dctx, eve, fid, pngBytes); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger set: %v", err)
	}
	if _, err := d.SetThumbnail(dctx, alice, fid, webpBytes); err != nil {
		t.Errorf("webp: %v", err)
	}
	if a := must1[diagram.Asset](t)(d.Thumbnail(dctx, alice, "", fid)); a.Mime != "image/webp" {
		t.Errorf("mime = %s", a.Mime)
	}
	if _, err := d.SetThumbnail(dctx, alice, fid, nil); !errors.Is(err, diagram.ErrUnsupportedMedia) {
		t.Errorf("empty: %v", err)
	}
	if _, err := d.SetThumbnail(dctx, alice, fid, append(pngBytes, make([]byte, MaxThumbnailBytes)...)); !errors.Is(err, diagram.ErrTooLarge) {
		t.Errorf("over 200 KB: %v", err)
	}
	if _, err := d.SetThumbnail(dctx, alice, fid, jpegBytes); !errors.Is(err, diagram.ErrUnsupportedMedia) {
		t.Errorf("jpeg preview: %v", err)
	}
	if _, err := d.SetThumbnail(dctx, alice, fid, []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")); !errors.Is(err, diagram.ErrUnsupportedMedia) {
		t.Errorf("svg preview: %v", err)
	}
}

func libDoc(name string) json.RawMessage {
	return json.RawMessage(`{"components":{"cmp_1":{"id":"cmp_1","name":"` + name + `","rev":1,"width":200,"height":100,"futureKey":true,
		"nodes":[{"key":"k1","type":"icon","x":0,"y":0,"width":200,"height":100,"data":{"label":"Service"}}],"edges":[]}},
		"styles":{"colors":{"c1":{"id":"c1","name":"Brand","value":"#2f6fdb","dark":"#8ab4ff"}},"texts":{"t1":{"id":"t1","name":"Title","fontSize":18,"bold":true}},
		"effects":{"e1":{"id":"e1","name":"Soft","shadow":{"x":0,"y":2,"blur":8,"color":"#0003"}},"e0":{"id":"e0","name":"None","shadow":null}}},
		"tokens":{"tk1":{"id":"tk1","name":"surface","light":"#fff","dark":"#111"}},"extra":1}`)
}

func TestDiagramLibrary(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	res := project(t, d, alice, "Lib")
	pid := res.Project.ID
	e.mem.AddMember(pid, bob, diagram.RoleEditor)
	e.mem.AddMember(pid, dana, diagram.RoleViewer)

	l := must1[diagram.Library](t)(d.GetLibrary(dctx, dana, "", pid))
	if l.Rev != 0 || string(l.Doc) != string(diagram.EmptyLibrary) {
		t.Errorf("fresh library = %d %s", l.Rev, l.Doc)
	}
	if _, err := d.GetLibrary(dctx, eve, "", pid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}

	saved := must1[diagram.Library](t)(d.SaveLibrary(dctx, bob, pid, 0, libDoc("Card")))
	if saved.Rev != 1 {
		t.Errorf("rev = %d", saved.Rev)
	}
	got := must1[diagram.Library](t)(d.GetLibrary(dctx, dana, "", pid))
	if got.Rev != 1 || !strings.Contains(string(got.Doc), `"futureKey"`) || !strings.Contains(string(got.Doc), `"extra"`) {
		t.Errorf("library = %d %s (unknown fields must survive)", got.Rev, got.Doc)
	}
	if _, err := d.SaveLibrary(dctx, dana, pid, 1, libDoc("x")); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer save: %v", err)
	}

	t.Run("a stale baseRev returns the current library", func(t *testing.T) {
		_, err := d.SaveLibrary(dctx, alice, pid, 0, diagram.EmptyLibrary)
		var c *diagram.LibraryConflict
		if !errors.As(err, &c) || !errors.Is(err, diagram.ErrLibraryConflict) || c.Code() != "LIBRARY_CONFLICT" {
			t.Fatalf("err = %v", err)
		}
		if c.Current.Rev != 1 || !strings.Contains(string(c.Current.Doc), "cmp_1") {
			t.Errorf("current = %d %s", c.Current.Rev, c.Current.Doc)
		}
		if next := must1[diagram.Library](t)(d.SaveLibrary(dctx, alice, pid, 1, libDoc("Card 2"))); next.Rev != 2 {
			t.Errorf("rev = %d", next.Rev)
		}
		// ahead of the current revision is stale too
		if _, err := d.SaveLibrary(dctx, alice, pid, 9, libDoc("x")); !errors.Is(err, diagram.ErrLibraryConflict) {
			t.Errorf("ahead: %v", err)
		}
	})

	t.Run("invalid documents are refused and change nothing", func(t *testing.T) {
		var v *domain.ValidationError
		for name, doc := range map[string]string{
			"not an object":  `[]`,
			"no components":  `{"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{}}`,
			"component stub": `{"components":{"x":{"id":"x"}},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{}}`,
			"bad node x":     strings.Replace(string(libDoc("a")), `"x":0,"y":0,"width"`, `"x":"left","y":0,"width"`, 1),
			"token no dark":  `{"components":{},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{"t":{"id":"t","name":"x","light":"#fff"}}}`,
			"shadow partial": `{"components":{},"styles":{"colors":{},"texts":{},"effects":{"e":{"id":"e","name":"x","shadow":{"x":1}}}},"tokens":{}}`,
		} {
			if _, err := d.SaveLibrary(dctx, alice, pid, 2, json.RawMessage(doc)); !errors.As(err, &v) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := d.SaveLibrary(dctx, alice, pid, -1, libDoc("x")); !errors.As(err, &v) {
			t.Errorf("negative baseRev: %v", err)
		}
		big := `{"components":{},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{},"pad":"` + strings.Repeat("x", diagram.MaxLibraryBytes) + `"}`
		if _, err := d.SaveLibrary(dctx, alice, pid, 2, json.RawMessage(big)); !errors.Is(err, diagram.ErrTooLarge) {
			t.Errorf("too large: %v", err)
		}
		if l := must1[diagram.Library](t)(d.GetLibrary(dctx, alice, "", pid)); l.Rev != 2 {
			t.Errorf("rev = %d, want 2", l.Rev)
		}
	})

	t.Run("concurrent first saves: one wins", func(t *testing.T) {
		p2 := project(t, d, alice, "Race").Project.ID
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins, conflicts := 0, 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := d.SaveLibrary(dctx, alice, p2, 0, libDoc("r"))
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					wins++
				} else if errors.Is(err, diagram.ErrLibraryConflict) {
					conflicts++
				} else {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if wins != 1 || conflicts != 7 {
			t.Errorf("wins = %d, conflicts = %d", wins, conflicts)
		}
	})
}

func stickies(ids ...string) json.RawMessage {
	var ns []string
	for _, id := range ids {
		ns = append(ns, `{"id":"`+id+`","type":"sticky","position":{"x":5,"y":5},"data":{"label":"secret","private":false,"authorId":"999"}}`)
	}
	return json.RawMessage(`{"nodes":[` + strings.Join(ns, ",") + `],"edges":[]}`)
}

func idsOf(t *testing.T, p diagram.Private) []string {
	t.Helper()
	var doc struct {
		Nodes []struct {
			ID   string
			Data map[string]any
		}
	}
	if err := json.Unmarshal(p.Data, &doc); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range doc.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestDiagramPrivateStickies(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	res := project(t, d, alice, "Priv")
	pid, fid := res.Project.ID, res.File.ID
	e.mem.AddMember(pid, bob, diagram.RoleEditor)
	e.mem.AddMember(pid, dana, diagram.RoleViewer)

	if p := must1[diagram.Private](t)(d.GetPrivate(dctx, alice, fid)); string(p.Data) != string(diagram.EmptyPrivate) || !p.UpdatedAt.IsZero() {
		t.Errorf("fresh = %s %v", p.Data, p.UpdatedAt)
	}
	put := must1[diagram.Private](t)(d.PutPrivate(dctx, alice, fid, stickies("p1", "p2")))
	if got := idsOf(t, put); len(got) != 2 || put.UpdatedAt.IsZero() {
		t.Errorf("put = %s", put.Data)
	}
	// stamped as private and mine, whatever the client sent
	var doc struct {
		Nodes []struct{ Data map[string]any }
	}
	_ = json.Unmarshal(put.Data, &doc)
	for _, n := range doc.Nodes {
		if n.Data["private"] != true || n.Data["authorId"] != "1" || n.Data["label"] != "secret" {
			t.Errorf("data = %v", n.Data)
		}
	}
	if got := idsOf(t, must1[diagram.Private](t)(d.GetPrivate(dctx, alice, fid))); len(got) != 2 {
		t.Errorf("mine = %v", got)
	}
	// nobody else's
	for _, who := range []int{bob, dana} {
		if got := idsOf(t, must1[diagram.Private](t)(d.GetPrivate(dctx, who, fid))); len(got) != 0 {
			t.Errorf("user %d sees %v", who, got)
		}
	}
	if _, err := d.GetPrivate(dctx, eve, fid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}

	// a viewer cannot write; an editor writes into their own slot only
	if _, err := d.PutPrivate(dctx, dana, fid, stickies("c1")); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer put: %v", err)
	}
	if _, err := d.PutPrivate(dctx, bob, fid, stickies("b1")); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(t, must1[diagram.Private](t)(d.GetPrivate(dctx, alice, fid))); len(got) != 2 {
		t.Errorf("alice after bob's put = %v", got)
	}
	if got := idsOf(t, must1[diagram.Private](t)(d.GetPrivate(dctx, bob, fid))); len(got) != 1 || got[0] != "b1" {
		t.Errorf("bob = %v", got)
	}

	t.Run("validation and the size cap", func(t *testing.T) {
		var v *domain.ValidationError
		for name, body := range map[string]string{
			"not sticky": `{"nodes":[{"id":"x","type":"icon","position":{"x":0,"y":0},"data":{"label":"n"}}],"edges":[]}`,
			"no label":   `{"nodes":[{"id":"x","type":"sticky","position":{"x":0,"y":0},"data":{}}],"edges":[]}`,
			"no pos":     `{"nodes":[{"id":"x","type":"sticky","data":{"label":"n"}}],"edges":[]}`,
			"bad width":  `{"nodes":[{"id":"x","type":"sticky","width":-1,"position":{"x":0,"y":0},"data":{"label":"n"}}],"edges":[]}`,
			"no edges":   `{"nodes":[]}`,
			"edge stub":  `{"nodes":[],"edges":[{"id":"e"}]}`,
			"not object": `[]`,
		} {
			if _, err := d.PutPrivate(dctx, alice, fid, json.RawMessage(body)); !errors.As(err, &v) {
				t.Errorf("%s: %v", name, err)
			}
		}
		var many []string
		for i := range 501 {
			many = append(many, "n"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+strings.Repeat("y", i/26))
		}
		if _, err := d.PutPrivate(dctx, alice, fid, stickies(many...)); !errors.As(err, &v) {
			t.Errorf("501 stickies: %v", err)
		}
		big := `{"nodes":[` + strings.Repeat(`{"id":"a","type":"sticky","position":{"x":0,"y":0},"data":{"label":"`+strings.Repeat("x", 9000)+`"}},`, 120)
		big = strings.TrimSuffix(big, ",") + `],"edges":[]}`
		if _, err := d.PutPrivate(dctx, alice, fid, json.RawMessage(big)); !errors.Is(err, diagram.ErrTooLarge) {
			t.Errorf("over 1 MB: %v", err)
		}
		if got := idsOf(t, must1[diagram.Private](t)(d.GetPrivate(dctx, alice, fid))); len(got) != 2 {
			t.Errorf("rejected puts must change nothing: %v", got)
		}
	})

	t.Run("an empty set clears; deleting the file removes them", func(t *testing.T) {
		if _, err := d.PutPrivate(dctx, alice, fid, json.RawMessage(`{"nodes":[],"edges":[]}`)); err != nil {
			t.Fatal(err)
		}
		if p := must1[diagram.Private](t)(d.GetPrivate(dctx, alice, fid)); len(idsOf(t, p)) != 0 || !p.UpdatedAt.IsZero() {
			t.Errorf("after clear = %s", p.Data)
		}
		if _, err := e.mem.Private(dctx, fid, alice); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("row must be gone: %v", err)
		}
		second, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "s"})
		must1[diagram.Private](t)(d.PutPrivate(dctx, alice, second.ID, stickies("z")))
		if err := d.DeleteFile(dctx, alice, second.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.mem.Private(dctx, second.ID, alice); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("private data outlived its file: %v", err)
		}
	})
}

func TestStoreAssetSVG(t *testing.T) {
	var d Diagrams
	d.MaxAssetBytes = 1 << 20
	d.SanitizeSVG = func(b []byte) ([]byte, bool, error) {
		if !bytes.Contains(b, []byte("<svg")) {
			return nil, false, nil
		}
		if bytes.Contains(b, []byte("bad")) {
			return nil, true, errors.New("bad")
		}
		return []byte("<svg clean/>"), true, nil
	}
	// refused paths return before touching storage
	if _, err := d.StoreAsset(context.Background(), 1, []byte("<svg bad/>")); !errors.Is(err, diagram.ErrUnsupportedMedia) {
		t.Errorf("unsanitizable: %v", err)
	}
	if _, err := d.StoreAsset(context.Background(), 1, []byte("<svg/>"), diagram.MimePNG); !errors.Is(err, diagram.ErrUnsupportedMedia) {
		t.Errorf("svg where only png: %v", err)
	}
}
