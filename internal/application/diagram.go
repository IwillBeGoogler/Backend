package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// Diagrams are the diagram workspace use cases. Access follows the project
// role (owner, or a member's viewer/commenter/editor), replaced for a member by a
// per-file override; a stranger gets not found, never forbidden, so ids do
// not leak. Callers pass the authenticated user's id (never 0).
type Diagrams struct {
	Repo  diagram.Repository
	Prefs diagram.PrefsRepository
	// KeepLast and KeepDays bound a file's snapshot history; named
	// versions are never pruned.
	KeepLast, KeepDays int
	// Coalesce is how long an unnamed, unlabeled head snapshot keeps
	// absorbing the same writer's next saves (0: every save adds one).
	Coalesce time.Duration
	// MaxDataBytes caps one document.
	MaxDataBytes int
	// MaxSDDBytes caps an imported .sdd file and MaxSDDUnzipBytes what it
	// may unpack to (0: 100 MB and 300 MB).
	MaxSDDBytes, MaxSDDUnzipBytes int64

	// Sharing stores members, share links, assets, libraries and private
	// stickies; NewDiagrams takes it from repo when repo implements it.
	Sharing diagram.SharingRepository
	// Users resolves accounts for invitations and member lists.
	Users DiagramUsers
	// Blobs keeps asset and thumbnail bytes by content hash.
	Blobs Storage
	// MaxAssetBytes caps one uploaded image.
	MaxAssetBytes int
	// SanitizeSVG cleans an SVG upload. isSVG says the bytes look like SVG;
	// err (when isSVG) means they cannot be cleaned. Nil refuses SVG.
	SanitizeSVG func(data []byte) (clean []byte, isSVG bool, err error)

	// Comments stores comment threads; NewDiagrams takes it from repo when
	// repo implements it.
	Comments diagram.CommentRepository
	// Notices delivers comment notifications through the blog's
	// notification system (nil: none are sent or listed).
	Notices DiagramNotices

	// Y keeps the Yjs state of live-edited files; NewDiagrams takes it from
	// repo when repo implements it. A restored version resets it.
	Y diagram.YRepository
	// Events receives realtime events (nil: none are sent).
	Events DiagramEvents
	// Cutoff ends live realtime sessions whose access was revoked (nil: none).
	Cutoff RealtimeCutoff

	// Publish stores files' blog pages; NewDiagrams takes it from repo when
	// repo implements it (nil: publishing is off).
	Publish diagram.PublishRepository
	// Engage stores claps and bookmarks of published files; NewDiagrams takes
	// it from repo when repo implements it (nil: both are off).
	Engage diagram.EngagementRepository
	// Tokens stores personal access tokens, Webhooks webhooks with their
	// deliveries and MCPAudit the MCP audit trail; NewDiagrams takes each
	// from repo when repo implements it.
	Tokens   diagram.TokenRepository
	Webhooks diagram.WebhookRepository
	MCPAudit diagram.MCPAuditRepository
	// Hooks queues and sends webhook deliveries (nil: webhooks are off).
	Hooks *WebhookDispatcher
	// Admin reads the whole workspace for moderators; NewDiagrams takes it
	// from repo when repo implements it.
	Admin diagram.AdminRepository
	// SchemaDump renders the app's own database schema as DDL for
	// AdminSchemaSQL (nil: the route answers 404).
	SchemaDump func(ctx context.Context, prefix string) (string, error)
	// Templates is the template gallery (nil: no gallery).
	Templates diagram.TemplateRepository
	// Authz checks the admin permissions (diagrams.read, diagrams.manage,
	// templates.manage); nil denies them all. Audit records admin actions.
	Authz Authorizer
	Audit Auditor

	now func() time.Time
}

func NewDiagrams(repo diagram.Repository, prefs diagram.PrefsRepository) *Diagrams {
	d := &Diagrams{Repo: repo, Prefs: prefs, KeepLast: 50, KeepDays: 30, Coalesce: 5 * time.Minute, MaxDataBytes: 10 << 20, MaxAssetBytes: 5 << 20, now: time.Now}
	d.Sharing, _ = repo.(diagram.SharingRepository)
	d.Comments, _ = repo.(diagram.CommentRepository)
	d.Y, _ = repo.(diagram.YRepository)
	d.Publish, _ = repo.(diagram.PublishRepository)
	d.Engage, _ = repo.(diagram.EngagementRepository)
	d.Admin, _ = repo.(diagram.AdminRepository)
	d.Tokens, _ = repo.(diagram.TokenRepository)
	d.Webhooks, _ = repo.(diagram.WebhookRepository)
	d.MCPAudit, _ = repo.(diagram.MCPAuditRepository)
	return d
}

// clock is the current time at the millisecond precision the API reports.
func (d *Diagrams) clock() time.Time { return d.now().Truncate(time.Millisecond) }

// DiagramFile is a file with its document and the caller's role on it.
type DiagramFile struct {
	diagram.Doc
	Role diagram.Role
}

// DiagramProjectPage is one page of the project list.
type DiagramProjectPage struct {
	Items          []diagram.ProjectSummary
	Total          int
	Page, PageSize int
}

// DiagramProject is a created project with its first file; Created is
// false when a client-chosen id was replayed.
type DiagramProject struct {
	Project diagram.ProjectSummary
	File    DiagramFile
	Created bool
}

// DiagramTree is a project's folders and file metadata, no documents.
type DiagramTree struct {
	Project diagram.ProjectSummary
	Folders []diagram.Folder
	Files   []diagram.File
}

// FolderCopy is the result of duplicating a folder.
type FolderCopy struct {
	Folder  diagram.Folder
	Folders []diagram.Folder
	Files   []diagram.File
}

// NewFolder, FolderPatch, NewFile and FilePatch are request bodies;
// Opt fields tell "absent" from null (null moves to the project root).
type NewFolder struct {
	ID       string
	Name     string
	ParentID *string
	Position *float64
}

