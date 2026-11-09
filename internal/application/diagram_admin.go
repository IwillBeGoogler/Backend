package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// Moderator use cases for the diagram workspace. They look at every
// project whoever owns it and are gated by permissions, not by project
// roles: diagrams.read (or diagrams.manage) to look, diagrams.manage to
// delete or revoke, templates.manage for the gallery.

// can asks the authorizer; without one every permission is denied.
func (d *Diagrams) can(ctx context.Context, resource, action string) error {
	if d.Authz == nil {
		return domain.ErrForbidden
	}
	return d.Authz.Can(ctx, resource, action, 0, 0)
}

// canRead allows diagrams.read or, as it implies reading, diagrams.manage.
func (d *Diagrams) canRead(ctx context.Context) error {
	err := d.can(ctx, "diagrams", "read")
	if err == nil || !errors.Is(err, domain.ErrForbidden) {
		return err
	}
	if d.can(ctx, "diagrams", "manage") == nil {
		return nil
	}
	return err
}

// canTemplates checks templates.manage and that the gallery is configured.
func (d *Diagrams) canTemplates(ctx context.Context) error {
	if err := d.can(ctx, "templates", "manage"); err != nil {
		return err
	}
	if d.Templates == nil {
		return errors.New("diagram templates repository not configured")
	}
	return nil
}

func (d *Diagrams) audit(ctx context.Context, actor int, action, target string, meta map[string]any) {
	if d.Audit != nil {
		d.Audit.Record(ctx, actor, action, target, meta)
	}
}

func (d *Diagrams) needAdmin() error {
	if d.Admin == nil {
		return errors.New("diagram admin repository not configured")
	}
	return nil
}

// AdminSchemaSQL is the app's own database schema as DDL text, for the ER
// import. It needs diagrams.manage; prefix keeps only tables starting with it.
func (d *Diagrams) AdminSchemaSQL(ctx context.Context, prefix string) (string, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.AdminSchemaSQL")
	defer span.End()
	if err := d.can(ctx, "diagrams", "manage"); err != nil {
		return "", err
	}
	if d.SchemaDump == nil {
		return "", domain.ErrNotFound
	}
	return d.SchemaDump(ctx, prefix)
}

// AdminStats are the workspace totals; active editors are counted over the
// last seven days.
func (d *Diagrams) AdminStats(ctx context.Context) (diagram.AdminStats, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.AdminStats")
	defer span.End()
	if err := d.canRead(ctx); err != nil {
		return diagram.AdminStats{}, err
	}
	if err := d.needAdmin(); err != nil {
		return diagram.AdminStats{}, err
	}
	return d.Admin.Stats(ctx, d.clock().Add(-7*24*time.Hour))
}

// AdminProjectPage is one page of the admin project list.
type AdminProjectPage struct {
	Items      []diagram.AdminProject
	Limit      int
	HasMore    bool
	NextCursor string
}

// Paginated lets the HTTP layer send {data, meta}.
func (p AdminProjectPage) Paginated() (any, any) {
	return p.Items, domain.Meta{Limit: p.Limit, HasMore: p.HasMore, NextCursor: p.NextCursor, Total: len(p.Items)}
}

// AdminProjects lists projects, last updated first. The cursor counts the
// rows already seen; owner is an exact username.
func (d *Diagrams) AdminProjects(ctx context.Context, search, owner string, p domain.Paging) (AdminProjectPage, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.AdminProjects")
	defer span.End()
	if err := d.canRead(ctx); err != nil {
		return AdminProjectPage{}, err
	}
	if err := d.needAdmin(); err != nil {
		return AdminProjectPage{}, err
	}
	offset := 0
	if p.Cursor != "" {
		c, err := domain.DecodeCursor(p.Cursor, 1)
		if err != nil {
			return AdminProjectPage{}, err
		}
		offset = int(c[0])
	}
	limit := min(max(p.Limit, 1), domain.MaxLimit)
	items, err := d.Admin.Projects(ctx, diagram.AdminProjectQuery{
		Search: strings.TrimSpace(search), Owner: strings.TrimSpace(owner), Offset: offset, Limit: limit})
	if err != nil {
		return AdminProjectPage{}, err
	}
	out := AdminProjectPage{Items: items, Limit: limit}
	if len(items) > limit {
		out.Items, out.HasMore = items[:limit], true
		out.NextCursor = domain.EncodeCursor(int64(offset + limit))
	}
	return out, nil
}

