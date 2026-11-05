package diagram

import "context"

// The Yjs state of a live-edited file. The server does not understand the
// documents: it keeps the bytes clients send, as an optional snapshot (one
// update holding everything up to SnapshotSeq, sent by a client) and the
// updates after it, in the order they arrived.

// YUpdate is one stored update; Seq grows by one per update of a file.
type YUpdate struct {
	Seq  int64
	Data []byte
}

// YState is what a joining client is sent.
type YState struct {
	Snapshot    []byte // nil when no client has compacted yet
	SnapshotSeq int64
	Updates     []YUpdate // Seq > SnapshotSeq, oldest first
}

// LastSeq is the sequence number of the newest update.
func (s YState) LastSeq() int64 {
	if n := len(s.Updates); n > 0 {
		return s.Updates[n-1].Seq
	}
	return s.SnapshotSeq
}

// YRepository stores the Yjs state of files.
type YRepository interface {
	// YLoad returns a file's state (empty for a file never edited live).
	YLoad(ctx context.Context, fileID string) (YState, error)
	// YAppend stores an update and returns its sequence number. It returns
	// ErrNotFound (domain) for a file that no longer exists.
	YAppend(ctx context.Context, fileID string, data []byte) (int64, error)
	// YAppendMany stores updates in order, in one transaction, and returns
	// the sequence number of the last (the others follow it consecutively).
	YAppendMany(ctx context.Context, fileID string, data [][]byte) (int64, error)
	// YCompact makes snapshot the state as of seq: it replaces the stored
	// snapshot and drops the updates up to seq. It reports false, changing
	// nothing, when seq is not past the stored snapshot or past the newest
	// update.
	YCompact(ctx context.Context, fileID string, snapshot []byte, seq int64) (bool, error)
	// YReset forgets a file's Yjs state (its document was replaced).
	YReset(ctx context.Context, fileID string) error
}
