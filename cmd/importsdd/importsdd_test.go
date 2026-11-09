package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// ddl is the old Node server's SQLite schema, newest shape.
const ddl = `
CREATE TABLE users (id text PRIMARY KEY, email text NOT NULL UNIQUE, password_hash text NOT NULL, name text NOT NULL, is_admin integer NOT NULL DEFAULT 0, created_at integer NOT NULL);
CREATE TABLE projects (id text PRIMARY KEY, owner_id text NOT NULL, name text NOT NULL, tree_rev integer NOT NULL DEFAULT 0, created_at integer NOT NULL, updated_at integer NOT NULL, updated_by text);
CREATE TABLE folders (id text PRIMARY KEY, project_id text NOT NULL, parent_id text, name text NOT NULL, position real NOT NULL DEFAULT 0, created_at integer NOT NULL, updated_at integer NOT NULL);
CREATE TABLE files (id text PRIMARY KEY, project_id text NOT NULL, folder_id text, name text NOT NULL, kind text NOT NULL DEFAULT 'sdd', file_kind text NOT NULL DEFAULT 'general', position real NOT NULL DEFAULT 0, data text NOT NULL, version integer NOT NULL, page_count integer NOT NULL DEFAULT 1, thumbnail_hash text, created_at integer NOT NULL, updated_at integer NOT NULL, updated_by text);
CREATE TABLE file_versions (id text PRIMARY KEY, file_id text NOT NULL, version integer NOT NULL, name text NOT NULL, data text NOT NULL, label text, named integer NOT NULL DEFAULT 0, created_by text, created_at integer NOT NULL);
CREATE TABLE file_members (file_id text NOT NULL, user_id text NOT NULL, role text NOT NULL, created_at integer NOT NULL, PRIMARY KEY (file_id, user_id));
CREATE TABLE user_prefs (user_id text PRIMARY KEY, data text NOT NULL, updated_at integer NOT NULL);
CREATE TABLE project_members (project_id text NOT NULL, user_id text NOT NULL, role text NOT NULL, created_at integer NOT NULL, PRIMARY KEY (project_id, user_id));
CREATE TABLE share_links (id text PRIMARY KEY, project_id text NOT NULL, file_id text, token_hash text NOT NULL UNIQUE, role text NOT NULL, expires_at integer, revoked_at integer, created_by text NOT NULL, created_at integer NOT NULL);
CREATE TABLE assets (hash text PRIMARY KEY, mime text NOT NULL, size integer NOT NULL, uploaded_by text, created_at integer NOT NULL);
CREATE TABLE file_ydocs (file_id text PRIMARY KEY, snapshot text NOT NULL, seq integer NOT NULL DEFAULT 0, updated_at integer NOT NULL);
CREATE TABLE file_yupdates (file_id text NOT NULL, seq integer NOT NULL, data text NOT NULL, created_at integer NOT NULL, PRIMARY KEY (file_id, seq));
CREATE TABLE comments (id text PRIMARY KEY, file_id text NOT NULL, project_id text NOT NULL, parent_id text, author_id text NOT NULL, body text NOT NULL, anchor text, resolved_at integer, resolved_by text, created_at integer NOT NULL, updated_at integer NOT NULL);
CREATE TABLE comment_mentions (comment_id text NOT NULL, user_id text NOT NULL, PRIMARY KEY (comment_id, user_id));
CREATE TABLE comment_reads (user_id text NOT NULL, file_id text NOT NULL, read_at integer NOT NULL, PRIMARY KEY (user_id, file_id));
CREATE TABLE notifications (id text PRIMARY KEY, user_id text NOT NULL, kind text NOT NULL, project_id text NOT NULL, file_id text NOT NULL, comment_id text, actor_id text, text text NOT NULL, created_at integer NOT NULL, read_at integer);
CREATE TABLE project_library (project_id text PRIMARY KEY, rev integer NOT NULL DEFAULT 0, data text NOT NULL, updated_at integer NOT NULL, updated_by text);
CREATE TABLE access_tokens (id text PRIMARY KEY, user_id text NOT NULL, name text NOT NULL, token_hash text NOT NULL UNIQUE, prefix text NOT NULL, scopes text NOT NULL, project_ids text, expires_at integer, last_used_at integer, created_at integer NOT NULL, revoked_at integer);
CREATE TABLE webhooks (id text PRIMARY KEY, project_id text NOT NULL, url text NOT NULL, secret text NOT NULL, events text NOT NULL, active integer NOT NULL DEFAULT 1, created_by text NOT NULL, created_at integer NOT NULL);
CREATE TABLE mcp_audit (id text PRIMARY KEY, user_id text NOT NULL, token_id text, tool text NOT NULL, project_id text, file_id text, status text NOT NULL, detail text, duration_ms integer, created_at integer NOT NULL);
CREATE TABLE file_private (file_id text NOT NULL, user_id text NOT NULL, data text NOT NULL, updated_at integer NOT NULL, PRIMARY KEY (file_id, user_id));
`