type FolderPatch struct {
	Name     *string
	ParentID diagram.Opt[*string]
	Position *float64
}

type NewDiagramFile struct {
	ID       string
	Name     string
	Kind     string
	FolderID *string
	Position *float64
	Data     json.RawMessage
	// Label names the first snapshot ("Imported from .sdd") and
	// ThumbnailHash is a stored preview to start with; the API does not
	// take either from a client.
	Label         string
	ThumbnailHash *string
}

type FilePatch struct {
	Name     *string
	FolderID diagram.Opt[*string]
	Position *float64
	Kind     *string
}

// NewDiagramProject is a project and its first file; Data is also
// accepted at the top level (older clients).
type NewDiagramProject struct {
	ID   string
	Name string
	File *NewDiagramFile
	Data json.RawMessage
}

// access is the caller's standing in a project.
type access struct {
	project diagram.Project
	role    diagram.Role
}

// projectAccess resolves the caller's role in a project: not found for
// strangers, forbidden when the role is below min.
func (d *Diagrams) projectAccess(ctx context.Context, actor int, id string, min diagram.Role) (access, error) {
	if !tokenReaches(ctx, id) { // an access token limited to other projects
		return access{}, diagram.NotFound("Project")
	}
	p, err := d.Repo.Project(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return access{}, diagram.NotFound("Project")
	}
	if err != nil {
		return access{}, err
	}
	role := diagram.RoleOwner
	if p.OwnerID != actor {
		if role, err = d.Repo.MemberRole(ctx, id, actor); err != nil {
			return access{}, err
		}
		if role == "" {
			return access{}, diagram.NotFound("Project")
		}
	}
	if !role.Allows(min) {
		return access{}, diagram.NeedRole(min)
	}
	return access{p, role}, nil
}

// fileAccess is projectAccess for a file, with the per-file override
// applied to members (owners stay owners).
func (d *Diagrams) fileAccess(ctx context.Context, actor int, id string, min diagram.Role) (access, diagram.Doc, error) {
	f, err := d.Repo.File(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return access{}, f, diagram.NotFound("File")
	}
	if err != nil {
		return access{}, f, err
	}
	a, err := d.projectAccess(ctx, actor, f.ProjectID, diagram.RoleViewer)
	if errors.Is(err, domain.ErrNotFound) {
		return access{}, f, diagram.NotFound("File")
	}
	if err != nil {
		return access{}, f, err
	}
	if a.role != diagram.RoleOwner {
		over, err := d.Repo.FileRole(ctx, id, actor)
		if err != nil {
			return access{}, f, err
		}
		if over != "" {
			a.role = over
		}
	}
	if !a.role.Allows(min) {
		return access{}, f, diagram.NeedRole(min)
	}
	return a, f, nil
}

func (d *Diagrams) folderAccess(ctx context.Context, actor int, id string, min diagram.Role) (access, diagram.Folder, error) {
	f, err := d.Repo.Folder(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return access{}, f, diagram.NotFound("Folder")
	}
	if err != nil {
		return access{}, f, err
	}
	a, err := d.projectAccess(ctx, actor, f.ProjectID, min)
	return a, f, err
}

// touch bumps the project's tree revision after a tree mutation.
func (d *Diagrams) touch(ctx context.Context, projectID string, actor int) (int, error) {
	return d.Repo.TouchTree(ctx, projectID, actor, d.clock())
}

func (d *Diagrams) summary(ctx context.Context, p diagram.Project, role diagram.Role) (diagram.ProjectSummary, error) {
	files, folders, err := d.Repo.Counts(ctx, p.ID)
	if err != nil {
		return diagram.ProjectSummary{}, err
	}
	thumbFile, thumb, err := d.Repo.ProjectThumb(ctx, p.ID)
	return diagram.ProjectSummary{Project: p, Role: role, FileCount: files, FolderCount: folders,
		ThumbFileID: thumbFile, ThumbnailHash: thumb}, err
}

// folderInProject checks that a folder id exists in the project.
func (d *Diagrams) folderInProject(ctx context.Context, projectID string, id *string) error {
	if id == nil {
		return nil
	}
	f, err := d.Repo.Folder(ctx, *id)
	if errors.Is(err, domain.ErrNotFound) || err == nil && f.ProjectID != projectID {
		return diagram.ErrFolderNotInProject
	}
	return err
}

// parentInProject is folderInProject for a parent folder.
func (d *Diagrams) parentInProject(ctx context.Context, projectID string, id *string) error {
	if err := d.folderInProject(ctx, projectID, id); err != nil {
		return diagram.ErrParentNotFound
	}
	return nil
}

// ListProjects pages through the projects the caller owns or belongs to.
func (d *Diagrams) ListProjects(ctx context.Context, actor int, q diagram.ProjectQuery) (DiagramProjectPage, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ListProjects")
	defer span.End()
	items, total, err := d.Repo.ListProjects(ctx, actor, q)
	return DiagramProjectPage{Items: items, Total: total, Page: q.Page, PageSize: q.PageSize}, err
}

// dataName is the document's own name, the last choice for a project name.
func dataName(data json.RawMessage) string {
	var doc struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &doc)
	return strings.TrimSpace(doc.Name)
}

