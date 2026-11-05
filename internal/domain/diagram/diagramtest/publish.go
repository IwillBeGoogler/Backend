package diagramtest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// Blog pages (diagram.PublishRepository), the admin view
// (diagram.AdminRepository) and the template gallery.

// SetUser names an account, so owners and authors show up in listings.
func (m *Memory) SetUser(id int, username string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.names[id] = username
}

func (m *Memory) SetPublication(_ context.Context, fileID string, p diagram.Publication) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[fileID]; !ok {
		return domain.ErrNotFound
	}
	for id, o := range m.pubs {
		if id != fileID && o.Slug == p.Slug {
			return domain.ErrConflict
		}
	}
	m.pubs[fileID] = p
	return nil
}

func (m *Memory) ClearPublication(_ context.Context, fileID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[fileID]; !ok {
		return domain.ErrNotFound
	}
	p := m.pubs[fileID]
	p.Visibility, p.PublishedAt = diagram.VisPrivate, time.Time{}
	m.pubs[fileID] = p
	return nil
}

// listing builds a file's page; the caller holds the lock.
func (m *Memory) listing(fileID string) (diagram.Listing, bool) {
	f, ok := m.files[fileID]
	if !ok {
		return diagram.Listing{}, false
	}
	p := m.pubs[fileID]
	if p.Visibility == "" {
		p.Visibility = diagram.VisPrivate
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	owner := m.projects[f.ProjectID].OwnerID
	total := 0
	for k, n := range m.claps {
		if strings.HasPrefix(k, fileID+"|") {
			total += n
		}
	}
	return diagram.Listing{Publication: p, FileID: f.ID, ProjectID: f.ProjectID, Name: f.Name, Kind: f.Kind, Version: f.Version,
		PageCount: f.PageCount, ThumbnailHash: f.ThumbnailHash, UpdatedAt: f.UpdatedAt, Claps: total,
		AuthorID: owner, AuthorUsername: m.names[owner]}, true
}

func (m *Memory) Listing(_ context.Context, fileID string) (diagram.Listing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.listing(fileID)
	if !ok {
		return l, domain.ErrNotFound
	}
	return l, nil
}

func (m *Memory) PublicBySlug(_ context.Context, slug string) (diagram.PublicDoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.pubs {
		if p.Slug == slug && p.Visibility.Published() {
			l, _ := m.listing(id)
			return diagram.PublicDoc{Listing: l, Data: m.files[id].Data}, nil
		}
	}
	return diagram.PublicDoc{}, domain.ErrNotFound
}

func (m *Memory) ListPublic(_ context.Context, q diagram.PublicQuery) ([]diagram.Listing, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []diagram.Listing
	for id, p := range m.pubs {
		if p.Visibility != diagram.VisPublic {
			continue
		}
		l, _ := m.listing(id)
		hay := strings.ToLower(l.Title + "\n" + l.Description)
		switch {
		case q.Q != "" && !strings.Contains(hay, strings.ToLower(q.Q)):
		case q.Tag != "" && !slices.Contains(l.Tags, q.Tag):
		case q.AuthorID != 0 && l.AuthorID != q.AuthorID:
		default:
			all = append(all, l)
		}
	}
	slices.SortFunc(all, func(a, b diagram.Listing) int {
		if c := b.PublishedAt.Compare(a.PublishedAt); c != 0 {
			return c
		}
		return strings.Compare(a.FileID, b.FileID)
	})
	total := len(all)
	from := min((max(q.Page, 1)-1)*q.Limit, total)
	return all[from:min(from+q.Limit, total)], total, nil
}

func (m *Memory) ListOwned(_ context.Context, userID, page, limit int) ([]diagram.Listing, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []diagram.Listing
	for id, p := range m.pubs {
		if !p.Visibility.Published() {
			continue
		}
		if l, ok := m.listing(id); ok && l.AuthorID == userID {
			all = append(all, l)
		}
	}
	slices.SortFunc(all, func(a, b diagram.Listing) int {
		if c := b.PublishedAt.Compare(a.PublishedAt); c != 0 {
			return c
		}
		return strings.Compare(a.FileID, b.FileID)
	})
	total := len(all)
	from := min((max(page, 1)-1)*limit, total)
	return all[from:min(from+limit, total)], total, nil
}

func (m *Memory) ReferencedPublic(_ context.Context, refs []string) (map[string]diagram.Listing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]diagram.Listing{}
	for _, ref := range refs {
		for id, p := range m.pubs {
			if (id == ref || p.Slug == ref) && p.Visibility.Published() {
				l, _ := m.listing(id)
				out[ref] = l
			}
		}
	}
	return out, nil
}

