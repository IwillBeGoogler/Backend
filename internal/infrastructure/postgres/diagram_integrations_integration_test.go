package postgres_test

// Access tokens, webhooks with their deliveries, the MCP audit trail and
// the owner's published list against a real Postgres, with the cascades
// that keep them from outliving their user or project. Skipped with
// -short or without Docker.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

func TestDiagramIntegrationsRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	run := strconv.FormatInt(time.Now().UnixNano()%1e9, 36) // a reused database keeps earlier runs' accounts
	account := func(name string) user.User {
		u, err := users.Create(ctx, "dgi_"+name+run, "dgi_"+name+run+"@example.com")
		must(t, err)
		return u
	}
	ali, bob := account("ali"), account("bob")
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Users, d.Blobs = users, &blobStore{data: map[string][]byte{}}
	made, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Integrations", Data: dgDoc("a")})
	must(t, err)
	pid, fid := made.Project.ID, made.File.ID

	t.Run("tokens: only the hash is stored, and listing, lookup, touch and revoke", func(t *testing.T) {
		secret, tok, err := d.CreateToken(ctx, ali.ID, application.NewAccessToken{Name: "ci", Scopes: []string{"files:read", "projects:read"},
			ExpiresInDays: 7, ProjectIDs: []string{pid}})
		must(t, err)
		var stored string
		must(t, db.QueryRow(ctx, `SELECT token_hash FROM dg_access_tokens WHERE id = $1`, tok.ID).Scan(&stored))
		if stored == secret || stored != diagram.HashToken(secret) {
			t.Fatalf("stored hash = %q", stored)
		}
		var leaked int
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM dg_access_tokens WHERE prefix || name || token_hash LIKE '%' || $1 || '%'`, secret).Scan(&leaked))
		if leaked != 0 {
			t.Error("the secret is in the table")
		}

		got, err := d.AuthenticateToken(ctx, secret)
		must(t, err)
		if got.UserID != ali.ID || got.Prefix != "sdd_pat_" || len(got.Scopes) != 2 || len(got.ProjectIDs) != 1 || got.ProjectIDs[0] != pid ||
			got.ExpiresAt == nil || got.ExpiresAt.Before(time.Now().Add(6*24*time.Hour)) {
			t.Errorf("token = %+v", got)
		}
		again, _ := repo.TokenByHash(ctx, diagram.HashToken(secret))
		if again.LastUsedAt == nil {
			t.Error("last_used_at was not set")
		}
		first := *again.LastUsedAt
		must(t, repo.TouchToken(ctx, tok.ID, first.Add(10*time.Second)))
		if again, _ = repo.TokenByHash(ctx, diagram.HashToken(secret)); !again.LastUsedAt.Equal(first) {
			t.Error("a touch within the minute moved last_used_at")
		}
		must(t, repo.TouchToken(ctx, tok.ID, first.Add(2*time.Minute)))
		if again, _ = repo.TokenByHash(ctx, diagram.HashToken(secret)); !again.LastUsedAt.After(first) {
			t.Error("a touch after a minute kept last_used_at")
		}

		// an unrestricted token has no project list
		_, open, err := d.CreateToken(ctx, ali.ID, application.NewAccessToken{Name: "all", Scopes: []string{"files:read"}})
		must(t, err)
		if list, _ := repo.Tokens(ctx, ali.ID); len(list) < 2 || list[0].ID != open.ID || list[0].ProjectIDs != nil {
			t.Errorf("tokens = %+v", list)
		}
		if _, err := d.AuthenticateToken(ctx, secret+"x"); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("wrong secret: %v", err)
		}

		if err := d.RevokeToken(ctx, bob.ID, tok.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("revoked by someone else: %v", err)
		}
		must(t, d.RevokeToken(ctx, ali.ID, tok.ID))
		if err := d.RevokeToken(ctx, ali.ID, tok.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("second revoke: %v", err)
		}
		if _, err := d.AuthenticateToken(ctx, secret); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("revoked token: %v", err)
		}
		for _, x := range must1(repo.Tokens(ctx, ali.ID)) {
			if x.ID == tok.ID {
				t.Error("a revoked token is listed")
			}
		}
	})

	t.Run("tokens go with their user", func(t *testing.T) {
		gone := account("gone")
		secret, _, err := d.CreateToken(ctx, gone.ID, application.NewAccessToken{Name: "x", Scopes: []string{"files:read"}})
		must(t, err)
		_, err = db.Exec(ctx, `DELETE FROM users WHERE id = $1`, gone.ID)
		must(t, err)
		if _, err := d.AuthenticateToken(ctx, secret); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("token of a deleted user: %v", err)
		}
	})

	t.Run("webhooks and deliveries", func(t *testing.T) {
		w := diagram.Webhook{ID: diagram.NewID(diagram.PrefixWebhook), ProjectID: pid, URL: "https://hooks.example.com/in", Secret: diagram.NewWebhookSecret(),
			Events: []string{"file.updated", "file.created"}, Active: true, CreatedBy: ali.ID, CreatedAt: time.Now().Truncate(time.Millisecond)}
		must(t, repo.CreateWebhook(ctx, w))
		off := w
		off.ID, off.Active, off.CreatedAt = diagram.NewID(diagram.PrefixWebhook), false, w.CreatedAt.Add(time.Second)
		must(t, repo.CreateWebhook(ctx, off))

		got, err := repo.Webhook(ctx, pid, w.ID)
		must(t, err)
		if got.URL != w.URL || got.Secret != w.Secret || len(got.Events) != 2 || !got.Active || got.CreatedBy != ali.ID {
			t.Errorf("webhook = %+v", got)
		}
		if _, err := repo.Webhook(ctx, "prj_other", w.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("a webhook through another project: %v", err)
		}
		list := must1(repo.Webhooks(ctx, pid))
		if len(list) != 2 || list[0].ID != off.ID {
			t.Errorf("newest first: %v", list)
		}
		if act := must1(repo.ActiveWebhooks(ctx, pid)); len(act) != 1 || act[0].ID != w.ID {
			t.Errorf("active = %v", act)
		}
		w.URL, w.Events, w.Active = "https://hooks.example.com/other", []string{"library.updated"}, false
		must(t, repo.UpdateWebhook(ctx, w))
		if got, _ := repo.WebhookByID(ctx, w.ID); got.URL != w.URL || got.Active || got.Events[0] != "library.updated" {
			t.Errorf("updated = %+v", got)
		}

		now := time.Now().Truncate(time.Millisecond)
		next := now.Add(time.Minute)
		for i := 0; i < 3; i++ {
			dl := diagram.Delivery{ID: diagram.NewID(diagram.PrefixDelivery), WebhookID: w.ID, Event: "file.updated", Payload: `{"n":` + string(rune('0'+i)) + `}`,
				Status: diagram.DeliveryPending, NextAttemptAt: &now, CreatedAt: now.Add(time.Duration(i) * time.Second)}
			must(t, repo.CreateDelivery(ctx, dl))
			if i == 2 {
				code := 503
				dl.Attempt, dl.ResponseCode, dl.Error, dl.NextAttemptAt = 1, &code, "HTTP 503", &next
				must(t, repo.UpdateDelivery(ctx, dl))
				back, err := repo.Delivery(ctx, dl.ID)
				must(t, err)
				if back.Attempt != 1 || *back.ResponseCode != 503 || back.Error != "HTTP 503" || back.NextAttemptAt == nil || back.Status != diagram.DeliveryPending {
					t.Errorf("delivery = %+v", back)
				}
				dl.Status, dl.NextAttemptAt, dl.Error = diagram.DeliveryOK, nil, ""
				must(t, repo.UpdateDelivery(ctx, dl))
				if back, _ = repo.Delivery(ctx, dl.ID); back.Status != diagram.DeliveryOK || back.Error != "" || back.NextAttemptAt != nil {
					t.Errorf("done delivery = %+v", back)
				}
			}
		}
		ds := must1(repo.Deliveries(ctx, w.ID, 2))
		if len(ds) != 2 || ds[0].Payload != `{"n":2}` {
			t.Errorf("newest first, limited: %+v", ds)
		}
		if err := repo.UpdateDelivery(ctx, diagram.Delivery{ID: "dlv_missing"}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("update of a missing delivery: %v", err)
		}
		// an invalid status is refused by the table
		bad := diagram.Delivery{ID: diagram.NewID(diagram.PrefixDelivery), WebhookID: w.ID, Event: "x", Payload: "{}", Status: "weird", CreatedAt: now}
		if err := repo.CreateDelivery(ctx, bad); err == nil {
			t.Error("a delivery with an unknown status was stored")
		}

		must(t, repo.DeleteWebhook(ctx, w.ID))
		if _, err := repo.Delivery(ctx, ds[0].ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("deliveries outlive their webhook: %v", err)
		}
		if err := repo.DeleteWebhook(ctx, w.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("second delete: %v", err)
		}
	})

	t.Run("webhooks go with their project", func(t *testing.T) {
		p2, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Doomed", Data: dgDoc("z")})
		must(t, err)
		w := diagram.Webhook{ID: diagram.NewID(diagram.PrefixWebhook), ProjectID: p2.Project.ID, URL: "https://x.example.com", Secret: "s", Events: []string{"file.updated"}, Active: true, CreatedAt: time.Now()}
		must(t, repo.CreateWebhook(ctx, w))
		must(t, d.DeleteProject(ctx, ali.ID, p2.Project.ID))
		if _, err := repo.WebhookByID(ctx, w.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("webhook outlived its project: %v", err)
		}
	})

	t.Run("mcp audit", func(t *testing.T) {
		d.RecordMCP(ctx, diagram.MCPAudit{UserID: ali.ID, TokenID: "tok_x", Tool: "read_file", FileID: fid, ProjectID: pid, Status: diagram.AuditOK, Detail: `{"fileId":"x"}`, DurationMS: 12})
		d.RecordMCP(ctx, diagram.MCPAudit{UserID: ali.ID, Tool: "add_nodes", Status: diagram.AuditDenied})
		d.RecordMCP(ctx, diagram.MCPAudit{UserID: bob.ID, Tool: "list_projects", Status: diagram.AuditOK})
		log, err := d.MCPAuditLog(ctx, ali.ID, 100)
		must(t, err)
		if len(log) != 2 || log[0].Tool != "add_nodes" || log[1].TokenID != "tok_x" || log[1].DurationMS != 12 || log[1].FileID != fid || log[0].TokenID != "" || log[0].Detail != "" {
			t.Errorf("audit = %+v", log)
		}
		if one, _ := d.MCPAuditLog(ctx, ali.ID, 1); len(one) != 1 {
			t.Errorf("limit: %d", len(one))
		}
		// the trail goes with the user
		gone := account("gone2")
		d.RecordMCP(ctx, diagram.MCPAudit{UserID: gone.ID, Tool: "x", Status: diagram.AuditOK})
		_, err = db.Exec(ctx, `DELETE FROM users WHERE id = $1`, gone.ID)
		must(t, err)
		var n int
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM dg_mcp_audit WHERE user_id = $1`, gone.ID).Scan(&n))
		if n != 0 {
			t.Error("audit entries outlive their user")
		}
	})

	t.Run("sdd export and import keep folders, thumbnails and history", func(t *testing.T) {
		src := must1(d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Exported", Data: dgDoc("n1", "n2")}))
		sid := src.Project.ID
		outer, _, err := d.CreateFolder(ctx, ali.ID, sid, application.NewFolder{Name: "Outer"})
		must(t, err)
		inner, _, err := d.CreateFolder(ctx, ali.ID, sid, application.NewFolder{Name: "Inner", ParentID: &outer.ID})
		must(t, err)
		nested, _, err := d.CreateFile(ctx, ali.ID, sid, application.NewDiagramFile{Name: "Nested", FolderID: &inner.ID, Data: dgDoc("x")})
		must(t, err)
		_, err = d.SetThumbnail(ctx, ali.ID, src.File.ID, itPNG)
		must(t, err)

		out := must1(d.ExportProject(ctx, ali.ID, "", sid))
		got := must1(d.ImportAny(ctx, bob.ID, "x.sdd", out.Data))
		if got.Kind != "project" || got.Project.OwnerID != bob.ID || got.Project.Name != "Exported" || got.Project.FileCount != 2 || got.Project.FolderCount != 2 {
			t.Fatalf("imported = %+v", got.Project)
		}
		tree := must1(d.Tree(ctx, bob.ID, got.Project.ID))
		byName := map[string]diagram.File{}
		for _, f := range tree.Files {
			byName[f.Name] = f
		}
		ids := map[string]string{}
		for _, f := range tree.Folders {
			ids[f.Name] = f.ID
		}
		if f := byName["Nested"]; f.ID == nested.ID || f.FolderID == nil || *f.FolderID != ids["Inner"] {
			t.Errorf("nested file = %+v (folders %v)", f, ids)
		}
		if in := tree.Folders; len(in) != 2 {
			t.Fatalf("folders = %+v", in)
		}
		for _, f := range tree.Folders {
			if f.Name == "Inner" && (f.ParentID == nil || *f.ParentID != ids["Outer"]) {
				t.Errorf("the folder tree was not kept: %+v", f)
			}
		}
		top := byName["Exported"]
		if top.ThumbnailHash == nil {
			t.Error("the thumbnail was lost")
		}
		doc := must1(d.GetFile(ctx, bob.ID, top.ID))
		// (document-level extras beyond settings and assets are not part of the .sdd format, as in the Node server)
		if !strings.Contains(string(doc.Data), `"n2"`) || !strings.Contains(string(doc.Data), `"x": 1.5`) {
			t.Errorf("document = %s", doc.Data)
		}
		vs := must1(d.Versions(ctx, bob.ID, top.ID))
		if len(vs) != 1 || vs[0].Label == nil || *vs[0].Label != "Imported from .sdd" {
			t.Errorf("versions = %+v", vs)
		}
		// the source is untouched, and a stranger could not have exported it
		if _, err := d.ExportProject(ctx, bob.ID, "", sid); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("export by a stranger: %v", err)
		}
		// one file into an existing project, in a folder
		one := must1(d.ExportFile(ctx, ali.ID, "", src.File.ID))
		outerID := ids["Outer"]
		into := must1(d.ImportIntoProject(ctx, bob.ID, got.Project.ID, &outerID, "f.sdd", one.Data))
		if into.File.FolderID == nil || *into.File.FolderID != outerID || into.File.ID == src.File.ID {
			t.Errorf("import into = %+v", into.File)
		}
		if _, err := d.ImportIntoProject(ctx, ali.ID, got.Project.ID, nil, "f.sdd", one.Data); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("import into a stranger's project: %v", err)
		}
	})

	t.Run("published list of an owner", func(t *testing.T) {
		second := must1(d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Pub2", Data: dgDoc("p")}))
		third := must1(d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Pub3", Data: dgDoc("q")}))
		theirs := must1(d.CreateProject(ctx, bob.ID, application.NewDiagramProject{Name: "Bobs", Data: dgDoc("b")}))
		_ = third
		must1(d.PublishFile(ctx, ali.ID, fid, application.PublishRequest{Visibility: diagram.VisPublic, Title: "First"}))
		time.Sleep(5 * time.Millisecond) // publication times are milliseconds: keep the order clear
		must1(d.PublishFile(ctx, ali.ID, second.File.ID, application.PublishRequest{Visibility: diagram.VisUnlisted, Title: "Second"}))
		must1(d.PublishFile(ctx, bob.ID, theirs.File.ID, application.PublishRequest{Visibility: diagram.VisPublic}))
		page := must1(d.PublishedDiagrams(ctx, ali.ID, 1, 10))
		if page.Total != 2 || len(page.Items) != 2 || page.Items[0].Title != "Second" || page.Items[1].Title != "First" {
			t.Fatalf("published = %+v", page)
		}
		if one := must1(d.PublishedDiagrams(ctx, ali.ID, 2, 1)); one.Total != 2 || len(one.Items) != 1 || one.Items[0].Title != "First" {
			t.Errorf("page 2 = %+v", one)
		}
		must(t, d.UnpublishFile(ctx, ali.ID, fid))
		if page = must1(d.PublishedDiagrams(ctx, ali.ID, 1, 10)); page.Total != 1 {
			t.Errorf("after unpublish: %+v", page)
		}
		if page = must1(d.PublishedDiagrams(ctx, bob.ID, 1, 10)); page.Total != 1 || page.Items[0].AuthorID != bob.ID {
			t.Errorf("bob's list: %+v", page)
		}
	})
}

func must1[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
