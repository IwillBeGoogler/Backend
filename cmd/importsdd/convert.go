package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// converter turns the old rows of one project into a bundle. It reads the
// source but never writes.
type converter struct {
	src   Source
	users map[string]int // old user id -> blog user id
	now   time.Time
	has   map[string]bool
}

func (c *converter) sel(ctx context.Context, table, where string, args ...any) ([]Row, error) {
	if c.has == nil {
		c.has = map[string]bool{}
	}
	ok, seen := c.has[table]
	if !seen {
		var err error
		if ok, err = c.src.Has(ctx, table); err != nil {
			return nil, err
		}
		c.has[table] = ok
	}
	if !ok {
		return nil, nil
	}
	return c.src.Select(ctx, table, where, args...)
}

// user maps an old user id; "" and unknown ids are not mapped.
func (c *converter) user(id string) (int, bool) {
	n, ok := c.users[id]
	return n, ok
}

func (c *converter) userp(id *string) *int {
	if id == nil {
		return nil
	}
	if n, ok := c.user(*id); ok {
		return &n
	}
	return nil
}

var roles = []string{"viewer", "commenter", "editor"}

// jsonOK reports whether raw can be stored as jsonb.
func jsonOK(raw []byte) bool {
	return json.Valid(raw) && !bytes.Contains(raw, []byte(`\u0000`))
}

// document validates a diagram document the way the API does and returns
// what to store. When validation fails the raw JSON is kept (ok is false
// only when it cannot be stored at all).
func document(raw, id, name string, created, updated time.Time) (data json.RawMessage, pages int, kind string, validErr error, ok bool) {
	if !jsonOK([]byte(raw)) {
		return nil, 0, "", nil, false
	}
	p, err := diagram.Prepare(json.RawMessage(raw), id, name, created, updated)
	if err != nil {
		return json.RawMessage(raw), 0, "", err, true
	}
	return p.Data, p.PageCount, p.Kind, nil, true
}

func rawPages(raw json.RawMessage) int {
	var d struct {
		Pages []json.RawMessage `json:"pages"`
	}
	if json.Unmarshal(raw, &d) != nil || len(d.Pages) == 0 {
		return 1
	}
	return len(d.Pages)
}

// treeOrder returns indexes with parents before children. Indexes whose
// parent is unknown (or part of a cycle) come back in bad.
func treeOrder(ids, parents []string) (order, bad []int) {
	placed := make(map[string]bool, len(ids))
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	left := make([]int, len(ids))
	for i := range left {
		left[i] = i
	}
	for len(left) > 0 {
		var next []int
		for _, i := range left {
			if p := parents[i]; p == "" || placed[p] {
				order = append(order, i)
				placed[ids[i]] = true
			} else {
				next = append(next, i)
			}
		}
		if len(next) == len(left) {
			return order, next
		}
		left = next
	}
	return order, nil
}