// AdminProject is one project with its files, members and links.
func (d *Diagrams) AdminProject(ctx context.Context, id string) (diagram.AdminProjectDetail, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.AdminProject")
	defer span.End()
	if err := d.canRead(ctx); err != nil {
		return diagram.AdminProjectDetail{}, err
	}
	if err := d.needAdmin(); err != nil {
		return diagram.AdminProjectDetail{}, err
	}
	det, err := d.Admin.ProjectDetail(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return det, diagram.NotFound("Project")
	}
	return det, err
}

// AdminDeleteProject removes a project with everything in it. Needs
// diagrams.manage.
func (d *Diagrams) AdminDeleteProject(ctx context.Context, actor int, id string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.AdminDeleteProject")
	defer span.End()
	if err := d.can(ctx, "diagrams", "manage"); err != nil {
		return err
	}
	p, err := d.Repo.Project(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return diagram.NotFound("Project")
	}
	if err != nil {
		return err
	}
	_, files, err := d.Repo.Tree(ctx, id)
	if err != nil {
		return err
	}
	if err := d.Repo.DeleteProject(ctx, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return diagram.NotFound("Project")
		}
		return err
	}
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	d.emit(ctx, DiagramEvent{Kind: EventProjectDeleted, ProjectID: id, FileIDs: ids})
	d.audit(ctx, actor, "diagram.project.delete", "diagram_project:"+id,
		map[string]any{"name": p.Name, "owner_id": p.OwnerID, "files": len(files)})
	return nil
}

// AdminRevokeShare revokes a share link of any project; revoking a revoked
// link is fine. Needs diagrams.manage.
func (d *Diagrams) AdminRevokeShare(ctx context.Context, actor int, shareID string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.AdminRevokeShare")
	defer span.End()
	if err := d.can(ctx, "diagrams", "manage"); err != nil {
		return err
	}
	if err := d.needAdmin(); err != nil {
		return err
	}
	project, err := d.Admin.RevokeShareByID(ctx, shareID, d.clock())
	if errors.Is(err, domain.ErrNotFound) {
		return diagram.NotFound("Share link")
	}
	if err != nil {
		return err
	}
	d.shareRevoked(ctx, project, shareID)
	d.audit(ctx, actor, "diagram.share.revoke", "diagram_share:"+shareID, map[string]any{"project_id": project})
	return nil
}

// AdminDeleteFile removes one file of any project, for a report decision.
// It needs diagrams.manage; a file that is already gone is not an error.
func (d *Diagrams) AdminDeleteFile(ctx context.Context, actor int, fileID string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.AdminDeleteFile")
	defer span.End()
	if err := d.can(ctx, "diagrams", "manage"); err != nil {
		return err
	}
	f, err := d.Repo.File(ctx, fileID)
	if err != nil {
		return err
	}
	if err := d.Repo.DeleteFile(ctx, fileID); err != nil {
		return err
	}
	d.emit(ctx, DiagramEvent{Kind: EventFileDeleted, ProjectID: f.ProjectID, FileID: fileID})
	_, _ = d.touchFor(ctx, f.ProjectID, actor, "file.deleted", "", fileID)
	d.audit(ctx, actor, "diagram.file.delete", "diagram_file:"+fileID,
		map[string]any{"name": f.Name, "project_id": f.ProjectID})
	return nil
}

