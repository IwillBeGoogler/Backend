package diagramhub

import (
	"context"
	"slices"
	"sync"
)

// Slots counts the participants of audio rooms, across API instances when
// backed by Redis.
type Slots interface {
	// Acquire takes (or refreshes) a place in a file's room; false means the
	// room already has max participants.
	Acquire(ctx context.Context, fileID, clientID string, max int) (bool, error)
	// Release gives the place back.
	Release(ctx context.Context, fileID, clientID string) error
	// Members lists who holds a place in a file's room.
	Members(ctx context.Context, fileID string) ([]string, error)
}

// MemorySlots is Slots for a single instance.
type MemorySlots struct {
	mu    sync.Mutex
	rooms map[string]map[string]struct{}
}

func NewMemorySlots() *MemorySlots { return &MemorySlots{rooms: map[string]map[string]struct{}{}} }

func (m *MemorySlots) Acquire(_ context.Context, fileID, clientID string, max int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	room := m.rooms[fileID]
	if _, in := room[clientID]; in {
		return true, nil
	}
	if len(room) >= max {
		return false, nil
	}
	if room == nil {
		room = map[string]struct{}{}
		m.rooms[fileID] = room
	}
	room[clientID] = struct{}{}
	return true, nil
}

func (m *MemorySlots) Release(_ context.Context, fileID, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rooms[fileID], clientID)
	if len(m.rooms[fileID]) == 0 {
		delete(m.rooms, fileID)
	}
	return nil
}

func (m *MemorySlots) Members(_ context.Context, fileID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.rooms[fileID]))
	for c := range m.rooms[fileID] {
		out = append(out, c)
	}
	slices.Sort(out)
	return out, nil
}
