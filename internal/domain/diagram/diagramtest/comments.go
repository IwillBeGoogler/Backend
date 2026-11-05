package diagramtest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
)

// Comments (diagram.CommentRepository).

func (m *Memory) Comment(_ context.Context, id string) (diagram.Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.comments[id]
	if !ok {
		return c, domain.ErrNotFound
	}
	return c, nil
}

func (m *Memory) Comments(_ context.Context, fileID string) ([]diagram.Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []diagram.Comment
	for _, c := range m.comments {
		if c.FileID == fileID {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b diagram.Comment) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m *Memory) InsertComment(_ context.Context, c diagram.Comment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[c.FileID]; !ok {
		return domain.ErrNotFound
	}
	if _, ok := m.comments[c.ID]; ok {
		return domain.ErrConflict
	}
	m.comments[c.ID] = c
	return nil
}

func (m *Memory) UpdateComment(_ context.Context, c diagram.Comment, setBody, setResolved bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.comments[c.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if setBody {
		cur.Body = c.Body
	}
	if setResolved {
		cur.ResolvedAt, cur.ResolvedBy = c.ResolvedAt, c.ResolvedBy
	}
	cur.UpdatedAt = c.UpdatedAt
	m.comments[c.ID] = cur
	return nil
}

func (m *Memory) DeleteComment(_ context.Context, id string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.comments[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	ids := []string{c.ID}
	if c.ParentID == nil {
		for rid, r := range m.comments {
			if r.ParentID != nil && *r.ParentID == id {
				ids = append(ids, rid)
			}
		}
	}
	for _, d := range ids {
		delete(m.comments, d)
	}
	return ids, nil
}

func (m *Memory) ReadAt(_ context.Context, fileID string, userID int) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reads[key(fileID, userID)], nil
}

func (m *Memory) MarkRead(_ context.Context, fileID string, userID int, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[fileID]; !ok {
		return domain.ErrNotFound
	}
	m.reads[key(fileID, userID)] = at
	return nil
}

var _ diagram.CommentRepository = (*Memory)(nil)

// Notices is an in-memory stand-in for the blog's notification system
// (application.DiagramNotices): it stores what the diagram use cases send.
type Notices struct {
	mu    sync.Mutex
	next  int64
	items []social.Notification
}

func NewNotices() *Notices { return &Notices{} }

// NotifyDiagram stores a notification for every recipient but the actor.
func (n *Notices) NotifyDiagram(_ context.Context, x social.Notification, recipients ...int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, id := range recipients {
		if id == 0 || id == x.ActorID {
			continue
		}
		n.next++
		x.ID, x.UserID, x.CreatedAt = n.next, id, time.Now()
		n.items = append(n.items, x)
	}
}

// DiagramNotifications lists a user's diagram notifications, newest first.
func (n *Notices) DiagramNotifications(_ context.Context, userID int, unreadOnly bool, limit int) ([]social.Notification, int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []social.Notification
	unread := 0
	for i := len(n.items) - 1; i >= 0; i-- {
		x := n.items[i]
		if x.UserID != userID {
			continue
		}
		if x.ReadAt == nil {
			unread++
		}
		if (!unreadOnly || x.ReadAt == nil) && len(out) < limit {
			out = append(out, x)
		}
	}
	return out, unread, nil
}

// MarkDiagramRead marks the user's diagram notifications read: ids, or all.
func (n *Notices) MarkDiagramRead(_ context.Context, userID int, ids []int64, all bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	for i := range n.items {
		if n.items[i].UserID == userID && (all || slices.Contains(ids, n.items[i].ID)) && n.items[i].ReadAt == nil {
			n.items[i].ReadAt = &now
		}
	}
	return nil
}

// All returns every stored notification.
func (n *Notices) All() []social.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.items)
}