// CreateProject creates a project together with its first file. A
// client-chosen id makes the call idempotent: replaying it for the same
// owner returns the existing project (Created false).
func (d *Diagrams) CreateProject(ctx context.Context, actor int, in NewDiagramProject) (DiagramProject, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.CreateProject")
	defer span.End()
	if err := diagram.CheckClientID(in.ID); err != nil {
		return DiagramProject{}, err
	}
	first := NewDiagramFile{Data: in.Data}
	fileName := ""
	if in.File != nil {
		first = *in.File
		fileName = strings.TrimSpace(first.Name)
		if first.Data == nil {
			first.Data = in.Data
		}
		if err := diagram.CheckClientID(first.ID); err != nil {
			return DiagramProject{}, err
		}
	}
	for _, n := range []string{in.Name, fileName} {
		if err := diagram.CheckNameSize(n, false); err != nil {
			return DiagramProject{}, err
		}
	}
	raw := firstNonEmpty(strings.TrimSpace(in.Name), fileName, dataName(in.Data))
	if raw == "" {
		return DiagramProject{}, diagram.Validation("name is required")
	}
	name, err := diagram.CleanName(raw, "Untitled")
	if err != nil {
		return DiagramProject{}, err
	}
	first.Name = firstNonEmpty(fileName, name, "")

	now := d.clock()
	p := diagram.Project{ID: in.ID, OwnerID: actor, Name: name, CreatedAt: now, UpdatedAt: now, UpdatedBy: actor}
	if p.ID == "" {
		p.ID = diagram.NewID(diagram.PrefixProject)
	} else if existing, err := d.Repo.Project(ctx, p.ID); err == nil {
		if existing.OwnerID != actor {
			return DiagramProject{}, diagram.ErrIDTaken
		}
		return d.replayProject(ctx, actor, existing, name)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return DiagramProject{}, err
	}
	// validate the document before anything is stored
	nf, priv, err := d.buildFile(ctx, p.ID, writer{user: actor, owner: actor}, first, 0, now)
	if err != nil {
		return DiagramProject{}, err
	}
	if err := d.Repo.CreateProject(ctx, p); err != nil {
		return DiagramProject{}, idTaken(err)
	}
	if err := d.Repo.InsertFile(ctx, nf); err != nil {
		return DiagramProject{}, idTaken(err)
	}
	if err := d.filePrivate(ctx, nf.Doc.ID, priv, now); err != nil {
		return DiagramProject{}, err
	}
	if _, err := d.touch(ctx, p.ID, actor); err != nil {
		return DiagramProject{}, err
	}
	fresh, err := d.Repo.Project(ctx, p.ID)
	if err != nil {
		return DiagramProject{}, err
	}
	sum, err := d.summary(ctx, fresh, diagram.RoleOwner)
	return DiagramProject{Project: sum, File: DiagramFile{nf.Doc, diagram.RoleOwner}, Created: true}, err
}