// ReportTarget checks that a reporter can see the file and returns its
// project and project owner: a member, or anyone for a published file.
func (d *Diagrams) ReportTarget(ctx context.Context, reporter int, fileID string) (projectID string, ownerID int, err error) {
	f, err := d.Repo.File(ctx, fileID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", 0, diagram.NotFound("File")
	}
	if err != nil {
		return "", 0, err
	}
	p, err := d.Repo.Project(ctx, f.ProjectID)
	if err != nil {
		return "", 0, err
	}
	if _, _, aerr := d.fileAccess(ctx, reporter, fileID, diagram.RoleViewer); aerr == nil {
		return p.ID, p.OwnerID, nil
	} else if !errors.Is(aerr, domain.ErrNotFound) {
		return "", 0, aerr
	}
	if d.Publish != nil {
		if l, lerr := d.Publish.Listing(ctx, fileID); lerr == nil && l.Visibility.Published() {
			return p.ID, p.OwnerID, nil
		}
	}
	return "", 0, diagram.NotFound("File")
}

// Templates

// AdminTemplates lists every template, drafts included.
func (d *Diagrams) AdminTemplates(ctx context.Context) ([]diagram.Template, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.AdminTemplates")
	defer span.End()
	if err := d.canTemplates(ctx); err != nil {
		return nil, err
	}
	return d.Templates.List(ctx, false)
}

// NewTemplate is the body of a template create.
type NewTemplate struct {
	Title       string
	Kind        string
	Description string
	FileID      string
	Published   bool
	Position    int
}

// TemplatePatch changes the fields that are set.
type TemplatePatch struct {
	Title       *string
	Kind        *string
	Description *string
	FileID      *string
	Published   *bool
	Position    *int
}

// fileExists is a validation error for a template that points nowhere. The
// actor also needs diagrams.read: a template publishes the file's content.
func (d *Diagrams) fileExists(ctx context.Context, fileID string) error {
	if err := d.canRead(ctx); err != nil {
		return err
	}
	_, err := d.Repo.File(ctx, fileID)
	if errors.Is(err, domain.ErrNotFound) {
		return diagram.Validation("file_id: no such diagram file")
	}
	return err
}

func (d *Diagrams) CreateTemplate(ctx context.Context, actor int, in NewTemplate) (diagram.Template, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.CreateTemplate")
	defer span.End()
	if err := d.canTemplates(ctx); err != nil {
		return diagram.Template{}, err
	}
	now := d.clock()
	t, err := diagram.CleanTemplate(diagram.Template{ID: diagram.NewID(diagram.PrefixTemplate), Title: in.Title, Kind: in.Kind,
		Description: in.Description, FileID: in.FileID, Published: in.Published, Position: in.Position, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return t, err
	}
	if err := d.fileExists(ctx, t.FileID); err != nil {
		return t, err
	}
	if err := d.Templates.Create(ctx, t); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return t, diagram.Validation("file_id: no such diagram file")
		}
		return t, err
	}
	d.audit(ctx, actor, "diagram.template.create", "diagram_template:"+t.ID, map[string]any{"title": t.Title, "file_id": t.FileID})
	return d.Templates.Get(ctx, t.ID)
}

func (d *Diagrams) UpdateTemplate(ctx context.Context, actor int, id string, in TemplatePatch) (diagram.Template, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.UpdateTemplate")
	defer span.End()
	if err := d.canTemplates(ctx); err != nil {
		return diagram.Template{}, err
	}
	t, err := d.Templates.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return t, diagram.NotFound("Template")
	}
	if err != nil {
		return t, err
	}
	old := t
	if in.Title != nil {
		t.Title = *in.Title
	}
	if in.Kind != nil {
		t.Kind = *in.Kind
	}
	if in.Description != nil {
		t.Description = *in.Description
	}
	if in.FileID != nil {
		t.FileID = *in.FileID
	}
	if in.Published != nil {
		t.Published = *in.Published
	}
	if in.Position != nil {
		t.Position = *in.Position
	}
	if t, err = diagram.CleanTemplate(t); err != nil {
		return t, err
	}
	if t.FileID != old.FileID {
		if err := d.fileExists(ctx, t.FileID); err != nil {
			return t, err
		}
	}
	t.UpdatedAt = d.clock()
	if err := d.Templates.Update(ctx, t); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			return t, diagram.NotFound("Template")
		case errors.Is(err, domain.ErrConflict):
			return t, diagram.Validation("file_id: no such diagram file")
		}
		return t, err
	}
	changes := map[string]any{}
	if t.Title != old.Title {
		changes["title"] = map[string]any{"old": old.Title, "new": t.Title}
	}
	if t.FileID != old.FileID {
		changes["file_id"] = map[string]any{"old": old.FileID, "new": t.FileID}
	}
	if t.Kind != old.Kind {
		changes["kind"] = map[string]any{"old": old.Kind, "new": t.Kind}
	}
	if t.Description != old.Description {
		changes["description"] = true
	}
	if t.Published != old.Published {
		changes["published"] = t.Published
	}
	if t.Position != old.Position {
		changes["position"] = t.Position
	}
	d.audit(ctx, actor, "diagram.template.update", "diagram_template:"+id, map[string]any{"changed": changes})
	return d.Templates.Get(ctx, id)
}

