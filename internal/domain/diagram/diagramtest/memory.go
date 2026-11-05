// Package diagramtest is an in-memory diagram.Repository and
// diagram.PrefsRepository for tests that need no database.
package diagramtest

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// Memory keeps everything in maps; it mirrors the Postgres repository's
// rules (unique ids, cascading deletes, compare-and-swap saves).
type Memory struct {
	mu       sync.Mutex
	projects map[string]diagram.Project
	folders  map[string]diagram.Folder
	files    map[string]diagram.Doc
	versions map[string][]diagram.Version // by file
	members  map[string]diagram.Role      // "project|user"
	overs    map[string]diagram.Role      // "file|user"
	prefs    map[int]json.RawMessage

	// sharing (see sharing.go)
	memberAt map[string]time.Time // "project|user" and "file|user"
	shares   map[string]diagram.ShareLink
	assets   map[string]diagram.Asset
	libs     map[string]diagram.Library
	privs    map[string]diagram.Private // "file|user"

	// comments (see comments.go)
	comments map[string]diagram.Comment
	reads    map[string]time.Time // "file|user"

	// Yjs state (see y.go)
	ys map[string]*yfile

	// tokens, webhooks and the MCP audit trail (see integrations.go)
	tokens     map[string]diagram.AccessToken
	hooks      map[string]diagram.Webhook
	deliveries map[string]diagram.Delivery
	audits     []diagram.MCPAudit

	// claps and bookmarks (see engagement.go), keyed "file|user"
	claps     map[string]int
	bookmarks map[string]time.Time

	// blog pages and account names (see publish.go)
	pubs  map[string]diagram.Publication
	names map[int]string
}

func NewMemory() *Memory {
	return &Memory{projects: map[string]diagram.Project{}, folders: map[string]diagram.Folder{}, files: map[string]diagram.Doc{},
		versions: map[string][]diagram.Version{}, members: map[string]diagram.Role{}, overs: map[string]diagram.Role{},
		prefs: map[int]json.RawMessage{}, memberAt: map[string]time.Time{}, shares: map[string]diagram.ShareLink{},
		assets: map[string]diagram.Asset{}, libs: map[string]diagram.Library{}, privs: map[string]diagram.Private{},
		comments: map[string]diagram.Comment{}, reads: map[string]time.Time{},
		pubs: map[string]diagram.Publication{}, names: map[int]string{},
		tokens: map[string]diagram.AccessToken{}, hooks: map[string]diagram.Webhook{}, deliveries: map[string]diagram.Delivery{}}
}

func key(id string, user int) string { return id + "|" + strconv.Itoa(user) }

// AddMember makes user a member of a project.
func (m *Memory) AddMember(projectID string, userID int, role diagram.Role) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.members[key(projectID, userID)] = role
}

// SetOverride sets a per-file role override.
func (m *Memory) SetOverride(fileID string, userID int, role diagram.Role) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overs[key(fileID, userID)] = role
}

func (m *Memory) CreateProject(_ context.Context, p diagram.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[p.ID]; ok {
		return domain.ErrConflict
	}
	m.projects[p.ID] = p
	return nil
}

func (m *Memory) Project(_ context.Context, id string) (diagram.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return p, domain.ErrNotFound
	}
	return p, nil
}

func (m *Memory) counts(id string) (files, folders int) {
	for _, f := range m.files {
		if f.ProjectID == id {
			files++
		}
	}
	for _, f := range m.folders {
		if f.ProjectID == id {
			folders++
		}
	}
	return
}

func (m *Memory) ListProjects(_ context.Context, userID int, q diagram.ProjectQuery) ([]diagram.ProjectSummary, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []diagram.ProjectSummary
	for _, p := range m.projects {
		role := m.members[key(p.ID, userID)]
		if p.OwnerID == userID {
			role = diagram.RoleOwner
		}
		if role == "" || !strings.Contains(strings.ToLower(p.Name), strings.ToLower(q.Search)) {
			continue
		}
		s := diagram.ProjectSummary{Project: p, Role: role}
		s.FileCount, s.FolderCount = m.counts(p.ID)
		s.ThumbFileID, s.ThumbnailHash = m.thumb(p.ID)
		all = append(all, s)
	}
	slices.SortFunc(all, func(a, b diagram.ProjectSummary) int {
		var c int
		if q.SortByName {
			c = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		} else {
			c = a.UpdatedAt.Compare(b.UpdatedAt)
		}
		if !q.Asc {
			c = -c
		}
		if c == 0 {
			c = strings.Compare(a.ID, b.ID)
		}
		return c
	})
	total := len(all)
	lo := min((q.Page-1)*q.PageSize, total)
	return all[lo:min(lo+q.PageSize, total)], total, nil
}

func (m *Memory) Counts(_ context.Context, id string) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, d := m.counts(id)
	return f, d, nil
}