// Admin

func (m *Memory) Stats(_ context.Context, since time.Time) (diagram.AdminStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := diagram.AdminStats{Projects: len(m.projects), Files: len(m.files), FilesByKind: map[string]int{}}
	for _, f := range m.files {
		st.FilesByKind[f.Kind]++
		st.StorageBytes += int64(len(f.Data))
	}
	editors := map[int]bool{}
	for _, vs := range m.versions {
		for _, v := range vs {
			st.StorageBytes += int64(len(v.Data))
			if v.CreatedBy != 0 && !v.CreatedAt.Before(since) {
				editors[v.CreatedBy] = true
			}
		}
	}
	st.ActiveEditors7d = len(editors)
	return st, nil
}

func (m *Memory) adminProject(p diagram.Project) diagram.AdminProject {
	out := diagram.AdminProject{ID: p.ID, Name: p.Name, Owner: diagram.AdminUser{ID: p.OwnerID, Username: m.names[p.OwnerID]},
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	out.Files, _ = m.counts(p.ID)
	out.ThumbFileID, out.ThumbnailHash = m.thumb(p.ID)
	now := time.Now()
	for _, s := range m.shares {
		if s.ProjectID == p.ID && s.RevokedAt == nil && !s.Expired(now) {
			out.Shared = true
		}
	}
	return out
}

func (m *Memory) Projects(_ context.Context, q diagram.AdminProjectQuery) ([]diagram.AdminProject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []diagram.AdminProject
	for _, p := range m.projects {
		a := m.adminProject(p)
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(q.Search)) &&
			(q.Owner == "" || strings.EqualFold(a.Owner.Username, q.Owner)) {
			all = append(all, a)
		}
	}
	slices.SortFunc(all, func(a, b diagram.AdminProject) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	from := min(q.Offset, len(all))
	return all[from:min(from+q.Limit+1, len(all))], nil
}

func (m *Memory) ProjectDetail(_ context.Context, id string) (diagram.AdminProjectDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return diagram.AdminProjectDetail{}, domain.ErrNotFound
	}
	d := diagram.AdminProjectDetail{AdminProject: m.adminProject(p)}
	for _, f := range m.files {
		if f.ProjectID == id {
			d.FileList = append(d.FileList, diagram.AdminFile{ID: f.ID, Name: f.Name, Kind: f.Kind, Version: f.Version,
				UpdatedAt: f.UpdatedAt, ThumbnailHash: f.ThumbnailHash})
		}
	}
	slices.SortFunc(d.FileList, func(a, b diagram.AdminFile) int { return strings.Compare(a.ID, b.ID) })
	for k, role := range m.members {
		if pid, uid, ok := strings.Cut(k, "|"); ok && pid == id {
			n := 0
			for _, c := range uid {
				n = n*10 + int(c-'0')
			}
			d.Members = append(d.Members, diagram.AdminMember{User: diagram.AdminUser{ID: n, Username: m.names[n]}, Role: role, JoinedAt: m.memberAt[k]})
		}
	}
	slices.SortFunc(d.Members, func(a, b diagram.AdminMember) int { return a.User.ID - b.User.ID })
	for _, s := range m.shares {
		if s.ProjectID == id {
			d.Shares = append(d.Shares, diagram.AdminShare{ID: s.ID, Role: s.Role, CreatedBy: m.names[s.CreatedBy],
				CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, RevokedAt: s.RevokedAt})
		}
	}
	slices.SortFunc(d.Shares, func(a, b diagram.AdminShare) int { return strings.Compare(a.ID, b.ID) })
	return d, nil
}

