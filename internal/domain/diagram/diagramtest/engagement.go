package diagramtest

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// Claps and bookmarks of published files (diagram.EngagementRepository).

func ek(fileID string, user int) string { return fileID + "|" + strconv.Itoa(user) }

// published reports whether the file is unlisted or public; the caller holds the lock.
func (m *Memory) published(fileID string) bool {
	_, ok := m.files[fileID]
	return ok && m.pubs[fileID].Visibility.Published()
}

func (m *Memory) PublicListing(_ context.Context, slug string) (diagram.Listing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.pubs {
		if p.Slug == slug && p.Visibility.Published() {
			l, _ := m.listing(id)
			return l, nil
		}
	}
	return diagram.Listing{}, domain.ErrNotFound
}

func (m *Memory) Clap(_ context.Context, fileID string, userID, n int) (int, int, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.published(fileID) {
		return 0, 0, false, domain.ErrNotFound
	}
	if m.claps == nil {
		m.claps = map[string]int{}
	}
	k := ek(fileID, userID)
	before, had := m.claps[k]
	m.claps[k] = min(before+n, diagram.MaxClaps)
	l, _ := m.listing(fileID)
	return l.Claps, m.claps[k], !had, nil
}

func (m *Memory) Bookmark(_ context.Context, fileID string, userID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.published(fileID) {
		return domain.ErrNotFound
	}
	if m.bookmarks == nil {
		m.bookmarks = map[string]time.Time{}
	}
	if _, ok := m.bookmarks[ek(fileID, userID)]; !ok {
		m.bookmarks[ek(fileID, userID)] = time.Now()
	}
	return nil
}

func (m *Memory) Unbookmark(_ context.Context, fileID string, userID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.bookmarks, ek(fileID, userID))
	return nil
}

func (m *Memory) Viewer(_ context.Context, fileID string, userID int) (diagram.Viewer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, saved := m.bookmarks[ek(fileID, userID)]
	return diagram.Viewer{MyClaps: m.claps[ek(fileID, userID)], Bookmarked: saved}, nil
}

func (m *Memory) ListBookmarked(_ context.Context, userID, page, limit int) ([]diagram.Listing, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type row struct {
		l  diagram.Listing
		at time.Time
	}
	var all []row
	for k, at := range m.bookmarks {
		id, u, _ := strings.Cut(k, "|")
		if u != strconv.Itoa(userID) || !m.published(id) {
			continue
		}
		l, _ := m.listing(id)
		all = append(all, row{l, at})
	}
	slices.SortFunc(all, func(a, b row) int { return b.at.Compare(a.at) })
	total := len(all)
	from := min((max(page, 1)-1)*limit, total)
	out := []diagram.Listing{}
	for _, r := range all[from:min(from+limit, total)] {
		out = append(out, r.l)
	}
	return out, total, nil
}