// thumb is the preview of a project's first file (by position, then name).
func (m *Memory) thumb(id string) (string, *string) {
	var first *diagram.Doc
	for _, f := range m.files {
		if f.ProjectID != id {
			continue
		}
		if first == nil || f.Position < first.Position || f.Position == first.Position && f.Name < first.Name {
			f := f
			first = &f
		}
	}
	if first == nil {
		return "", nil
	}
	return first.ID, first.ThumbnailHash
}

func (m *Memory) ProjectThumb(_ context.Context, id string) (string, *string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fid, hash := m.thumb(id)
	return fid, hash, nil
}

func (m *Memory) RenameProject(_ context.Context, id, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return domain.ErrNotFound
	}
	p.Name, p.UpdatedAt = name, at
	m.projects[id] = p
	return nil
}

func (m *Memory) TouchTree(_ context.Context, id string, by int, at time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return 0, domain.ErrNotFound
	}
	p.TreeRev++
	p.UpdatedAt, p.UpdatedBy = at, by
	m.projects[id] = p
	return p.TreeRev, nil
}

func (m *Memory) DeleteProject(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.projects, id)
	delete(m.libs, id)
	for sid, sl := range m.shares {
		if sl.ProjectID == id {
			delete(m.shares, sid)
		}
	}
	for k := range m.members {
		if strings.HasPrefix(k, id+"|") {
			delete(m.members, k)
		}
	}
	for fid, f := range m.folders {
		if f.ProjectID == id {
			delete(m.folders, fid)
		}
	}
	for fid, f := range m.files {
		if f.ProjectID == id {
			m.deleteFile(fid)
		}
	}
	return nil
}

func (m *Memory) MemberRole(_ context.Context, projectID string, userID int) (diagram.Role, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.members[key(projectID, userID)], nil
}

func (m *Memory) FileRole(_ context.Context, fileID string, userID int) (diagram.Role, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.overs[key(fileID, userID)], nil
}

func (m *Memory) Tree(_ context.Context, projectID string) ([]diagram.Folder, []diagram.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var folders []diagram.Folder
	var files []diagram.File
	for _, f := range m.folders {
		if f.ProjectID == projectID {
			folders = append(folders, f)
		}
	}
	for _, f := range m.files {
		if f.ProjectID == projectID {
			files = append(files, f.File)
		}
	}
	slices.SortFunc(folders, func(a, b diagram.Folder) int { return cmpPos(a.Position, b.Position, a.Name, b.Name, a.ID, b.ID) })
	slices.SortFunc(files, func(a, b diagram.File) int { return cmpPos(a.Position, b.Position, a.Name, b.Name, a.ID, b.ID) })
	return folders, files, nil
}

func cmpPos(pa, pb float64, na, nb, ia, ib string) int {
	switch {
	case pa < pb:
		return -1
	case pa > pb:
		return 1
	}
	if c := strings.Compare(na, nb); c != 0 {
		return c
	}
	return strings.Compare(ia, ib)
}

func (m *Memory) Folder(_ context.Context, id string) (diagram.Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.folders[id]
	if !ok {
		return f, domain.ErrNotFound
	}
	return f, nil
}

func (m *Memory) CreateFolder(_ context.Context, f diagram.Folder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.folders[f.ID]; ok {
		return domain.ErrConflict
	}
	m.folders[f.ID] = f
	return nil
}

func (m *Memory) UpdateFolder(_ context.Context, f diagram.Folder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.folders[f.ID]; !ok {
		return domain.ErrNotFound
	}
	m.folders[f.ID] = f
	return nil
}

func same(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func (m *Memory) NextPosition(_ context.Context, projectID string, parent *string) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next, found := 0.0, false
	for _, f := range m.files {
		if f.ProjectID == projectID && same(f.FolderID, parent) && (!found || f.Position+1 > next) {
			next, found = f.Position+1, true
		}
	}
	for _, f := range m.folders {
		if f.ProjectID == projectID && same(f.ParentID, parent) && (!found || f.Position+1 > next) {
			next, found = f.Position+1, true
		}
	}
	return next, nil
}

func (m *Memory) DeleteFolders(_ context.Context, projectID string, ids []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for fid, f := range m.files {
		if f.ProjectID == projectID && f.FolderID != nil && slices.Contains(ids, *f.FolderID) {
			m.deleteFile(fid)
			n++
		}
	}
	for _, id := range ids {
		delete(m.folders, id)
	}
	return n, nil
}

func (m *Memory) deleteFile(id string) {
	delete(m.files, id)
	delete(m.pubs, id)
	delete(m.versions, id)
	delete(m.ys, id)
	for k := range m.overs {
		if strings.HasPrefix(k, id+"|") {
			delete(m.overs, k)
		}
	}
	for k := range m.privs {
		if strings.HasPrefix(k, id+"|") {
			delete(m.privs, k)
		}
	}
	for k := range m.reads {
		if strings.HasPrefix(k, id+"|") {
			delete(m.reads, k)
		}
	}
	for cid, c := range m.comments {
		if c.FileID == id {
			delete(m.comments, cid)
		}
	}
	for sid, sl := range m.shares {
		if sl.FileID != nil && *sl.FileID == id {
			delete(m.shares, sid)
		}
	}
}