// DeleteTemplate removes the template from the gallery; its file stays.
func (d *Diagrams) DeleteTemplate(ctx context.Context, actor int, id string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.DeleteTemplate")
	defer span.End()
	if err := d.canTemplates(ctx); err != nil {
		return err
	}
	err := d.Templates.Delete(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return diagram.NotFound("Template")
	}
	if err == nil {
		d.audit(ctx, actor, "diagram.template.delete", "diagram_template:"+id, nil)
	}
	return err
}

// ReorderTemplates puts the named templates first in the given order and
// renumbers the gallery, atomically.
func (d *Diagrams) ReorderTemplates(ctx context.Context, actor int, ids []string) ([]diagram.Template, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ReorderTemplates")
	defer span.End()
	if err := d.canTemplates(ctx); err != nil {
		return nil, err
	}
	if len(ids) == 0 || len(ids) > 500 {
		return nil, diagram.Validation("ids: 1-500 template ids")
	}
	if err := d.Templates.Reorder(ctx, ids, d.clock()); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, diagram.NotFound("Template")
		}
		return nil, err
	}
	d.audit(ctx, actor, "diagram.template.reorder", "diagram_template:*", map[string]any{"count": len(ids)})
	return d.Templates.List(ctx, false)
}

// PublicTemplates is the gallery: published templates in order.
func (d *Diagrams) PublicTemplates(ctx context.Context) ([]diagram.Template, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.PublicTemplates")
	defer span.End()
	if d.Templates == nil {
		return nil, nil
	}
	return d.Templates.List(ctx, true)
}

// ForkTemplate copies a published template into a new project of the
// caller and returns the project with its file.
func (d *Diagrams) ForkTemplate(ctx context.Context, actor int, id string) (DiagramProject, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ForkTemplate")
	defer span.End()
	if d.Templates == nil {
		return DiagramProject{}, diagram.NotFound("Template")
	}
	t, err := d.Templates.Get(ctx, id)
	if err != nil || !t.Published {
		if err == nil || errors.Is(err, domain.ErrNotFound) {
			err = diagram.NotFound("Template")
		}
		return DiagramProject{}, err
	}
	src, err := d.Repo.File(ctx, t.FileID)
	if errors.Is(err, domain.ErrNotFound) {
		return DiagramProject{}, diagram.NotFound("Template")
	}
	if err != nil {
		return DiagramProject{}, err
	}
	out, err := d.CreateProject(ctx, actor, NewDiagramProject{Name: t.Title,
		File: &NewDiagramFile{Name: t.Title, Kind: src.Kind, Data: src.Data}})
	if err != nil {
		return out, err
	}
	if src.ThumbnailHash != nil && d.Sharing != nil {
		if err := d.Sharing.SetThumbnail(ctx, out.File.ID, *src.ThumbnailHash); err != nil {
			return out, err
		}
		out.File.ThumbnailHash = src.ThumbnailHash
		out.Project.ThumbFileID, out.Project.ThumbnailHash = out.File.ID, src.ThumbnailHash
	}
	return out, nil
}
