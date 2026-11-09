package diagramtest

import (
	"context"
	"slices"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// yfile is the stored Yjs state of one file.
type yfile struct {
	snapshot []byte
	snapSeq  int64
	updates  []diagram.YUpdate
}

func (f *yfile) last() int64 {
	if n := len(f.updates); n > 0 {
		return f.updates[n-1].Seq
	}
	return f.snapSeq
}

// y returns the file's state, nil for a file that does not exist; the lock
// is held by the caller.
func (m *Memory) y(fileID string, create bool) *yfile {
	if _, ok := m.files[fileID]; !ok {
		return nil
	}
	if m.ys == nil {
		m.ys = map[string]*yfile{}
	}
	f := m.ys[fileID]
	if f == nil && create {
		f = &yfile{}
		m.ys[fileID] = f
	}
	return f
}

func (m *Memory) YLoad(_ context.Context, fileID string) (diagram.YState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.y(fileID, false)
	if f == nil {
		return diagram.YState{}, nil
	}
	ups := make([]diagram.YUpdate, len(f.updates))
	for i, u := range f.updates {
		ups[i] = diagram.YUpdate{Seq: u.Seq, Data: slices.Clone(u.Data)}
	}
	return diagram.YState{Snapshot: slices.Clone(f.snapshot), SnapshotSeq: f.snapSeq, Updates: ups}, nil
}

func (m *Memory) YAppend(_ context.Context, fileID string, data []byte) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.y(fileID, true)
	if f == nil {
		return 0, domain.ErrNotFound
	}
	seq := f.last() + 1
	f.updates = append(f.updates, diagram.YUpdate{Seq: seq, Data: slices.Clone(data)})
	return seq, nil
}

func (m *Memory) YAppendMany(_ context.Context, fileID string, data [][]byte) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.y(fileID, true)
	if f == nil {
		return 0, domain.ErrNotFound
	}
	for _, d := range data {
		f.updates = append(f.updates, diagram.YUpdate{Seq: f.last() + 1, Data: slices.Clone(d)})
	}
	return f.last(), nil
}

func (m *Memory) YCompact(_ context.Context, fileID string, snapshot []byte, seq int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.y(fileID, false)
	if f == nil || seq <= f.snapSeq || seq > f.last() {
		return false, nil
	}
	f.snapshot, f.snapSeq = slices.Clone(snapshot), seq
	f.updates = slices.DeleteFunc(f.updates, func(u diagram.YUpdate) bool { return u.Seq <= seq })
	return true, nil
}

func (m *Memory) YReset(_ context.Context, fileID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.ys, fileID)
	return nil
}