const (
	goodDoc = `{"version":2,"activePageId":"pg","pages":[{"id":"pg","name":"Page 1","nodes":[{"id":"n1","type":"sticky","selected":true}],"edges":[]}]}`
	badDoc  = `{"version":1}`
	ts      = 1792738472734 // ms
	oldHash = "$argon2id$v=19$m=65536,t=3,p=4$UnLM05xsHfwyodeHoPE9kA$tBXFHLo6N/QIHDgmNDR0UnpLxyd7u5f4SwkNpVLP7fc"
)

var png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 32)...)

func hexHash(b []byte) string { return diagram.HashBytes(b) }

// fixture builds a small old database and storage directory.
func fixture(t *testing.T) (Source, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sdd.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	okHash := hexHash(png)
	badBlob := hexHash([]byte("claimed"))
	shareHash := hexHash([]byte("share-token"))
	y := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	stmts := []string{
		`INSERT INTO users VALUES ('u1','Ali@Example.com','` + oldHash + `','Ali Valiyev',0,1), ('u2','bob@example.com','x','Bob',0,1), ('u3','admin@example.com','x','Admin',1,1)`,
		`INSERT INTO projects VALUES ('p1','u1','Main',3,` + itoa(ts) + `,` + itoa(ts) + `,'u2'), ('p2','u2','Bob only',0,1,1,NULL)`,
		`INSERT INTO folders VALUES ('fo2','p1','fo1','Child',2,1,1), ('fo1','p1',NULL,'Root',1,1,1), ('fo3','p1','nope','Orphan',3,1,1)`,
		`INSERT INTO files VALUES ('fi1','p1','fo2','Good','erd','db',1,'` + goodDoc + `',2,1,'` + okHash + `',1,1,'u1'),
			('fi2','p1',NULL,'Invalid','sdd','general',2,'` + badDoc + `',1,1,NULL,1,1,NULL),
			('fi3','p1',NULL,'Garbage','sdd','general',3,'not json',1,1,NULL,1,1,NULL),
			('fi4','p1','gone','Lost folder','sdd','weird',4,'` + goodDoc + `',1,1,NULL,1,1,NULL)`,
		`INSERT INTO file_versions VALUES ('v1','fi1',1,'Good','` + goodDoc + `','first',0,'u1',1), ('v2','fi1',2,'Good','` + goodDoc + `',NULL,1,'u2',2),
			('v3','fi1',2,'Good','` + goodDoc + `',NULL,0,NULL,3), ('v4','fi2',1,'Invalid','` + badDoc + `',NULL,0,NULL,4), ('v5','fi9',1,'x','` + goodDoc + `',NULL,0,NULL,5)`,
		`INSERT INTO project_members VALUES ('p1','u2','editor',1), ('p1','u3','boss',1), ('p1','u1','viewer',1)`,
		`INSERT INTO file_members VALUES ('fi1','u3','viewer',1), ('fi3','u3','viewer',1)`,
		`INSERT INTO share_links VALUES ('s1','p1',NULL,'` + shareHash + `','viewer',NULL,NULL,'u1',1), ('s2','p1',NULL,'ABC','viewer',NULL,NULL,'u1',1), ('s3','p1','fi3','` + hexHash([]byte("x")) + `','editor',NULL,NULL,'u1',1)`,
		`INSERT INTO project_library VALUES ('p1',0,'{"components":[]}',1,'u1')`,
		`INSERT INTO file_private VALUES ('fi1','u1','{"nodes":[],"edges":[]}',1)`,
		`INSERT INTO comments VALUES ('c2','fi1','p1','c1','u3','reply',NULL,NULL,NULL,2,2), ('c1','fi1','p1',NULL,'u3','root','{"type":"point","x":1,"y":2}',5,'u1',1,1), ('c3','fi1','p1','zz','u1','orphan',NULL,NULL,NULL,3,3)`,
		`INSERT INTO comment_mentions VALUES ('c1','u1'), ('c1','u2'), ('c3','u1')`,
		`INSERT INTO comment_reads VALUES ('u1','fi1',9)`,
		`INSERT INTO notifications VALUES ('n1','u1','mention','p1','fi1','c1','u3','hey',1,NULL), ('n2','u1','weird','p1','fi1',NULL,NULL,'x',1,NULL), ('n3','u1','reply','p1','fi1','c3','u1','r',1,7)`,
		`INSERT INTO webhooks VALUES ('w1','p1','https://example.com/h','whsec_x','["file.created","bogus"]',1,'u1',1)`,
		`INSERT INTO file_ydocs VALUES ('fi1','` + y + `',4,1)`,
		`INSERT INTO file_yupdates VALUES ('fi1',5,'` + y + `',1), ('fi1',6,'%%%',1)`,
		`INSERT INTO assets VALUES ('` + okHash + `','image/png',1,'u1',1), ('` + hexHash([]byte("absent")) + `','image/png',1,NULL,1), ('` + badBlob + `','image/png',1,NULL,1)`,
		`INSERT INTO access_tokens VALUES ('t1','u1','ci','` + hexHash([]byte("tok")) + `','sdd_pat_','["files:read","nope"]','["p1"]',NULL,NULL,1,NULL), ('t2','u1','all','` + hexHash([]byte("tok2")) + `','sdd_pat_','["files:write"]',NULL,NULL,NULL,1,NULL)`,
		`INSERT INTO user_prefs VALUES ('u1','{"theme":"dark"}',1), ('u2','{}',1)`,
		`INSERT INTO mcp_audit VALUES ('a1','u1','t1','get_file','p1','fi1','ok','{}',12,1)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	db.Close()
	// blobs: the good one, and the mismatching one under a wrong content
	store := filepath.Join(dir, "storage")
	put := func(hash string, b []byte) {
		p := filepath.Join(store, hash[:2], hash[2:4], hash)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(okHash, png)
	put(badBlob, []byte("different"))
	src, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src, store
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

type memBlobs struct{ put map[string][]byte }

func (m *memBlobs) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, _ := io.ReadAll(r)
	m.put[key] = b
	return nil
}

func (m *memBlobs) Get(_ context.Context, key string) (application.Object, error) {
	if b, ok := m.put[key]; ok {
		return application.Object{Body: io.NopCloser(bytes.NewReader(b))}, nil
	}
	return application.Object{}, domain.ErrNotFound
}

func newImporter(t *testing.T, create bool) (*importer, *dryStore, *memBlobs) {
	t.Helper()
	src, storeDir := fixture(t)
	st := &dryStore{users: map[string]int{"ali@example.com": 11}}
	blobs := &memBlobs{put: map[string][]byte{}}
	return &importer{src: src, st: st, blobsIn: localBlobs{storeDir}, blobsOut: blobs, createMissing: create}, st, blobs
}

func hasProblem(r *report, entity, id, sub string) bool {
	return slices.ContainsFunc(r.problems, func(p problem) bool {
		return p.Entity == entity && p.ID == id && strings.Contains(p.Msg, sub)
	})
}

func TestImportMapsEverything(t *testing.T) {
	im, st, blobs := newImporter(t, false)
	rep, err := im.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Projects) != 1 || st.Projects[0].Project.ID != "p1" {
		t.Fatalf("projects = %v", st.Projects)
	}
	p := st.Projects[0]
	if p.Project.OwnerID != 11 || p.Project.UpdatedBy != nil || p.Project.TreeRev != 3 || p.Project.CreatedAt.UnixMilli() != ts {
		t.Errorf("project = %+v", p.Project)
	}
	if !hasProblem(rep, "project", "p2", "owner") || !hasProblem(rep, "user", "u2", "no account") {
		t.Errorf("unmapped owner not reported: %v", rep.problems)
	}

	// folders: parents first; the orphan moves to the root
	var ids []string
	for _, f := range p.Folders {
		ids = append(ids, f.ID)
		if f.ID == "fo3" && f.ParentID != nil {
			t.Error("orphan folder keeps its parent")
		}
	}
	if !slices.Equal(ids[:2], []string{"fo1", "fo2"}) || len(ids) != 3 {
		t.Errorf("folder order = %v", ids)
	}

	// files: validated and normalized, invalid kept raw, garbage skipped
	files := map[string]dgFile{}
	for _, f := range p.Files {
		files[f.ID] = f
	}
	if len(files) != 3 || files["fi3"].ID != "" {
		t.Fatalf("files = %v", files)
	}
	if g := files["fi1"]; g.Kind != "erd" || g.FileKind != "db" || strings.Contains(string(g.Data), "selected") || !strings.Contains(string(g.Data), `"id":"fi1"`) || g.FolderID == nil {
		t.Errorf("good file = %+v", g)
	}
	if string(files["fi2"].Data) != badDoc || !hasProblem(rep, "file", "fi2", "raw data kept") {
		t.Errorf("invalid file: %s", files["fi2"].Data)
	}
	if !hasProblem(rep, "file", "fi3", "not storable") {
		t.Error("garbage file not reported")
	}
	if f := files["fi4"]; f.FolderID != nil || f.FileKind != "general" {
		t.Errorf("lost-folder file = %+v", f)
	}

	// versions: the duplicate number and the unknown file are skipped
	if len(p.Versions) != 3 || !hasProblem(rep, "version", "v3", "duplicate") {
		t.Errorf("versions = %d, problems %v", len(p.Versions), rep.problems)
	}
	i := slices.IndexFunc(p.Versions, func(v dgVersion) bool { return v.ID == "v2" })
	if v := p.Versions[i]; !v.Named || v.CreatedBy != nil {
		t.Errorf("v2 = %+v (u2 has no account: created_by must be NULL)", v)
	}

	// members: u2 unmapped, u3 unmapped; the owner row is dropped
	if len(p.Members) != 0 || len(p.FileMembers) != 0 {
		t.Errorf("members = %v / %v", p.Members, p.FileMembers)
	}

	// share links: the hash is kept as is; a bad hash and a link to a lost file are skipped
	if len(p.Shares) != 1 || p.Shares[0].TokenHash != hexHash([]byte("share-token")) || p.Shares[0].CreatedBy == nil {
		t.Errorf("shares = %+v", p.Shares)
	}
	if !hasProblem(rep, "share link", "s2", "sha256") || !hasProblem(rep, "share link", "s3", "not imported") {
		t.Errorf("share problems: %v", rep.problems)
	}

	if p.Library == nil || p.Library.Rev != 1 || len(p.Private) != 1 {
		t.Errorf("library %+v private %v", p.Library, p.Private)
	}

	// comments: parent first, orphan reply skipped, anchor kept, mentions of mapped users only
	if len(p.Comments) != 2 || p.Comments[0].ID != "c1" || p.Comments[1].ParentID == nil || p.Comments[0].ResolvedAt == nil || p.Comments[0].Anchor == nil || p.Comments[0].AuthorID != nil {
		t.Errorf("comments = %+v", p.Comments)
	}
	if len(p.Mentions) != 1 || p.Mentions[0] != (dgMention{"c1", 11}) || len(p.Reads) != 1 {
		t.Errorf("mentions %v reads %v", p.Mentions, p.Reads)
	}
	if len(p.Notifs) != 2 || p.Notifs[0].Type != "diagram_mention" || p.Notifs[1].CommentID != nil || p.Notifs[1].ReadAt == nil {
		t.Errorf("notifications = %+v", p.Notifs)
	}
	if len(p.Webhooks) != 1 || !slices.Equal(p.Webhooks[0].Events, []string{"file.created"}) || p.Webhooks[0].Secret != "whsec_x" {
		t.Errorf("webhooks = %+v", p.Webhooks)
	}
	if len(p.YDocs) != 1 || !bytes.Equal(p.YDocs[0].Snapshot, []byte{1, 2, 3}) || len(p.YUpdates) != 1 || p.YUpdates[0].Seq != 5 {
		t.Errorf("yjs = %+v %+v", p.YDocs, p.YUpdates)
	}

	// global: blob uploaded under diagram/<sha256>; absent and mismatching blobs skipped
	g := st.Global
	if len(g.Assets) != 1 || g.Assets[0].Mime != "image/png" || g.Assets[0].Size != len(png) || !bytes.Equal(blobs.put["diagram/"+g.Assets[0].Hash], png) {
		t.Errorf("assets = %+v blobs %d", g.Assets, len(blobs.put))
	}
	if rep.skipped["asset"] != 2 {
		t.Errorf("skipped assets = %d: %v", rep.skipped["asset"], rep.problems)
	}
	if len(g.Tokens) != 2 || !slices.Equal(g.Tokens[0].Scopes, []string{"files:read"}) || !slices.Equal(g.Tokens[0].ProjectIDs, []string{"p1"}) || g.Tokens[1].ProjectIDs != nil {
		t.Errorf("tokens = %+v", g.Tokens)
	}
	if len(g.Prefs) != 1 || len(g.Audit) != 1 || *g.Audit[0].DurationMs != 12 {
		t.Errorf("prefs %v audit %v", g.Prefs, g.Audit)
	}
}

func TestImportIsIdempotent(t *testing.T) {
	im, st, _ := newImporter(t, false)
	if _, err := im.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep, err := im.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Projects) != 1 || rep.existing["project"] != 1 || rep.imported["file"] != 0 {
		t.Errorf("second run: %d projects, existing %v, imported %v", len(st.Projects), rep.existing, rep.imported)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	im, st, blobs := newImporter(t, false)
	im.dry, im.blobsOut = true, nil
	rep, err := im.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Projects) != 0 || st.Global != nil || len(blobs.put) != 0 {
		t.Error("dry run wrote")
	}
	if rep.imported["file"] != 3 || rep.imported["asset"] != 1 {
		t.Errorf("counts = %v", rep.imported)
	}
	var out bytes.Buffer
	rep.print(&out, true, false)
	if !strings.Contains(out.String(), "would import") {
		t.Errorf("summary:\n%s", out.String())
	}
}

func TestCreateMissingKeepsOldPassword(t *testing.T) {
	im, st, _ := newImporter(t, true)
	rep, err := im.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Created) != 2 { // bob and admin; ali matched by email, case-insensitively
		t.Fatalf("created = %+v", st.Created)
	}
	if bob := st.Created[0]; bob.Username != "Bob" || bob.PasswordHash != "" {
		t.Errorf("bob = %+v (hash 'x' is not argon2id and must not be copied)", bob)
	}
	if len(st.Projects) != 2 || rep.imported["user (created)"] != 2 {
		t.Errorf("projects %d, imported %v", len(st.Projects), rep.imported)
	}
	// the kept hash of a created user
	st2 := &dryStore{}
	im2 := &importer{src: im.src, st: st2, createMissing: true}
	if _, err := im2.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st2.Created[0].Email != "Ali@Example.com" || st2.Created[0].Username != "Ali_Valiyev" || st2.Created[0].PasswordHash != oldHash {
		t.Errorf("ali = %+v", st2.Created[0])
	}
}

func TestUsernames(t *testing.T) {
	cases := []struct{ name, email, want string }{
		{"Ali Valiyev", "a@b.c", "Ali_Valiyev"},
		{"  ", "pw-99c02ae2c7@example.test", "pw_99c02ae2c7"},
		{"x", "yo@b.c", "yo_x"},
		{strings.Repeat("a", 40), "a@b.c", strings.Repeat("a", 32)},
	}
	for _, c := range cases {
		if got := usernameBase(c.name, c.email); got != c.want {
			t.Errorf("usernameBase(%q,%q) = %q, want %q", c.name, c.email, got, c.want)
		}
	}
	if got := usernameTry(strings.Repeat("a", 32), 2); len(got) != 32 || !strings.HasSuffix(got, "_3") {
		t.Errorf("usernameTry = %q", got)
	}
}

func TestEpoch(t *testing.T) {
	if epoch(ts).Year() != 2026 || epoch(1_700_000_000).Year() != 2023 {
		t.Error("epoch must accept milliseconds and seconds")
	}
}

func TestTreeOrder(t *testing.T) {
	order, bad := treeOrder([]string{"c", "b", "a", "x", "y"}, []string{"b", "a", "", "y", "x"})
	if !slices.Equal(order, []int{2, 1, 0}) || !slices.Equal(bad, []int{3, 4}) {
		t.Errorf("order %v bad %v", order, bad)
	}
}