func (c *converter) project(ctx context.Context, pr Row, owner int) (*projectBundle, error) {
	pid := pr.str("id")
	b := &projectBundle{counts: map[string]int{}, skipped: map[string]int{}}
	b.Project = dgProject{ID: pid, OwnerID: owner, Name: pr.str("name"), TreeRev: pr.int("tree_rev", 0),
		CreatedAt: pr.ts("created_at", c.now), UpdatedAt: pr.ts("updated_at", c.now), UpdatedBy: c.userp(pr.strp("updated_by"))}
	b.add("project")

	if err := c.tree(ctx, b, pid); err != nil {
		return nil, err
	}
	fileIDs := map[string]bool{}
	for _, f := range b.Files {
		fileIDs[f.ID] = true
	}
	inProject := `file_id IN (SELECT id FROM files WHERE project_id = ?)`
	steps := []func() error{
		func() error { return c.versions(ctx, b, fileIDs, inProject, pid) },
		func() error { return c.members(ctx, b, fileIDs, pid) },
		func() error { return c.shares(ctx, b, fileIDs, pid) },
		func() error { return c.library(ctx, b, pid) },
		func() error { return c.private(ctx, b, fileIDs, inProject, pid) },
		func() error { return c.comments(ctx, b, fileIDs, inProject, pid) },
		func() error { return c.webhooks(ctx, b, pid) },
		func() error { return c.yjs(ctx, b, fileIDs, inProject, pid) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return b, c.notifications(ctx, b, fileIDs, pid)
}

func (c *converter) tree(ctx context.Context, b *projectBundle, pid string) error {
	rows, err := c.sel(ctx, "folders", "project_id = ?", pid)
	if err != nil {
		return err
	}
	ids, parents := make([]string, len(rows)), make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.str("id")
		if p := r.strp("parent_id"); p != nil {
			parents[i] = *p
		}
	}
	order, bad := treeOrder(ids, parents)
	isBad := map[int]bool{}
	for _, i := range bad {
		isBad[i] = true
		b.warn("folder", ids[i], "parent "+parents[i]+" not found or cyclic; moved to the project root")
	}
	order = append(order, bad...)
	folders := map[string]bool{}
	for _, i := range order {
		r := rows[i]
		f := dgFolder{ID: ids[i], ProjectID: pid, Name: r.str("name"), Position: r.f64("position"),
			CreatedAt: r.ts("created_at", c.now), UpdatedAt: r.ts("updated_at", c.now)}
		if parents[i] != "" && !isBad[i] {
			f.ParentID = &parents[i]
		}
		folders[f.ID] = true
		b.Folders = append(b.Folders, f)
		b.add("folder")
	}

	rows, err = c.sel(ctx, "files", "project_id = ?", pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		id := r.str("id")
		created, updated := r.ts("created_at", c.now), r.ts("updated_at", c.now)
		data, pages, kind, verr, ok := document(r.str("data"), id, r.str("name"), created, updated)
		if !ok {
			b.problem("file", id, "data is not storable JSON; file skipped")
			continue
		}
		if verr != nil {
			b.warn("file", id, "document invalid, raw data kept: "+verr.Error())
			pages = r.int("page_count", rawPages(data))
		}
		if old := r.str("kind"); diagram.ValidKind(old) {
			kind = old
		} else if kind == "" {
			kind = diagram.KindSDD
		}
		fk := r.str("file_kind")
		if !slices.Contains([]string{"general", "system", "db", "ood"}, fk) {
			fk = "general"
		}
		f := dgFile{ID: id, ProjectID: pid, Name: r.str("name"), Kind: kind, FileKind: fk, Position: r.f64("position"),
			Data: data, Version: r.int("version", 1), PageCount: max(pages, 1), ThumbnailHash: r.strp("thumbnail_hash"),
			CreatedAt: created, UpdatedAt: updated, UpdatedBy: c.userp(r.strp("updated_by"))}
		if fid := r.strp("folder_id"); fid != nil && *fid != "" {
			if folders[*fid] {
				f.FolderID = fid
			} else {
				b.warn("file", id, "folder "+*fid+" not found; moved to the project root")
			}
		}
		b.Files = append(b.Files, f)
		b.add("file")
	}
	return nil
}

func (c *converter) versions(ctx context.Context, b *projectBundle, files map[string]bool, where, pid string) error {
	rows, err := c.sel(ctx, "file_versions", where, pid)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, f := range b.Files {
		names[f.ID] = f.Name
	}
	type key struct {
		file string
		ver  int
	}
	seen := map[key]bool{}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].int("version", 0) < rows[j].int("version", 0) })
	for _, r := range rows {
		id, fid, ver := r.str("id"), r.str("file_id"), r.int("version", 0)
		switch {
		case !files[fid]:
			b.problem("version", id, "file "+fid+" not imported")
			continue
		case seen[key{fid, ver}]:
			b.problem("version", id, fmt.Sprintf("duplicate version %d of file %s", ver, fid))
			continue
		}
		seen[key{fid, ver}] = true
		created := r.ts("created_at", c.now)
		data, _, _, verr, ok := document(r.str("data"), fid, r.str("name"), created, created)
		if !ok {
			b.problem("version", id, "data is not storable JSON; version skipped")
			continue
		}
		if verr != nil {
			b.warn("version", id, "document invalid, raw data kept: "+verr.Error())
		}
		b.Versions = append(b.Versions, dgVersion{ID: id, FileID: fid, Version: ver, Name: r.str("name"), Data: data,
			Label: r.strp("label"), Named: r.flag("named", false), CreatedBy: c.userp(r.strp("created_by")), CreatedAt: created})
		b.add("version")
	}
	return nil
}

func (c *converter) members(ctx context.Context, b *projectBundle, files map[string]bool, pid string) error {
	rows, err := c.sel(ctx, "project_members", "project_id = ?", pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		uid, role := r.str("user_id"), r.str("role")
		n, ok := c.user(uid)
		switch {
		case !ok:
			b.problem("project member", pid+"/"+uid, "user has no account here")
		case !slices.Contains(roles, role):
			b.problem("project member", pid+"/"+uid, "unknown role "+role)
		case n == b.Project.OwnerID:
			b.problem("project member", pid+"/"+uid, "owner is not a member")
		default:
			b.Members = append(b.Members, dgMember{pid, n, role, r.ts("created_at", c.now)})
			b.add("project member")
		}
	}
	rows, err = c.sel(ctx, "file_members", `file_id IN (SELECT id FROM files WHERE project_id = ?)`, pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fid, uid, role := r.str("file_id"), r.str("user_id"), r.str("role")
		n, ok := c.user(uid)
		switch {
		case !files[fid]:
			b.problem("file member", fid+"/"+uid, "file not imported")
		case !ok:
			b.problem("file member", fid+"/"+uid, "user has no account here")
		case !slices.Contains(roles, role):
			b.problem("file member", fid+"/"+uid, "unknown role "+role)
		default:
			b.FileMembers = append(b.FileMembers, dgMember{fid, n, role, r.ts("created_at", c.now)})
			b.add("file member")
		}
	}
	return nil
}