func (m *Memory) InsertTree(ctx context.Context, folders []diagram.Folder, files []diagram.NewFile) error {
	for _, f := range folders {
		if err := m.CreateFolder(ctx, f); err != nil {
			return err
		}
	}
	for _, f := range files {
		if err := m.InsertFile(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

func (m *Memory) File(_ context.Context, id string) (diagram.Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[id]
	if !ok {
		return f, domain.ErrNotFound
	}
	return f, nil
}

func (m *Memory) InsertFile(_ context.Context, f diagram.NewFile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[f.Doc.ID]; ok {
		return domain.ErrConflict
	}
	m.files[f.Doc.ID] = f.Doc
	m.versions[f.Doc.ID] = []diagram.Version{f.First}
	return nil
}

func (m *Memory) UpdateFileMeta(_ context.Context, f diagram.File) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.files[f.ID]
	if !ok {
		return domain.ErrNotFound
	}
	d.Name, d.Kind, d.FolderID, d.Position, d.UpdatedAt = f.Name, f.Kind, f.FolderID, f.Position, f.UpdatedAt
	m.files[f.ID] = d
	return nil
}

func (m *Memory) SaveFile(_ context.Context, f diagram.Doc, expected int, v diagram.Version) (bool, error) {
	return m.saveFile(f, expected, v, false)
}

func (m *Memory) SaveFileOver(_ context.Context, f diagram.Doc, expected int, v diagram.Version) (bool, error) {
	return m.saveFile(f, expected, v, true)
}

func (m *Memory) saveFile(f diagram.Doc, expected int, v diagram.Version, over bool) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.files[f.ID]
	if !ok || cur.Version != expected {
		return false, nil
	}
	m.files[f.ID] = f
	if over {
		vs := m.versions[f.ID]
		for i := range vs {
			if vs[i].Version == expected && !vs[i].Named && vs[i].Label == nil {
				vs[i] = v
				return true, nil
			}
		}
	}
	m.versions[f.ID] = append(m.versions[f.ID], v)
	return true, nil
}

func (m *Memory) DeleteFile(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[id]; !ok {
		return domain.ErrNotFound
	}
	m.deleteFile(id)
	return nil
}

func (m *Memory) Versions(_ context.Context, fileID string) ([]diagram.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.versions[fileID])
	for i := range out {
		out[i].Data = nil
	}
	slices.SortFunc(out, func(a, b diagram.Version) int { return b.Version - a.Version })
	return out, nil
}

func (m *Memory) GetVersion(_ context.Context, fileID string, n int) (diagram.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.versions[fileID] {
		if v.Version == n {
			return v, nil
		}
	}
	return diagram.Version{}, domain.ErrNotFound
}

func (m *Memory) VersionInfo(ctx context.Context, fileID string, n int) (diagram.Version, error) {
	v, err := m.GetVersion(ctx, fileID, n)
	v.Data = nil
	return v, err
}

func (m *Memory) CountVersions(_ context.Context, fileID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.versions[fileID]), nil
}

func (m *Memory) NameHead(_ context.Context, fileID, label string, by int, at time.Time) (diagram.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[fileID]
	if !ok {
		return diagram.Version{}, domain.ErrNotFound
	}
	vs := m.versions[fileID]
	for i := range vs {
		if vs[i].Version == f.Version {
			vs[i].Label, vs[i].Named = &label, true
			out := vs[i]
			out.Data = nil
			return out, nil
		}
	}
	v := diagram.Version{Version: f.Version, Name: f.Name, Label: &label, Named: true, CreatedBy: by, CreatedAt: at, Data: f.Data}
	m.versions[fileID] = append(vs, v)
	v.Data = nil
	return v, nil
}

func (m *Memory) DeleteVersions(_ context.Context, fileID string, drop []int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.versions[fileID] = slices.DeleteFunc(m.versions[fileID], func(v diagram.Version) bool { return slices.Contains(drop, v.Version) })
	return nil
}

func (m *Memory) Prefs(_ context.Context, userID int) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.prefs[userID]; ok {
		return p, nil
	}
	return json.RawMessage(`{}`), nil
}

func (m *Memory) UpdatePrefs(_ context.Context, userID int, _ time.Time, apply func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.prefs[userID]
	if !ok {
		cur = json.RawMessage(`{}`)
	}
	next, err := apply(cur)
	if err != nil {
		return nil, err
	}
	m.prefs[userID] = next
	return next, nil
}

var (
	_ diagram.Repository      = (*Memory)(nil)
	_ diagram.PrefsRepository = (*Memory)(nil)
)