// replayProject answers a replayed create: the existing project and its
// first file (made when the file is gone).
func (d *Diagrams) replayProject(ctx context.Context, actor int, p diagram.Project, name string) (DiagramProject, error) {
	_, files, err := d.Repo.Tree(ctx, p.ID)
	if err != nil {
		return DiagramProject{}, err
	}
	var doc diagram.Doc
	if len(files) > 0 {
		doc, err = d.Repo.File(ctx, files[0].ID)
	} else {
		nf, _, berr := d.buildFile(ctx, p.ID, writer{user: actor, owner: actor}, NewDiagramFile{Name: name}, 0, d.clock())
		if err = berr; err == nil {
			err = d.Repo.InsertFile(ctx, nf)
			doc = nf.Doc
		}
	}
	if err != nil {
		return DiagramProject{}, err
	}
	sum, err := d.summary(ctx, p, diagram.RoleOwner)
	return DiagramProject{Project: sum, File: DiagramFile{doc, diagram.RoleOwner}}, err
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(a, b, c string) string {
	for _, s := range []string{a, b, c} {
		if s != "" {
			return s
		}
	}
	return ""
}

func idTaken(err error) error {
	if errors.Is(err, domain.ErrConflict) {
		return diagram.ErrIDTaken
	}
	return err
}

// GetProject returns a project with its counts.
func (d *Diagrams) GetProject(ctx context.Context, actor int, id string) (diagram.ProjectSummary, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.GetProject")
	defer span.End()
	a, err := d.projectAccess(ctx, actor, id, diagram.RoleViewer)
	if err != nil {
		return diagram.ProjectSummary{}, err
	}
	return d.summary(ctx, a.project, a.role)
}

// RenameProject is allowed for editors.
func (d *Diagrams) RenameProject(ctx context.Context, actor int, id, name string) (diagram.ProjectSummary, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.RenameProject")
	defer span.End()
	a, err := d.projectAccess(ctx, actor, id, diagram.RoleEditor)
	if err != nil {
		return diagram.ProjectSummary{}, err
	}
	if err := diagram.CheckNameSize(name, true); err != nil {
		return diagram.ProjectSummary{}, err
	}
	if name, err = diagram.CleanName(name, a.project.Name); err != nil {
		return diagram.ProjectSummary{}, err
	}
	if err := d.Repo.RenameProject(ctx, id, name, d.clock()); err != nil {
		return diagram.ProjectSummary{}, err
	}
	rev, err := d.touchFor(ctx, id, actor, "project.renamed", "", "")
	if err != nil {
		return diagram.ProjectSummary{}, err
	}
	a.project.Name, a.project.TreeRev, a.project.UpdatedBy = name, rev, actor
	a.project.UpdatedAt = d.clock()
	return d.summary(ctx, a.project, a.role)
}

// DeleteProject removes a project with all its folders and files; owners only.
func (d *Diagrams) DeleteProject(ctx context.Context, actor int, id string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.DeleteProject")
	defer span.End()
	if _, err := d.projectAccess(ctx, actor, id, diagram.RoleOwner); err != nil {
		return err
	}
	var fileIDs []string
	if d.Events != nil {
		_, files, err := d.Repo.Tree(ctx, id)
		if err != nil {
			return err
		}
		for _, f := range files {
			fileIDs = append(fileIDs, f.ID)
		}
	}
	if err := d.Repo.DeleteProject(ctx, id); err != nil {
		return err
	}
	d.emit(ctx, DiagramEvent{Kind: EventProjectDeleted, ProjectID: id, FileIDs: fileIDs})
	return nil
}

// Tree returns a project's folders and file metadata with its tree_rev.
func (d *Diagrams) Tree(ctx context.Context, actor int, id string) (DiagramTree, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.Tree")
	defer span.End()
	a, err := d.projectAccess(ctx, actor, id, diagram.RoleViewer)
	if err != nil {
		return DiagramTree{}, err
	}
	folders, files, err := d.Repo.Tree(ctx, id)
	if err != nil {
		return DiagramTree{}, err
	}
	sum := diagram.ProjectSummary{Project: a.project, Role: a.role, FileCount: len(files), FolderCount: len(folders)}
	return DiagramTree{Project: sum, Folders: folders, Files: files}, nil
}

// CreateFolder adds a folder; a client-chosen id makes it idempotent.
func (d *Diagrams) CreateFolder(ctx context.Context, actor int, projectID string, in NewFolder) (diagram.Folder, bool, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.CreateFolder")
	defer span.End()
	if _, err := d.projectAccess(ctx, actor, projectID, diagram.RoleEditor); err != nil {
		return diagram.Folder{}, false, err
	}
	if err := diagram.CheckClientID(in.ID); err != nil {
		return diagram.Folder{}, false, err
	}
	if err := diagram.CheckNameSize(in.Name, true); err != nil {
		return diagram.Folder{}, false, err
	}
	if in.ID != "" {
		existing, err := d.Repo.Folder(ctx, in.ID)
		switch {
		case err == nil && existing.ProjectID == projectID:
			return existing, false, nil
		case err == nil:
			return diagram.Folder{}, false, diagram.ErrIDTaken
		case !errors.Is(err, domain.ErrNotFound):
			return diagram.Folder{}, false, err
		}
	}
	if err := d.parentInProject(ctx, projectID, in.ParentID); err != nil {
		return diagram.Folder{}, false, err
	}
	name, err := diagram.CleanName(in.Name, "New folder")
	if err != nil {
		return diagram.Folder{}, false, err
	}
	pos, err := d.position(ctx, projectID, in.ParentID, in.Position)
	if err != nil {
		return diagram.Folder{}, false, err
	}
	now := d.clock()
	f := diagram.Folder{ID: in.ID, ProjectID: projectID, ParentID: in.ParentID, Name: name, Position: pos, CreatedAt: now, UpdatedAt: now}
	if f.ID == "" {
		f.ID = diagram.NewID(diagram.PrefixFolder)
	}
	if err := d.Repo.CreateFolder(ctx, f); err != nil {
		return diagram.Folder{}, false, idTaken(err)
	}
	_, err = d.touchFor(ctx, projectID, actor, "folder.created", f.ID, "")
	return f, true, err
}

// position is the requested sort key, or the end of the parent.
func (d *Diagrams) position(ctx context.Context, projectID string, parent *string, want *float64) (float64, error) {
	if want != nil {
		return *want, nil
	}
	return d.Repo.NextPosition(ctx, projectID, parent)
}

// UpdateFolder renames and/or moves a folder; moving it into itself or a
// descendant is a conflict (CYCLE).
func (d *Diagrams) UpdateFolder(ctx context.Context, actor int, id string, in FolderPatch) (diagram.Folder, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.UpdateFolder")
	defer span.End()
	_, f, err := d.folderAccess(ctx, actor, id, diagram.RoleEditor)
	if err != nil {
		return f, err
	}
	changed := false
	if in.Name != nil {
		if err := diagram.CheckNameSize(*in.Name, true); err != nil {
			return f, err
		}
		name, err := diagram.CleanName(*in.Name, f.Name)
		if err != nil {
			return f, err
		}
		changed, f.Name = changed || name != f.Name, name
	}
	if in.ParentID.Set && !sameID(in.ParentID.Val, f.ParentID) {
		parent := in.ParentID.Val
		if err := d.parentInProject(ctx, f.ProjectID, parent); err != nil {
			return f, err
		}
		if parent != nil {
			folders, _, err := d.Repo.Tree(ctx, f.ProjectID)
			if err != nil {
				return f, err
			}
			if diagram.SubtreeIDs(folders, f.ID)[*parent] {
				return f, diagram.ErrCycle
			}
		}
		f.ParentID, changed = parent, true
		if in.Position == nil { // a moved folder goes last
			if f.Position, err = d.Repo.NextPosition(ctx, f.ProjectID, parent); err != nil {
				return f, err
			}
		}
	}
	if in.Position != nil {
		changed, f.Position = changed || *in.Position != f.Position, *in.Position
	}
	if !changed {
		return f, nil
	}
	f.UpdatedAt = d.clock()
	if err := d.Repo.UpdateFolder(ctx, f); err != nil {
		return f, err
	}
	_, err = d.touchFor(ctx, f.ProjectID, actor, "folder.updated", f.ID, "")
	return f, err
}

func sameID(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// DeleteFolder removes a folder with everything inside it and returns the
// counts.
func (d *Diagrams) DeleteFolder(ctx context.Context, actor int, id string) (folders, files int, err error) {
	ctx, span := tracer.Start(ctx, "Diagrams.DeleteFolder")
	defer span.End()
	_, f, err := d.folderAccess(ctx, actor, id, diagram.RoleEditor)
	if err != nil {
		return 0, 0, err
	}
	all, inFiles, err := d.Repo.Tree(ctx, f.ProjectID)
	if err != nil {
		return 0, 0, err
	}
	ids := slices.Collect(func(yield func(string) bool) {
		for id := range diagram.SubtreeIDs(all, f.ID) {
			if !yield(id) {
				return
			}
		}
	})
	if files, err = d.Repo.DeleteFolders(ctx, f.ProjectID, ids); err != nil {
		return 0, 0, err
	}
	for _, gone := range inFiles { // their live sessions end
		if gone.FolderID != nil && slices.Contains(ids, *gone.FolderID) {
			d.emit(ctx, DiagramEvent{Kind: EventFileDeleted, ProjectID: f.ProjectID, FileID: gone.ID})
		}
	}
	_, err = d.touchFor(ctx, f.ProjectID, actor, "folder.deleted", f.ID, "")
	return len(ids), files, err
}

// DuplicateFolder deep-copies a folder (subfolders and files, with fresh
// ids and history) next to the original.
func (d *Diagrams) DuplicateFolder(ctx context.Context, actor int, id string) (FolderCopy, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.DuplicateFolder")
	defer span.End()
	_, f, err := d.folderAccess(ctx, actor, id, diagram.RoleEditor)
	if err != nil {
		return FolderCopy{}, err
	}
	folders, files, err := d.Repo.Tree(ctx, f.ProjectID)
	if err != nil {
		return FolderCopy{}, err
	}
	ids := diagram.SubtreeIDs(folders, f.ID)
	now := d.clock()
	idMap := map[string]string{}
	for id := range ids {
		idMap[id] = diagram.NewID(diagram.PrefixFolder)
	}
	var out FolderCopy
	for _, o := range folders {
		if !ids[o.ID] {
			continue
		}
		c := o
		c.ID, c.CreatedAt, c.UpdatedAt = idMap[o.ID], now, now
		if o.ID == f.ID {
			c.ParentID, c.Name = f.ParentID, o.Name+" (copy)"
			if c.Name, err = diagram.CleanName(c.Name, "Untitled"); err != nil {
				return FolderCopy{}, err
			}
			if c.Position, err = d.Repo.NextPosition(ctx, f.ProjectID, f.ParentID); err != nil {
				return FolderCopy{}, err
			}
			out.Folder = c
		} else {
			parent := idMap[*o.ParentID]
			c.ParentID = &parent
		}
		out.Folders = append(out.Folders, c)
	}
	var news []diagram.NewFile
	for _, m := range files {
		if m.FolderID == nil || !ids[*m.FolderID] {
			continue
		}
		src, err := d.Repo.File(ctx, m.ID)
		if err != nil {
			return FolderCopy{}, err
		}
		folder := idMap[*m.FolderID]
		nf, err := d.copyFileTo(ctx, src, &folder, src.Name, src.Position, actor, now)
		if err != nil {
			return FolderCopy{}, err
		}
		news = append(news, nf)
		out.Files = append(out.Files, nf.Doc.File)
	}
	if err := d.Repo.InsertTree(ctx, out.Folders, news); err != nil {
		return FolderCopy{}, err
	}
	_, err = d.touchFor(ctx, f.ProjectID, actor, "folder.created", out.Folder.ID, "")
	return out, err
}

// buildFile validates and prepares a new file (and its first snapshot).
// position is used when in.Position is nil. Private stickies in the
// document are taken out of it: they are returned, by owner, for the caller
// to file with filePrivate once the file is stored.
func (d *Diagrams) buildFile(ctx context.Context, projectID string, w writer, in NewDiagramFile, position float64, now time.Time) (diagram.NewFile, map[int]diagram.PrivateSet, error) {
	actor := w.user
	if in.Kind != "" && !diagram.ValidKind(in.Kind) {
		return diagram.NewFile{}, nil, diagram.Validation("kind: must be one of sdd, erd, uml, board")
	}
	id := in.ID
	if id == "" {
		id = diagram.NewID(diagram.PrefixFile)
	}
	name, err := diagram.CleanName(in.Name, "Untitled")
	if err != nil {
		return diagram.NewFile{}, nil, err
	}
	data := in.Data
	if len(data) == 0 {
		data = diagram.EmptyData(name, now)
	}
	if len(data) > d.MaxDataBytes {
		return diagram.NewFile{}, nil, diagram.TooLarge("The project is too large")
	}
	data, priv := d.scrubPrivate(ctx, data, w)
	prep, err := diagram.Prepare(data, id, name, now, now)
	if err != nil {
		return diagram.NewFile{}, nil, err
	}
	kind := firstNonEmpty(in.Kind, prep.Kind, "")
	if in.Position != nil {
		position = *in.Position
	}
	doc := diagram.Doc{
		File: diagram.File{ID: id, ProjectID: projectID, FolderID: in.FolderID, Name: name, Kind: kind, Position: position,
			Version: 1, PageCount: prep.PageCount, CreatedAt: now, UpdatedAt: now, UpdatedBy: actor, UpdatedByShare: w.share},
		Data: prep.Data,
	}
	first := diagram.Version{Version: 1, Name: name, CreatedBy: actor, CreatedByShare: w.share, CreatedAt: now, Data: prep.Data}
	if in.Label != "" {
		first.Label = &in.Label
	}
	doc.ThumbnailHash = in.ThumbnailHash
	return diagram.NewFile{Doc: doc, First: first}, priv, nil
}

// copyFileTo builds a copy of src in parent (nil: the root) with version 1
// and a history that starts with "Duplicated from".
func (d *Diagrams) copyFileTo(ctx context.Context, src diagram.Doc, parent *string, name string, position float64, actor int, now time.Time) (diagram.NewFile, error) {
	// the source is a stored document: it holds no private stickies
	nf, _, err := d.buildFile(ctx, src.ProjectID, writer{user: actor}, NewDiagramFile{Name: name, Kind: src.Kind, FolderID: parent, Position: &position, Data: src.Data}, position, now)
	if err != nil {
		return nf, err
	}
	nf.Doc.ThumbnailHash = src.ThumbnailHash
	label := "Duplicated from " + src.Name
	nf.First.Label = &label
	return nf, nil
}

// CreateFile adds a file; a client-chosen id makes it idempotent.
func (d *Diagrams) CreateFile(ctx context.Context, actor int, projectID string, in NewDiagramFile) (DiagramFile, bool, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.CreateFile")
	defer span.End()
	a, err := d.projectAccess(ctx, actor, projectID, diagram.RoleEditor)
	if err != nil {
		return DiagramFile{}, false, err
	}
	if err := diagram.CheckClientID(in.ID); err != nil {
		return DiagramFile{}, false, err
	}
	if err := diagram.CheckNameSize(in.Name, true); err != nil {
		return DiagramFile{}, false, err
	}
	if err := d.folderInProject(ctx, projectID, in.FolderID); err != nil {
		return DiagramFile{}, false, err
	}
	if in.ID != "" {
		existing, err := d.Repo.File(ctx, in.ID)
		switch {
		case err == nil && existing.ProjectID == projectID:
			return DiagramFile{existing, a.role}, false, nil
		case err == nil:
			return DiagramFile{}, false, diagram.ErrIDTaken
		case !errors.Is(err, domain.ErrNotFound):
			return DiagramFile{}, false, err
		}
	}
	pos, err := d.position(ctx, projectID, in.FolderID, nil)
	if err != nil {
		return DiagramFile{}, false, err
	}
	now := d.clock()
	nf, priv, err := d.buildFile(ctx, projectID, writer{user: actor, owner: a.project.OwnerID}, in, pos, now)
	if err != nil {
		return DiagramFile{}, false, err
	}
	if err := d.Repo.InsertFile(ctx, nf); err != nil {
		return DiagramFile{}, false, idTaken(err)
	}
	if err := d.filePrivate(ctx, nf.Doc.ID, priv, now); err != nil {
		return DiagramFile{}, false, err
	}
	_, err = d.touchFor(ctx, projectID, actor, "file.created", "", nf.Doc.ID)
	return DiagramFile{nf.Doc, a.role}, true, err
}

// GetFile returns a file with its document.
func (d *Diagrams) GetFile(ctx context.Context, actor int, id string) (DiagramFile, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.GetFile")
	defer span.End()
	a, f, err := d.fileAccess(ctx, actor, id, diagram.RoleViewer)
	return DiagramFile{f, a.role}, err
}

// SaveFile saves a document and/or a new name. ifMatch is the version the
// caller last saw ("" when the header is missing): a stale or missing one
// is a precondition failure carrying the current version. The new
// document becomes version n+1 in the history.
func (d *Diagrams) SaveFile(ctx context.Context, actor int, id string, name *string, data json.RawMessage, ifMatch string) (DiagramFile, error) {
	return d.SaveFileLabeled(ctx, actor, id, name, data, ifMatch, nil)
}

// SaveFileLabeled is SaveFile with a label on the snapshot it makes (the
// public API labels its saves).
func (d *Diagrams) SaveFileLabeled(ctx context.Context, actor int, id string, name *string, data json.RawMessage, ifMatch string, label *string) (DiagramFile, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.SaveFile")
	defer span.End()
	a, f, err := d.fileAccess(ctx, actor, id, diagram.RoleEditor)
	if err != nil {
		return DiagramFile{}, err
	}
	if name == nil && len(data) == 0 {
		return DiagramFile{}, diagram.Validation("Provide name and/or data")
	}
	if name != nil {
		if err := diagram.CheckNameSize(*name, true); err != nil {
			return DiagramFile{}, err
		}
	}
	expected, ok := diagram.ParseIfMatch(ifMatch)
	if !ok {
		return DiagramFile{}, diagram.IfMatchRequired(f.Version)
	}
	doc, err := d.commit(ctx, writer{user: actor, owner: a.project.OwnerID}, f, expected, name, data, label)
	return DiagramFile{doc, a.role}, err
}

// writer is who saves: a person, or a share link (user 0, share set), and
// the project's owner, who takes private stickies of unknown authors.
type writer struct {
	user  int
	share string
	owner int
	// restored marks a save that replaces the document with an old version.
	restored bool
}

// fallbackOwner is who a private sticky of an unknown author is filed under.
func (w writer) fallbackOwner() int {
	if w.user != 0 {
		return w.user
	}
	return w.owner
}

// commit stores a new head for cur if the file is still at version
// expected. Without data the current document is kept.
func (d *Diagrams) commit(ctx context.Context, w writer, cur diagram.Doc, expected int, name *string, data json.RawMessage, label *string) (diagram.Doc, error) {
	actor := w.user
	n := cur.Name
	if name != nil {
		var err error
		if n, err = diagram.CleanName(*name, "Untitled"); err != nil {
			return cur, err
		}
	}
	if len(data) == 0 {
		data = cur.Data
	}
	if len(data) > d.MaxDataBytes {
		return cur, diagram.TooLarge("The project is too large")
	}
	now := d.clock()
	// private stickies never enter the shared document: they go to their
	// author's own store (before the save, as the Node server does)
	data, priv := d.scrubPrivate(ctx, data, w)
	prep, err := diagram.Prepare(data, cur.ID, n, cur.CreatedAt, now)
	if err != nil {
		return cur, err
	}
	if err := d.filePrivate(ctx, cur.ID, priv, now); err != nil {
		return cur, err
	}
	next := cur
	next.Name, next.Kind, next.Data, next.PageCount = n, prep.Kind, prep.Data, prep.PageCount
	next.Version, next.UpdatedAt, next.UpdatedBy, next.UpdatedByShare, next.Restored = expected+1, now, actor, w.share, w.restored
	v := diagram.Version{Version: next.Version, Name: n, Label: label, CreatedBy: actor, CreatedByShare: w.share, CreatedAt: now, Data: prep.Data}
	save := d.Repo.SaveFile
	if d.coalesces(ctx, w, cur, expected, label) {
		save = d.Repo.SaveFileOver
	}
	saved, err := save(ctx, next, expected, v)
	if err != nil {
		return cur, err
	}
	if !saved {
		fresh, err := d.Repo.File(ctx, cur.ID)
		if errors.Is(err, domain.ErrNotFound) {
			return cur, diagram.NotFound("File")
		}
		if err != nil {
			return cur, err
		}
		return cur, diagram.VersionConflict(fresh.Version, fresh.Restored)
	}
	if w.restored {
		d.resetY(ctx, cur.ID) // live editors start over from the restored document
		d.emit(ctx, DiagramEvent{Kind: EventRestored, ProjectID: cur.ProjectID, FileID: cur.ID})
	}
	if d.Events != nil {
		d.emit(ctx, DiagramEvent{Kind: EventFile, ProjectID: cur.ProjectID, FileID: cur.ID, Version: next.Version, Name: n,
			FileKind: next.Kind, UpdatedAt: ms(now), Restored: w.restored, By: d.actorOf(ctx, w)})
	}
	if err := d.prune(ctx, cur.ID, now); err != nil {
		return next, err
	}
	if n != cur.Name {
		if _, err := d.touchFor(ctx, cur.ProjectID, actor, "file.renamed", "", cur.ID); err != nil {
			return next, err
		}
	}
	return next, nil
}

// coalesces says a save of cur may rewrite the head snapshot: it is unnamed
// and unlabeled, came from the same writer, not from a restore, and is
// younger than d.Coalesce.
func (d *Diagrams) coalesces(ctx context.Context, w writer, cur diagram.Doc, expected int, label *string) bool {
	// v1 is the file as created: always kept, so it stays restorable
	if d.Coalesce <= 0 || w.restored || cur.Restored || label != nil || cur.Version != expected || expected <= 1 {
		return false
	}
	if cur.UpdatedBy != w.user || cur.UpdatedByShare != w.share {
		return false
	}
	h, err := d.Repo.VersionInfo(ctx, cur.ID, expected)
	if err != nil {
		return false
	}
	return !h.Named && h.Label == nil && h.CreatedBy == w.user && h.CreatedByShare == w.share &&
		d.clock().Sub(h.CreatedAt) < d.Coalesce
}

// prune drops the snapshots retention no longer keeps.
func (d *Diagrams) prune(ctx context.Context, fileID string, now time.Time) error {
	// the newest max(1, KeepLast) snapshots always survive
	if n, err := d.Repo.CountVersions(ctx, fileID); err == nil && n <= max(1, d.KeepLast) {
		return nil
	}
	vs, err := d.Repo.Versions(ctx, fileID)
	if err != nil {
		return err
	}
	keep := diagram.KeepVersions(vs, now, d.KeepLast, d.KeepDays)
	var drop []int
	for _, v := range vs {
		if !keep[v.Version] {
			drop = append(drop, v.Version)
		}
	}
	if len(drop) == 0 {
		return nil
	}
	return d.Repo.DeleteVersions(ctx, fileID, drop)
}

// UpdateFileMeta renames and/or moves a file. It does not touch the
// document or its version: tree operations are last-writer-wins.
func (d *Diagrams) UpdateFileMeta(ctx context.Context, actor int, id string, in FilePatch) (diagram.File, diagram.Role, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.UpdateFileMeta")
	defer span.End()
	a, doc, err := d.fileAccess(ctx, actor, id, diagram.RoleEditor)
	f := doc.File
	if err != nil {
		return f, a.role, err
	}
	changed := false
	if in.Name != nil {
		if err := diagram.CheckNameSize(*in.Name, true); err != nil {
			return f, a.role, err
		}
		name, err := diagram.CleanName(*in.Name, f.Name)
		if err != nil {
			return f, a.role, err
		}
		changed, f.Name = changed || name != f.Name, name
	}
	if in.Kind != nil {
		if !diagram.ValidKind(*in.Kind) {
			return f, a.role, diagram.Validation("kind: must be one of sdd, erd, uml, board")
		}
		changed, f.Kind = changed || *in.Kind != f.Kind, *in.Kind
	}
	if in.FolderID.Set {
		if err := d.folderInProject(ctx, f.ProjectID, in.FolderID.Val); err != nil {
			return f, a.role, err
		}
		if !sameID(in.FolderID.Val, f.FolderID) {
			f.FolderID, changed = in.FolderID.Val, true
			if in.Position == nil { // a moved file goes last
				if f.Position, err = d.Repo.NextPosition(ctx, f.ProjectID, f.FolderID); err != nil {
					return f, a.role, err
				}
			}
		}
	}
	if in.Position != nil {
		changed, f.Position = changed || *in.Position != f.Position, *in.Position
	}
	if !changed {
		return f, a.role, nil
	}
	f.UpdatedAt = d.clock()
	if err := d.Repo.UpdateFileMeta(ctx, f); err != nil {
		return f, a.role, err
	}
	_, err = d.touchFor(ctx, f.ProjectID, actor, "file.updated", "", f.ID)
	return f, a.role, err
}

// DeleteFile removes a file with its history.
func (d *Diagrams) DeleteFile(ctx context.Context, actor int, id string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.DeleteFile")
	defer span.End()
	_, f, err := d.fileAccess(ctx, actor, id, diagram.RoleEditor)
	if err != nil {
		return err
	}
	if err := d.Repo.DeleteFile(ctx, id); err != nil {
		return err
	}
	d.emit(ctx, DiagramEvent{Kind: EventFileDeleted, ProjectID: f.ProjectID, FileID: id})
	_, err = d.touchFor(ctx, f.ProjectID, actor, "file.deleted", "", id)
	return err
}

// DuplicateFile copies a file next to the original, or into folder. The
// copy lands in the project, so the caller needs write access to it, not
// just to the source file.
func (d *Diagrams) DuplicateFile(ctx context.Context, actor int, id string, folder diagram.Opt[*string]) (DiagramFile, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.DuplicateFile")
	defer span.End()
	_, f, err := d.fileAccess(ctx, actor, id, diagram.RoleViewer)
	if err != nil {
		return DiagramFile{}, err
	}
	pa, err := d.projectAccess(ctx, actor, f.ProjectID, diagram.RoleEditor)
	if err != nil {
		return DiagramFile{}, err
	}
	parent := f.FolderID
	if folder.Set {
		parent = folder.Val
	}
	if err := d.parentInProject(ctx, f.ProjectID, parent); err != nil {
		return DiagramFile{}, err
	}
	pos, err := d.Repo.NextPosition(ctx, f.ProjectID, parent)
	if err != nil {
		return DiagramFile{}, err
	}
	nf, err := d.copyFileTo(ctx, f, parent, f.Name+" (copy)", pos, actor, d.clock())
	if err != nil {
		return DiagramFile{}, err
	}
	if err := d.Repo.InsertFile(ctx, nf); err != nil {
		return DiagramFile{}, err
	}
	_, err = d.touchFor(ctx, f.ProjectID, actor, "file.created", "", nf.Doc.ID)
	return DiagramFile{nf.Doc, pa.role}, err
}

// Versions lists a file's snapshots, newest first.
func (d *Diagrams) Versions(ctx context.Context, actor int, fileID string) ([]diagram.Version, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.Versions")
	defer span.End()
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleViewer); err != nil {
		return nil, err
	}
	return d.Repo.Versions(ctx, fileID)
}

// Version returns one snapshot with its document.
func (d *Diagrams) Version(ctx context.Context, actor int, fileID string, n int) (diagram.Version, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.Version")
	defer span.End()
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleViewer); err != nil {
		return diagram.Version{}, err
	}
	return d.versionOf(ctx, fileID, n)
}