// shares keeps token_hash as is: both servers store the lowercase hex
// sha256 of the token, so existing links keep working.
func (c *converter) shares(ctx context.Context, b *projectBundle, files map[string]bool, pid string) error {
	rows, err := c.sel(ctx, "share_links", "project_id = ?", pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		id, role, hash := r.str("id"), r.str("role"), r.str("token_hash")
		s := dgShare{ID: id, ProjectID: pid, TokenHash: hash, Role: role, ExpiresAt: r.tsp("expires_at"),
			RevokedAt: r.tsp("revoked_at"), CreatedBy: c.userp(r.strp("created_by")), CreatedAt: r.ts("created_at", c.now)}
		switch {
		case !diagram.ValidHash(hash):
			b.problem("share link", id, "token_hash is not a sha256 hex digest; the link cannot work")
			continue
		case !slices.Contains(roles, role):
			b.problem("share link", id, "unknown role "+role)
			continue
		}
		if fid := r.strp("file_id"); fid != nil && *fid != "" {
			if !files[*fid] {
				// NULL would widen the link to the whole project
				b.problem("share link", id, "file "+*fid+" not imported")
				continue
			}
			s.FileID = fid
		}
		b.Shares = append(b.Shares, s)
		b.add("share link")
	}
	return nil
}

func (c *converter) library(ctx context.Context, b *projectBundle, pid string) error {
	rows, err := c.sel(ctx, "project_library", "project_id = ?", pid)
	if err != nil || len(rows) == 0 {
		return err
	}
	r := rows[0]
	if !jsonOK([]byte(r.str("data"))) {
		b.problem("library", pid, "data is not storable JSON")
		return nil
	}
	b.Library = &dgLibrary{ProjectID: pid, Rev: max(r.int("rev", 1), 1), Data: json.RawMessage(r.str("data")),
		UpdatedAt: r.ts("updated_at", c.now), UpdatedBy: c.userp(r.strp("updated_by"))}
	b.add("library")
	return nil
}

func (c *converter) private(ctx context.Context, b *projectBundle, files map[string]bool, where, pid string) error {
	rows, err := c.sel(ctx, "file_private", where, pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fid, uid := r.str("file_id"), r.str("user_id")
		n, ok := c.user(uid)
		switch {
		case !files[fid] || !ok:
			b.problem("private stickies", fid+"/"+uid, "file not imported or user has no account here")
		case !jsonOK([]byte(r.str("data"))):
			b.problem("private stickies", fid+"/"+uid, "data is not storable JSON")
		default:
			b.Private = append(b.Private, dgPrivate{fid, n, json.RawMessage(r.str("data")), r.ts("updated_at", c.now)})
			b.add("private stickies")
		}
	}
	return nil
}

func (c *converter) comments(ctx context.Context, b *projectBundle, files map[string]bool, where, pid string) error {
	rows, err := c.sel(ctx, "comments", "project_id = ?", pid)
	if err != nil {
		return err
	}
	ids, parents := make([]string, len(rows)), make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.str("id")
		if p := r.strp("parent_id"); p != nil {
			parents[i] = *p
		}
	}
	order, bad := treeOrder(ids, parents)
	for _, i := range bad {
		b.problem("comment", ids[i], "parent "+parents[i]+" not found or cyclic")
	}
	done := map[string]bool{}
	for _, i := range order {
		r := rows[i]
		fid := r.str("file_id")
		if !files[fid] {
			b.problem("comment", ids[i], "file "+fid+" not imported")
			continue
		}
		if parents[i] != "" && !done[parents[i]] {
			b.problem("comment", ids[i], "parent comment was skipped")
			continue
		}
		cm := dgComment{ID: ids[i], FileID: fid, ProjectID: pid, AuthorID: c.userp(r.strp("author_id")), Body: r.str("body"),
			ResolvedAt: r.tsp("resolved_at"), ResolvedBy: c.userp(r.strp("resolved_by")),
			CreatedAt: r.ts("created_at", c.now), UpdatedAt: r.ts("updated_at", c.now)}
		if parents[i] != "" {
			cm.ParentID = &parents[i]
		}
		if a := r.str("anchor"); a != "" {
			if jsonOK([]byte(a)) {
				cm.Anchor = json.RawMessage(a)
			} else {
				b.warn("comment", ids[i], "anchor is not JSON; dropped")
			}
		}
		done[cm.ID] = true
		b.Comments = append(b.Comments, cm)
		b.add("comment")
	}

	rows, err = c.sel(ctx, "comment_mentions", `comment_id IN (SELECT id FROM comments WHERE project_id = ?)`, pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		cid, uid := r.str("comment_id"), r.str("user_id")
		if n, ok := c.user(uid); ok && done[cid] {
			b.Mentions = append(b.Mentions, dgMention{cid, n})
			b.add("comment mention")
		} else {
			b.problem("comment mention", cid+"/"+uid, "comment skipped or user has no account here")
		}
	}
	rows, err = c.sel(ctx, "comment_reads", where, pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fid, uid := r.str("file_id"), r.str("user_id")
		if n, ok := c.user(uid); ok && files[fid] {
			b.Reads = append(b.Reads, dgRead{fid, n, r.ts("read_at", c.now)})
			b.add("comment read")
		} else {
			b.problem("comment read", fid+"/"+uid, "file not imported or user has no account here")
		}
	}
	return nil
}

