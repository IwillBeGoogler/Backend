package postgres_test

// Coverage of the diagram repository's publish/engagement, integrations,
// admin/templates, comments and sharing parts, by the fault-injection sweep
// of cov_diagram_integration_test.go. Skipped with -short or without Docker.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

func TestCovDiagramMore(t *testing.T) {
	db := connect(t)
	owner, other := covDiaUsers(t, db)

	t.Run("publish and engagement", func(t *testing.T) {
		t.Parallel()
		covDiaSweep(t, db, "pub", func(ctx context.Context, tag string, p *pgxpool.Pool) error {
			w, err := covDiaBuild(ctx, p, tag, owner, other)
			if err != nil {
				return err
			}
			r := w.r
			slug, slug2, tg := "s-"+tag, "u-"+tag, "tg"+tag
			pub := diagram.Publication{Visibility: diagram.VisPublic, Slug: slug, Title: "T " + tag, Description: "d", Tags: []string{tg}, PublishedAt: w.now}
			return covDiaRun(
				func() error { return r.SetPublication(ctx, w.fa, pub) },
				func() error { return covDiaIs(r.SetPublication(ctx, w.missing, pub), domain.ErrNotFound) },
				func() error { return covDiaIs(r.SetPublication(ctx, w.fb, pub), domain.ErrConflict) },
				func() error {
					// nil tags are stored as an empty list
					return r.SetPublication(ctx, w.fb, diagram.Publication{Visibility: diagram.VisUnlisted, Slug: slug2, Title: "U", PublishedAt: w.now})
				},
				func() error {
					l, err := r.Listing(ctx, w.fb)
					return covDiaChk(err, l.Visibility == diagram.VisUnlisted && l.Tags != nil && len(l.Tags) == 0 && l.AuthorUsername == "covdia_owner", "listing of unlisted")
				},
				func() error {
					l, err := r.Listing(ctx, w.fa)
					return covDiaChk(err, l.Slug == slug && !l.PublishedAt.IsZero() && len(l.Tags) == 1, "listing of public")
				},
				func() error {
					// a private file has no published_at: the zero time
					if err := r.ClearPublication(ctx, w.fb); err != nil {
						return err
					}
					l, err := r.Listing(ctx, w.fb)
					return covDiaChk(err, l.Visibility == diagram.VisPrivate, "cleared")
				},
				func() error { _, err := r.Listing(ctx, w.missing); return covDiaIs(err, domain.ErrNotFound) },
				func() error { return covDiaIs(r.ClearPublication(ctx, w.missing), domain.ErrNotFound) },
				func() error {
					d, err := r.PublicBySlug(ctx, slug)
					return covDiaChk(err, d.FileID == w.fa && len(d.Data) > 0, "public by slug")
				},
				func() error { _, err := r.PublicBySlug(ctx, "nope-"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					ls, total, err := r.ListPublic(ctx, diagram.PublicQuery{Q: "t " + tag, Tag: tg, AuthorID: owner, Page: 1, Limit: 10})
					return covDiaChk(err, len(ls) == 1 && total == 1 && ls[0].FileID == w.fa, "list public by tag")
				},
				func() error {
					ls, total, err := r.ListPublic(ctx, diagram.PublicQuery{Tag: "zz" + tag, Limit: 10})
					return covDiaChk(err, len(ls) == 0 && total == 0, "list public no match")
				},
				func() error {
					ls, total, err := r.ListOwned(ctx, owner, 1, 100)
					return covDiaChk(err, len(ls) >= 1 && total >= 1, "list owned")
				},
				func() error {
					m, err := r.ReferencedPublic(ctx, nil)
					return covDiaChk(err, len(m) == 0, "no refs")
				},
				func() error {
					m, err := r.ReferencedPublic(ctx, []string{w.fa, slug, "", "nope-" + tag})
					return covDiaChk(err, len(m) == 2 && m[w.fa].FileID == w.fa && m[slug].FileID == w.fa, "referenced by id and slug")
				},
				func() error {
					l, err := r.PublicListing(ctx, slug)
					return covDiaChk(err, l.FileID == w.fa, "public listing")
				},
				func() error { _, err := r.PublicListing(ctx, "nope-"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					total, mine, created, err := r.Clap(ctx, w.fa, other, 3)
					return covDiaChk(err, total == 3 && mine == 3 && created, "first clap")
				},
				func() error {
					total, mine, created, err := r.Clap(ctx, w.fa, other, 60)
					return covDiaChk(err, total == 50 && mine == 50 && !created, "claps are capped")
				},
				func() error { _, _, _, err := r.Clap(ctx, w.fb, other, 1); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					_, _, _, err := r.Clap(ctx, w.missing, other, 1)
					return covDiaIs(err, domain.ErrNotFound)
				},
				func() error { _, _, _, err := r.Clap(ctx, w.fa, -1, 1); return covDiaIs(err, domain.ErrNotFound) },
				func() error { return r.Bookmark(ctx, w.fa, other) },
				func() error { return r.Bookmark(ctx, w.fa, other) },
				func() error { return covDiaIs(r.Bookmark(ctx, w.fb, other), domain.ErrNotFound) },
				func() error {
					v, err := r.Viewer(ctx, w.fa, other)
					return covDiaChk(err, v.MyClaps == 50 && v.Bookmarked, "viewer")
				},
				func() error {
					ls, total, err := r.ListBookmarked(ctx, other, 1, 10)
					return covDiaChk(err, len(ls) >= 1 && total >= 1, "bookmarked")
				},
				func() error { return r.Unbookmark(ctx, w.fa, other) },
				func() error {
					v, err := r.Viewer(ctx, w.fa, other)
					return covDiaChk(err, !v.Bookmarked, "unbookmarked")
				},
			)
		})
	})

	t.Run("integrations", func(t *testing.T) {
		t.Parallel()
		covDiaSweep(t, db, "int", func(ctx context.Context, tag string, p *pgxpool.Pool) error {
			w, err := covDiaBuild(ctx, p, tag, owner, other)
			if err != nil {
				return err
			}
			r := w.r
			later := w.now.Add(time.Minute)
			tok := diagram.AccessToken{ID: "tok_" + tag, UserID: owner, Name: "n", Hash: "h" + tag, Prefix: "sdd_pat_ab",
				Scopes: []string{"read"}, ProjectIDs: []string{w.pid}, ExpiresAt: &later, CreatedAt: w.now}
			h1 := diagram.Webhook{ID: "whk_" + tag, ProjectID: w.pid, URL: "https://example.com/h", Secret: "whsec_x", Events: []string{"file.saved"},
				Active: true, CreatedBy: owner, CreatedAt: w.now}
			h2 := h1
			h2.ID, h2.Active, h2.CreatedBy, h2.CreatedAt = "whk_"+tag+"2", false, 0, later
			code := 200
			d1 := diagram.Delivery{ID: "dlv_" + tag, WebhookID: h1.ID, Event: "file.saved", Payload: "{}", Status: "pending", NextAttemptAt: &later, CreatedAt: w.now}
			return covDiaRun(
				func() error { return r.CreateToken(ctx, tok) },
				func() error {
					ts, err := r.Tokens(ctx, owner)
					return covDiaChk(err, len(ts) >= 1, "tokens")
				},
				func() error {
					t, err := r.TokenByHash(ctx, tok.Hash)
					return covDiaChk(err, t.ID == tok.ID && len(t.Scopes) == 1, "token by hash")
				},
				func() error { _, err := r.TokenByHash(ctx, "nope"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error { return r.TouchToken(ctx, tok.ID, later.Add(time.Hour)) },
				func() error { return r.RevokeToken(ctx, owner, tok.ID, later) },
				func() error { return covDiaIs(r.RevokeToken(ctx, owner, tok.ID, later), domain.ErrNotFound) },
				func() error { return r.CreateWebhook(ctx, h1) },
				func() error { return r.CreateWebhook(ctx, h2) },
				func() error { hs, err := r.Webhooks(ctx, w.pid); return covDiaChk(err, len(hs) == 2, "webhooks") },
				func() error {
					hs, err := r.ActiveWebhooks(ctx, w.pid)
					return covDiaChk(err, len(hs) == 1 && hs[0].ID == h1.ID, "active webhooks")
				},
				func() error {
					h, err := r.Webhook(ctx, w.pid, h1.ID)
					return covDiaChk(err, h.URL == h1.URL, "webhook")
				},
				func() error { _, err := r.Webhook(ctx, w.pid, "nope"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					h, err := r.WebhookByID(ctx, h2.ID)
					return covDiaChk(err, h.CreatedBy == 0, "webhook by id")
				},
				func() error { _, err := r.WebhookByID(ctx, "nope"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					h1.URL = "https://example.com/other"
					return r.UpdateWebhook(ctx, h1)
				},
				func() error {
					m := h1
					m.ID = "nope" + tag
					return covDiaIs(r.UpdateWebhook(ctx, m), domain.ErrNotFound)
				},
				func() error { return r.CreateDelivery(ctx, d1) },
				func() error {
					d, err := r.Delivery(ctx, d1.ID)
					return covDiaChk(err, d.Status == "pending" && d.Error == "", "delivery")
				},
				func() error { _, err := r.Delivery(ctx, "nope"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					d1.Status, d1.Attempt, d1.ResponseCode, d1.Error, d1.NextAttemptAt = "failed", 1, &code, "boom", nil
					return r.UpdateDelivery(ctx, d1)
				},
				func() error {
					m := d1
					m.ID = "nope" + tag
					return covDiaIs(r.UpdateDelivery(ctx, m), domain.ErrNotFound)
				},
				func() error {
					ds, err := r.Deliveries(ctx, h1.ID, 10)
					return covDiaChk(err, len(ds) == 1 && ds[0].Error == "boom" && *ds[0].ResponseCode == 200, "deliveries")
				},
				func() error {
					return r.AddAudit(ctx, diagram.MCPAudit{ID: "aud_" + tag, UserID: owner, TokenID: "tok", Tool: "t", ProjectID: w.pid, FileID: w.fa,
						Status: "ok", Detail: "d", DurationMS: 5, CreatedAt: later})
				},
				func() error {
					return r.AddAudit(ctx, diagram.MCPAudit{ID: "aud_" + tag + "2", UserID: owner, Tool: "t", Status: "denied", CreatedAt: w.now})
				},
				func() error {
					as, err := r.Audit(ctx, owner, 1000)
					return covDiaChk(err, len(as) >= 2, "audit")
				},
				func() error { return r.DeleteWebhook(ctx, h1.ID) },
				func() error { return covDiaIs(r.DeleteWebhook(ctx, h1.ID), domain.ErrNotFound) },
			)
		})
	})

	t.Run("admin and templates", func(t *testing.T) {
		t.Parallel()
		covDiaSweep(t, db, "adm", func(ctx context.Context, tag string, p *pgxpool.Pool) error {
			w, err := covDiaBuild(ctx, p, tag, owner, other)
			if err != nil {
				return err
			}
			r, tr := w.r, postgres.NewDiagramTemplateRepo(p)
			later := w.now.Add(time.Minute)
			share := diagram.ShareLink{ID: "shr_" + tag, ProjectID: w.pid, Role: diagram.RoleViewer, CreatedBy: owner, CreatedAt: w.now, TokenHash: "th" + tag}
			t1 := diagram.Template{ID: "tpl_" + tag, Title: "T" + tag, Kind: diagram.TemplateSystem, Description: "d", FileID: w.fa, Published: true, CreatedAt: w.now, UpdatedAt: w.now}
			t2 := t1
			t2.ID, t2.Title, t2.FileID, t2.Published, t2.Position = "tpl_"+tag+"2", "A"+tag, w.fb, false, 1
			return covDiaRun(
				func() error { return r.SetMember(ctx, w.pid, other, diagram.RoleViewer, w.now) },
				func() error { return r.CreateShare(ctx, share) },
				func() error {
					st, err := r.Stats(ctx, w.now.Add(-7*24*time.Hour))
					return covDiaChk(err, st.Projects >= 1 && st.Files >= 2 && st.FilesByKind["sdd"] >= 2 && st.ActiveEditors7d >= 1, "stats")
				},
				func() error {
					ps, err := r.Projects(ctx, diagram.AdminProjectQuery{Search: "<" + tag + ">%", Owner: "COVDIA_OWNER", Limit: 5})
					return covDiaChk(err, len(ps) == 0, "search escapes wildcards")
				},
				func() error {
					ps, err := r.Projects(ctx, diagram.AdminProjectQuery{Search: "<" + tag + ">", Owner: "COVDIA_OWNER", Limit: 5})
					return covDiaChk(err, len(ps) == 1 && ps[0].Files == 2 && ps[0].Shared && ps[0].Owner.Username == "covdia_owner", "projects")
				},
				func() error {
					ps, err := r.Projects(ctx, diagram.AdminProjectQuery{Owner: "nobody", Limit: 5, Offset: 1})
					return covDiaChk(err, len(ps) == 0, "projects of nobody")
				},
				func() error {
					d, err := r.ProjectDetail(ctx, w.pid)
					return covDiaChk(err, len(d.FileList) == 2 && len(d.Members) == 1 && len(d.Shares) == 1 && d.Shares[0].CreatedBy == "covdia_owner", "detail")
				},
				func() error { _, err := r.ProjectDetail(ctx, w.missing); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					pid, err := r.RevokeShareByID(ctx, share.ID, later)
					return covDiaChk(err, pid == w.pid, "revoke by id")
				},
				func() error {
					_, err := r.RevokeShareByID(ctx, "nope"+tag, later)
					return covDiaIs(err, domain.ErrNotFound)
				},

				func() error { return tr.Create(ctx, t1) },
				func() error { return tr.Create(ctx, t2) },
				func() error { return covDiaIs(tr.Create(ctx, t1), domain.ErrConflict) },
				func() error {
					m := t1
					m.ID, m.FileID = "tpl_"+tag+"3", w.missing
					return covDiaIs(tr.Create(ctx, m), domain.ErrConflict)
				},
				func() error { ts, err := tr.List(ctx, false); return covDiaChk(err, len(ts) >= 2, "list all") },
				func() error {
					ts, err := tr.List(ctx, true)
					for _, x := range ts {
						if !x.Published {
							return covDiaChk(err, false, "unpublished in published list")
						}
					}
					return covDiaChk(err, len(ts) >= 1, "list published")
				},
				func() error {
					x, err := tr.Get(ctx, t1.ID)
					return covDiaChk(err, x.Title == t1.Title && x.PageCount == 1, "get")
				},
				func() error { _, err := tr.Get(ctx, "nope"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					t1.Title, t1.UpdatedAt = "renamed"+tag, later
					return tr.Update(ctx, t1)
				},
				func() error {
					m := t1
					m.ID = "nope" + tag
					return covDiaIs(tr.Update(ctx, m), domain.ErrNotFound)
				},
				func() error {
					m := t1
					m.FileID = w.missing
					return covDiaIs(tr.Update(ctx, m), domain.ErrConflict)
				},
				func() error {
					// unknown ids fail; duplicates and omitted ones are tolerated
					return covDiaIs(tr.Reorder(ctx, []string{t2.ID, "nope" + tag}, later), domain.ErrNotFound)
				},
				func() error { return tr.Reorder(ctx, []string{t2.ID, t2.ID, t1.ID}, later) },
				func() error { return tr.Reorder(ctx, []string{t2.ID}, later) },
				func() error {
					x, err := tr.Get(ctx, t2.ID)
					return covDiaChk(err, x.Position == 0, "reordered first")
				},
				func() error { return tr.Delete(ctx, t1.ID) },
				func() error { return covDiaIs(tr.Delete(ctx, t1.ID), domain.ErrNotFound) },
				func() error { return tr.Delete(ctx, t2.ID) },
			)
		})
	})

	t.Run("comments", func(t *testing.T) {
		t.Parallel()
		covDiaSweep(t, db, "cmt", func(ctx context.Context, tag string, p *pgxpool.Pool) error {
			w, err := covDiaBuild(ctx, p, tag, owner, other)
			if err != nil {
				return err
			}
			r := w.r
			later := w.now.Add(time.Minute)
			rootID := "cmt_" + tag
			root := diagram.Comment{ID: rootID, FileID: w.fa, ProjectID: w.pid, AuthorID: owner, Body: "hi",
				Anchor: json.RawMessage(`{"type":"point","x":1,"y":2}`), Mentions: []int{other, other}, CreatedAt: w.now, UpdatedAt: w.now}
			reply := diagram.Comment{ID: rootID + "r", FileID: w.fa, ProjectID: w.pid, ParentID: &rootID, Body: "re", CreatedAt: later, UpdatedAt: later}
			return covDiaRun(
				func() error { return r.InsertComment(ctx, root) },
				func() error { return r.InsertComment(ctx, reply) },
				func() error { return covDiaIs(r.InsertComment(ctx, root), domain.ErrConflict) },
				func() error {
					m := root
					m.ID, m.FileID = rootID+"x", w.missing
					return covDiaIs(r.InsertComment(ctx, m), domain.ErrNotFound)
				},
				func() error {
					c, err := r.Comment(ctx, rootID)
					return covDiaChk(err, c.Body == "hi" && len(c.Mentions) == 1 && c.Mentions[0] == other && len(c.Anchor) > 0, "comment")
				},
				func() error { _, err := r.Comment(ctx, "nope"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					cs, err := r.Comments(ctx, w.fa)
					return covDiaChk(err, len(cs) == 2 && cs[0].ID == rootID && cs[1].AuthorID == 0 && len(cs[1].Mentions) == 0, "comments")
				},
				func() error { cs, err := r.Comments(ctx, w.fb); return covDiaChk(err, len(cs) == 0, "no comments") },
				func() error {
					c := root
					c.Body, c.UpdatedAt = "edited", later
					return r.UpdateComment(ctx, c, true, false)
				},
				func() error {
					c := root
					c.ResolvedAt, c.ResolvedBy, c.UpdatedAt = &later, owner, later
					return r.UpdateComment(ctx, c, false, true)
				},
				func() error {
					c, err := r.Comment(ctx, rootID)
					return covDiaChk(err, c.Body == "edited" && c.ResolvedAt != nil && c.ResolvedBy == owner, "patched parts are independent")
				},
				func() error {
					c := root
					c.ID = "nope" + tag
					return covDiaIs(r.UpdateComment(ctx, c, true, true), domain.ErrNotFound)
				},
				func() error { at, err := r.ReadAt(ctx, w.fa, owner); return covDiaChk(err, at.IsZero(), "never read") },
				func() error { return r.MarkRead(ctx, w.fa, owner, w.now) },
				func() error { return r.MarkRead(ctx, w.fa, owner, later) },
				func() error { at, err := r.ReadAt(ctx, w.fa, owner); return covDiaChk(err, at.Equal(later), "read at") },
				func() error { return covDiaIs(r.MarkRead(ctx, w.missing, owner, later), domain.ErrNotFound) },
				func() error {
					ids, err := r.DeleteComment(ctx, rootID)
					return covDiaChk(err, len(ids) == 2 && ids[0] == rootID, "delete takes the replies")
				},
				func() error { _, err := r.DeleteComment(ctx, rootID); return covDiaIs(err, domain.ErrNotFound) },
			)
		})
	})

	t.Run("sharing", func(t *testing.T) {
		t.Parallel()
		covDiaSweep(t, db, "shr", func(ctx context.Context, tag string, p *pgxpool.Pool) error {
			w, err := covDiaBuild(ctx, p, tag, owner, other)
			if err != nil {
				return err
			}
			r := w.r
			exp := w.now.Add(time.Hour)
			s1 := diagram.ShareLink{ID: "shr_" + tag, ProjectID: w.pid, Role: diagram.RoleViewer, ExpiresAt: &exp, CreatedBy: owner, CreatedAt: w.now, TokenHash: "th" + tag}
			s2 := diagram.ShareLink{ID: "shr_" + tag + "2", ProjectID: w.pid, FileID: &w.fa, Role: diagram.RoleEditor, CreatedAt: w.now, TokenHash: "th2" + tag}
			sum := sha256.Sum256([]byte(tag))
			hash := hex.EncodeToString(sum[:])
			asset := diagram.Asset{Hash: hash, Mime: "image/png", Size: 3, UploadedBy: owner, CreatedAt: w.now}
			lib1, lib2 := json.RawMessage(`{"items":[1]}`), json.RawMessage(`{"items":[1,2]}`)
			sticky := json.RawMessage(`{"nodes":[],"edges":[]}`)
			return covDiaRun(
				func() error { ms, err := r.Members(ctx, w.pid); return covDiaChk(err, len(ms) == 0, "no members") },
				func() error { return r.SetMember(ctx, w.pid, other, diagram.RoleViewer, w.now) },
				func() error { return r.SetMember(ctx, w.pid, other, diagram.RoleEditor, w.now) },
				func() error {
					return covDiaIs(r.SetMember(ctx, w.pid, -1, diagram.RoleViewer, w.now), domain.ErrNotFound)
				},
				func() error {
					ms, err := r.Members(ctx, w.pid)
					return covDiaChk(err, len(ms) == 1 && ms[0].UserID == other && ms[0].Role == diagram.RoleEditor, "members")
				},
				func() error {
					ok, err := r.UpdateMember(ctx, w.pid, other, diagram.RoleViewer)
					return covDiaChk(err, ok, "update member")
				},
				func() error {
					ok, err := r.UpdateMember(ctx, w.pid, owner, diagram.RoleViewer)
					return covDiaChk(err, !ok, "update non-member")
				},
				func() error {
					ms, err := r.FileMembers(ctx, w.fa)
					return covDiaChk(err, len(ms) == 0, "no file members")
				},
				func() error { return r.SetFileMember(ctx, w.fa, other, diagram.RoleViewer, w.now) },
				func() error { return r.SetFileMember(ctx, w.fa, other, diagram.RoleEditor, w.now) },
				func() error {
					return covDiaIs(r.SetFileMember(ctx, w.missing, other, diagram.RoleViewer, w.now), domain.ErrNotFound)
				},
				func() error {
					ms, err := r.FileMembers(ctx, w.fa)
					return covDiaChk(err, len(ms) == 1 && ms[0].Role == diagram.RoleEditor, "file members")
				},
				func() error {
					role, err := r.FileRole(ctx, w.fa, other)
					return covDiaChk(err, role == diagram.RoleEditor, "file role")
				},
				func() error {
					ok, err := r.RemoveFileMember(ctx, w.fa, other)
					return covDiaChk(err, ok, "remove file member")
				},
				func() error {
					ok, err := r.RemoveFileMember(ctx, w.fa, other)
					return covDiaChk(err, !ok, "remove again")
				},
				func() error { return r.SetFileMember(ctx, w.fa, other, diagram.RoleViewer, w.now) },
				func() error { ok, err := r.RemoveMember(ctx, w.pid, other); return covDiaChk(err, ok, "remove member") },
				func() error {
					ms, err := r.FileMembers(ctx, w.fa)
					return covDiaChk(err, len(ms) == 0, "removing a member drops the file overrides")
				},
				func() error {
					ok, err := r.RemoveMember(ctx, w.pid, other)
					return covDiaChk(err, !ok, "remove member again")
				},
				func() error { return r.CreateShare(ctx, s1) },
				func() error { return r.CreateShare(ctx, s2) },
				func() error { return covDiaIs(r.CreateShare(ctx, s1), domain.ErrConflict) },
				func() error {
					ss, err := r.Shares(ctx, w.pid)
					return covDiaChk(err, len(ss) == 2, "shares")
				},
				func() error { ok, err := r.RevokeShare(ctx, w.pid, s2.ID, w.now); return covDiaChk(err, ok, "revoke") },
				func() error {
					ok, err := r.RevokeShare(ctx, w.pid, "nope"+tag, w.now)
					return covDiaChk(err, !ok, "revoke missing")
				},
				func() error {
					ss, err := r.SharesExpiredBetween(ctx, w.now, w.now.Add(2*time.Hour))
					found := false
					for _, s := range ss {
						found = found || s.ID == s1.ID
					}
					return covDiaChk(err, found, "expired between")
				},
				func() error {
					ss, err := r.SharesExpiredBetween(ctx, w.now.Add(2*time.Hour), w.now.Add(3*time.Hour))
					return covDiaChk(err, len(ss) == 0, "none expire later")
				},
				func() error {
					s, err := r.ShareByHash(ctx, s2.TokenHash)
					return covDiaChk(err, s.ID == s2.ID && s.FileID != nil && s.RevokedAt != nil, "share by hash")
				},
				func() error { _, err := r.ShareByHash(ctx, "nope"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error { ok, err := r.InsertAsset(ctx, asset); return covDiaChk(err, ok, "insert asset") },
				func() error { ok, err := r.InsertAsset(ctx, asset); return covDiaChk(err, !ok, "insert asset again") },
				func() error {
					a, err := r.Asset(ctx, hash)
					return covDiaChk(err, a.Size == 3 && a.UploadedBy == owner, "asset")
				},
				func() error { _, err := r.Asset(ctx, "nope"+tag); return covDiaIs(err, domain.ErrNotFound) },
				func() error { return r.SetThumbnail(ctx, w.fa, hash) },
				func() error { return covDiaIs(r.SetThumbnail(ctx, w.missing, hash), domain.ErrNotFound) },
				func() error { l, err := r.Library(ctx, w.pid); return covDiaChk(err, l.Rev == 0, "no library") },
				func() error {
					ok, err := r.SaveLibrary(ctx, w.pid, 0, lib1, owner, w.now)
					return covDiaChk(err, ok, "first save")
				},
				func() error {
					ok, err := r.SaveLibrary(ctx, w.pid, 0, lib1, owner, w.now)
					return covDiaChk(err, !ok, "loses to the first save")
				},
				func() error {
					ok, err := r.SaveLibrary(ctx, w.pid, 1, lib2, 0, w.now)
					return covDiaChk(err, ok, "second save")
				},
				func() error {
					ok, err := r.SaveLibrary(ctx, w.pid, 1, lib2, owner, w.now)
					return covDiaChk(err, !ok, "stale save")
				},
				func() error {
					_, err := r.SaveLibrary(ctx, w.missing, 0, lib1, owner, w.now)
					return covDiaIs(err, domain.ErrNotFound)
				},
				func() error { l, err := r.Library(ctx, w.pid); return covDiaChk(err, l.Rev == 2, "library rev") },
				func() error { _, err := r.Private(ctx, w.fa, other); return covDiaIs(err, domain.ErrNotFound) },
				func() error { return r.PutPrivate(ctx, w.fa, other, sticky, w.now) },
				func() error { return r.PutPrivate(ctx, w.fa, other, sticky, w.now.Add(time.Second)) },
				func() error { return covDiaIs(r.PutPrivate(ctx, w.missing, other, sticky, w.now), domain.ErrNotFound) },
				func() error {
					pr, err := r.Private(ctx, w.fa, other)
					return covDiaChk(err, len(pr.Data) > 0 && !pr.UpdatedAt.IsZero(), "private")
				},
				func() error { return r.DeletePrivate(ctx, w.fa, other) },
				func() error { _, err := r.Private(ctx, w.fa, other); return covDiaIs(err, domain.ErrNotFound) },
			)
		})
	})
}