func (d *Diagrams) versionOf(ctx context.Context, fileID string, n int) (diagram.Version, error) {
	v, err := d.Repo.GetVersion(ctx, fileID, n)
	if errors.Is(err, domain.ErrNotFound) {
		return v, diagram.NotFound("Version")
	}
	return v, err
}

// MaxVersionLabel is the longest name of a named version.
const MaxVersionLabel = 120

// SaveNamedVersion names the file as it is now ("Save version..."); named
// versions are never pruned. Editors only.
func (d *Diagrams) SaveNamedVersion(ctx context.Context, actor int, fileID, label string) (diagram.Version, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.SaveNamedVersion")
	defer span.End()
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleEditor); err != nil {
		return diagram.Version{}, err
	}
	label = strings.TrimSpace(label)
	if label == "" || len([]rune(label)) > MaxVersionLabel {
		return diagram.Version{}, diagram.Validation("name must be 1-120 characters")
	}
	return d.Repo.NameHead(ctx, fileID, label, actor, d.clock())
}

// RestoreVersion saves an old snapshot as a NEW version on top of the
// current head.
func (d *Diagrams) RestoreVersion(ctx context.Context, actor int, fileID string, n int) (DiagramFile, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.RestoreVersion")
	defer span.End()
	a, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleEditor)
	if err != nil {
		return DiagramFile{}, err
	}
	old, err := d.versionOf(ctx, fileID, n)
	if err != nil {
		return DiagramFile{}, err
	}
	label := "Restored from v" + itoa(old.Version)
	// no If-Match: restore builds on whatever the head is; retry when it moves
	for attempt := 0; ; attempt++ {
		head, err := d.Repo.File(ctx, fileID)
		if errors.Is(err, domain.ErrNotFound) {
			return DiagramFile{}, diagram.NotFound("File")
		}
		if err != nil {
			return DiagramFile{}, err
		}
		doc, err := d.commit(ctx, writer{user: actor, owner: a.project.OwnerID, restored: true}, head, head.Version, nil, old.Data, &label)
		if errors.Is(err, diagram.ErrPrecondition) && attempt < 4 {
			continue
		}
		return DiagramFile{doc, a.role}, err
	}
}