// notifications become the blog's diagram_* notifications.
func (c *converter) notifications(ctx context.Context, b *projectBundle, files map[string]bool, pid string) error {
	rows, err := c.sel(ctx, "notifications", "project_id = ?", pid)
	if err != nil {
		return err
	}
	comments := map[string]bool{}
	for _, cm := range b.Comments {
		comments[cm.ID] = true
	}
	for _, r := range rows {
		id, kind, fid := r.str("id"), r.str("kind"), r.str("file_id")
		n, ok := c.user(r.str("user_id"))
		switch {
		case !ok:
			b.problem("notification", id, "user has no account here")
			continue
		case !slices.Contains([]string{"mention", "reply", "comment"}, kind):
			b.problem("notification", id, "unknown kind "+kind)
			continue
		case !files[fid]:
			b.problem("notification", id, "file "+fid+" not imported")
			continue
		}
		nt := dgNotification{UserID: n, Type: "diagram_" + kind, ActorID: c.userp(r.strp("actor_id")), ProjectID: pid, FileID: fid,
			Text: r.str("text"), ReadAt: r.tsp("read_at"), CreatedAt: r.ts("created_at", c.now)}
		if cid := r.strp("comment_id"); cid != nil && comments[*cid] {
			nt.CommentID = cid
		}
		b.Notifs = append(b.Notifs, nt)
		b.add("notification")
	}
	return nil
}

func (c *converter) webhooks(ctx context.Context, b *projectBundle, pid string) error {
	rows, err := c.sel(ctx, "webhooks", "project_id = ?", pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		id := r.str("id")
		var evs []string
		if json.Unmarshal([]byte(r.str("events")), &evs) != nil {
			b.problem("webhook", id, "events is not a JSON array")
			continue
		}
		var keep []string
		for _, e := range evs {
			if slices.Contains(diagram.WebhookEvents, e) {
				keep = append(keep, e)
			} else {
				b.warn("webhook", id, "unknown event "+e+" dropped")
			}
		}
		if keep == nil {
			keep = []string{}
		}
		// secrets are plain text on both sides: deliveries stay verifiable
		b.Webhooks = append(b.Webhooks, dgWebhook{ID: id, ProjectID: pid, URL: r.str("url"), Secret: r.str("secret"), Events: keep,
			Active: r.flag("active", true), CreatedBy: c.userp(r.strp("created_by")), CreatedAt: r.ts("created_at", c.now)})
		b.add("webhook")
	}
	return nil
}

func b64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
}

func (c *converter) yjs(ctx context.Context, b *projectBundle, files map[string]bool, where, pid string) error {
	rows, err := c.sel(ctx, "file_ydocs", where, pid)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fid := r.str("file_id")
		snap, err := b64(r.str("snapshot"))
		if !files[fid] || err != nil {
			b.problem("yjs snapshot", fid, "file not imported or snapshot is not base64")
			continue
		}
		seq, _ := r.i64("seq")
		b.YDocs = append(b.YDocs, dgYDoc{fid, snap, seq, r.ts("updated_at", c.now)})
		b.add("yjs snapshot")
	}
	rows, err = c.sel(ctx, "file_yupdates", where, pid)
	if err != nil {
		return err
	}
	sort.SliceStable(rows, func(i, j int) bool { a, _ := rows[i].i64("seq"); z, _ := rows[j].i64("seq"); return a < z })
	for _, r := range rows {
		fid := r.str("file_id")
		seq, _ := r.i64("seq")
		data, err := b64(r.str("data"))
		if !files[fid] || err != nil {
			b.problem("yjs update", fmt.Sprintf("%s#%d", fid, seq), "file not imported or data is not base64")
			continue
		}
		b.YUpdates = append(b.YUpdates, dgYUpdate{fid, seq, data, r.ts("created_at", c.now)})
		b.add("yjs update")
	}
	return nil
}
