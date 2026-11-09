package diagramtest

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// members lists the entries of roles whose key starts with id|.
func (m *Memory) memberList(roles map[string]diagram.Role, id string) []diagram.Member {
	var out []diagram.Member
	for k, role := range roles {
		rest, ok := strings.CutPrefix(k, id+"|")
		if !ok {
			continue
		}
		uid, _ := strconv.Atoi(rest)
		out = append(out, diagram.Member{UserID: uid, Role: role, CreatedAt: m.memberAt[k]})
	}
	slices.SortFunc(out, func(a, b diagram.Member) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return a.UserID - b.UserID
	})
	return out
}

func (m *Memory) Members(_ context.Context, projectID string) ([]diagram.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.memberList(m.members, projectID), nil
}

func (m *Memory) SetMember(_ context.Context, projectID string, userID int, role diagram.Role, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(projectID, userID)
	if _, ok := m.members[k]; !ok {
		m.memberAt[k] = at
	}
	m.members[k] = role
	return nil
}

func (m *Memory) UpdateMember(_ context.Context, projectID string, userID int, role diagram.Role) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(projectID, userID)
	if _, ok := m.members[k]; !ok {
		return false, nil
	}
	m.members[k] = role
	return true, nil
}

func (m *Memory) RemoveMember(_ context.Context, projectID string, userID int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(projectID, userID)
	if _, ok := m.members[k]; !ok {
		return false, nil
	}
	delete(m.members, k)
	for fid, f := range m.files {
		if f.ProjectID == projectID {
			delete(m.overs, key(fid, userID))
		}
	}
	return true, nil
}

func (m *Memory) FileMembers(_ context.Context, fileID string) ([]diagram.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.memberList(m.overs, fileID), nil
}

func (m *Memory) SetFileMember(_ context.Context, fileID string, userID int, role diagram.Role, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(fileID, userID)
	if _, ok := m.overs[k]; !ok {
		m.memberAt[k] = at
	}
	m.overs[k] = role
	return nil
}

func (m *Memory) RemoveFileMember(_ context.Context, fileID string, userID int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(fileID, userID)
	_, ok := m.overs[k]
	delete(m.overs, k)
	return ok, nil
}

func (m *Memory) CreateShare(_ context.Context, s diagram.ShareLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.shares[s.ID]; ok {
		return domain.ErrConflict
	}
	for _, o := range m.shares {
		if o.TokenHash == s.TokenHash {
			return domain.ErrConflict
		}
	}
	m.shares[s.ID] = s
	return nil
}

func (m *Memory) Shares(_ context.Context, projectID string) ([]diagram.ShareLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []diagram.ShareLink
	for _, s := range m.shares {
		if s.ProjectID == projectID {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b diagram.ShareLink) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out, nil
}

func (m *Memory) RevokeShare(_ context.Context, projectID, id string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.shares[id]
	if !ok || s.ProjectID != projectID {
		return false, nil
	}
	if s.RevokedAt == nil {
		s.RevokedAt = &at
		m.shares[id] = s
	}
	return true, nil
}

func (m *Memory) SharesExpiredBetween(_ context.Context, from, to time.Time) ([]diagram.ShareLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []diagram.ShareLink
	for _, s := range m.shares {
		if s.ExpiresAt != nil && s.ExpiresAt.After(from) && !s.ExpiresAt.After(to) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Memory) ShareByHash(_ context.Context, hash string) (diagram.ShareLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.shares {
		if s.TokenHash == hash {
			return s, nil
		}
	}
	return diagram.ShareLink{}, domain.ErrNotFound
}

func (m *Memory) Asset(_ context.Context, hash string) (diagram.Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assets[hash]
	if !ok {
		return a, domain.ErrNotFound
	}
	return a, nil
}

func (m *Memory) InsertAsset(_ context.Context, a diagram.Asset) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.assets[a.Hash]; ok {
		return false, nil
	}
	m.assets[a.Hash] = a
	return true, nil
}

func (m *Memory) SetThumbnail(_ context.Context, fileID, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[fileID]
	if !ok {
		return domain.ErrNotFound
	}
	f.ThumbnailHash = &hash
	m.files[fileID] = f
	return nil
}

func (m *Memory) Library(_ context.Context, projectID string) (diagram.Library, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.libs[projectID], nil
}

func (m *Memory) SaveLibrary(_ context.Context, projectID string, baseRev int, doc json.RawMessage, _ int, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[projectID]; !ok {
		return false, domain.ErrNotFound
	}
	if m.libs[projectID].Rev != baseRev {
		return false, nil
	}
	m.libs[projectID] = diagram.Library{Rev: baseRev + 1, Doc: doc}
	return true, nil
}

func (m *Memory) Private(_ context.Context, fileID string, userID int) (diagram.Private, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.privs[key(fileID, userID)]
	if !ok {
		return p, domain.ErrNotFound
	}
	return p, nil
}

func (m *Memory) PutPrivate(_ context.Context, fileID string, userID int, data json.RawMessage, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.privs[key(fileID, userID)] = diagram.Private{Data: data, UpdatedAt: at}
	return nil
}

func (m *Memory) DeletePrivate(_ context.Context, fileID string, userID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.privs, key(fileID, userID))
	return nil
}

var _ diagram.SharingRepository = (*Memory)(nil)

// ExpireShares sets every share link's expiry to the past.
func (m *Memory) ExpireShares() {
	m.mu.Lock()
	defer m.mu.Unlock()
	past := time.Now().Add(-time.Hour)
	for id, s := range m.shares {
		s.ExpiresAt = &past
		m.shares[id] = s
	}
}