// GetPrefs returns the caller's preferences object.
func (d *Diagrams) GetPrefs(ctx context.Context, actor int) (json.RawMessage, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.GetPrefs")
	defer span.End()
	return d.Prefs.Prefs(ctx, actor)
}

// PatchPrefs merges top-level keys into the caller's preferences; a null
// value deletes the key.
func (d *Diagrams) PatchPrefs(ctx context.Context, actor int, patch json.RawMessage) (json.RawMessage, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.PatchPrefs")
	defer span.End()
	return d.Prefs.UpdatePrefs(ctx, actor, d.clock(), func(cur json.RawMessage) (json.RawMessage, error) {
		return diagram.MergePrefs(cur, patch)
	})
}

// ms is a time as the API reports it: epoch milliseconds.
func ms(t time.Time) int64 { return t.UnixMilli() }

// RealtimeCutoff ends the live realtime sessions that rested on access which
// was just revoked (application.MQTTAccess).
type RealtimeCutoff interface {
	// ShareRevoked cuts the guests that connected with the share link.
	ShareRevoked(ctx context.Context, projectID, shareID string)
	// MemberLost cuts a member's sessions in the project (removed, or role lowered).
	MemberLost(ctx context.Context, projectID string, userID int)
}

// CutExpiredShares cuts the guests of links that expired within the time a
// guest connection is remembered: the broker checks a subscription only when
// it is made, so a live session would otherwise outlast its link. Links
// already cut have no clients left, so running it again is harmless.
func (d *Diagrams) CutExpiredShares(ctx context.Context) error {
	if d.Cutoff == nil {
		return nil
	}
	now := d.clock()
	links, err := d.Sharing.SharesExpiredBetween(ctx, now.Add(-MQTTGuestTTL), now)
	if err != nil {
		return err
	}
	for _, l := range links {
		d.Cutoff.ShareRevoked(ctx, l.ProjectID, l.ID)
	}
	return nil
}

func (d *Diagrams) shareRevoked(ctx context.Context, projectID, shareID string) {
	if d.Cutoff != nil {
		d.Cutoff.ShareRevoked(ctx, projectID, shareID)
	}
}

func (d *Diagrams) memberLost(ctx context.Context, projectID string, userID int) {
	if d.Cutoff != nil {
		d.Cutoff.MemberLost(ctx, projectID, userID)
	}
}