func (m *Memory) RevokeShareByID(_ context.Context, id string, at time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.shares[id]
	if !ok {
		return "", domain.ErrNotFound
	}
	if s.RevokedAt == nil {
		s.RevokedAt = &at
		m.shares[id] = s
	}
	return s.ProjectID, nil
}

// Templates is an in-memory diagram.TemplateRepository; a template is read
// with its file's preview, as in Postgres, and goes with its file.
type Templates struct {
	mem   *Memory
	mu    sync.Mutex
	items map[string]diagram.Template
}

func NewTemplates(mem *Memory) *Templates {
	return &Templates{mem: mem, items: map[string]diagram.Template{}}
}

// fill adds the file's preview; false when the file is gone.
func (t *Templates) fill(tp diagram.Template) (diagram.Template, bool) {
	t.mem.mu.Lock()
	defer t.mem.mu.Unlock()
	f, ok := t.mem.files[tp.FileID]
	if !ok {
		return tp, false
	}
	tp.ThumbnailHash, tp.PageCount = f.ThumbnailHash, f.PageCount
	return tp, true
}

func (t *Templates) List(_ context.Context, onlyPublished bool) ([]diagram.Template, error) {
	t.mu.Lock()
	var all []diagram.Template
	for _, tp := range t.items {
		if !onlyPublished || tp.Published {
			all = append(all, tp)
		}
	}
	t.mu.Unlock()
	var out []diagram.Template
	for _, tp := range all {
		if tp, ok := t.fill(tp); ok {
			out = append(out, tp)
		}
	}
	slices.SortFunc(out, func(a, b diagram.Template) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		if c := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (t *Templates) Get(_ context.Context, id string) (diagram.Template, error) {
	t.mu.Lock()
	tp, ok := t.items[id]
	t.mu.Unlock()
	if !ok {
		return tp, domain.ErrNotFound
	}
	tp, ok = t.fill(tp)
	if !ok {
		return tp, domain.ErrNotFound
	}
	return tp, nil
}

func (t *Templates) Create(_ context.Context, tp diagram.Template) error {
	if _, ok := t.fill(tp); !ok {
		return domain.ErrConflict
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.items[tp.ID]; ok {
		return domain.ErrConflict
	}
	t.items[tp.ID] = tp
	return nil
}

func (t *Templates) Update(_ context.Context, tp diagram.Template) error {
	if _, ok := t.fill(tp); !ok {
		return domain.ErrConflict
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	cur, ok := t.items[tp.ID]
	if !ok {
		return domain.ErrNotFound
	}
	tp.CreatedAt = cur.CreatedAt
	t.items[tp.ID] = tp
	return nil
}

func (t *Templates) Delete(_ context.Context, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(t.items, id)
	return nil
}

func (t *Templates) Reorder(ctx context.Context, ids []string, at time.Time) error {
	cur, _ := t.List(ctx, false)
	t.mu.Lock()
	defer t.mu.Unlock()
	have := map[string]bool{}
	for _, tp := range cur {
		have[tp.ID] = true
	}
	var order []string
	placed := map[string]bool{}
	for _, id := range ids {
		if !have[id] {
			return domain.ErrNotFound
		}
		if !placed[id] {
			placed[id] = true
			order = append(order, id)
		}
	}
	for _, tp := range cur {
		if !placed[tp.ID] {
			order = append(order, tp.ID)
		}
	}
	for i, id := range order {
		tp := t.items[id]
		tp.Position, tp.UpdatedAt = i, at
		t.items[id] = tp
	}
	return nil
}

var (
	_ diagram.PublishRepository    = (*Memory)(nil)
	_ diagram.EngagementRepository = (*Memory)(nil)
	_ diagram.AdminRepository      = (*Memory)(nil)
	_ diagram.TemplateRepository   = (*Templates)(nil)
)
