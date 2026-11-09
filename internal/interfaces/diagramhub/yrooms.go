package diagramhub

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/topic"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/yjs"
)

// Live co-editing: a y-websocket compatible relay over MQTT. The server does
// not hold a Yjs document and never merges: it stores the updates editors
// publish (in order, numbered), hands them to joiners and to the other
// editors, and uses a yjs.Tracker, which knows what the update stream covers,
// to ask a joiner only for what is missing and to skip updates that carry
// nothing new. The payloads are y-websocket frames (yjs.Update and friends).
//
//   - A joining client publishes sync step 1 to yjs/req/{client}. The server
//     answers on yjs/sync/{client} with the stored snapshot and every stored
//     update as sync updates, then an empty sync step 2 (which is what tells
//     y-websocket clients they are synced), then its own sync step 1 with the
//     state vector of everything it holds, so the client answers on yjs/up
//     with sync step 2 holding what it made offline.
//   - A sync update or step 2 an editor publishes to yjs/up/{client} is, if it
//     has anything new, republished at once on yjs/down with the sender as
//     "origin", and written to the database in the next batch (FlushEvery).
//   - Viewers and commenters read and show their cursor but never write: the
//     broker refuses their publish (access rules) and the server also tells
//     them with a "denied" message on dg/c/{client}/ctl.
//   - After CompactAfter stored updates the server sends the editor whose
//     update reached the mark a compaction hint (message 4, with a sequence
//     number) on its sync topic. A client holding everything up to it
//     replies on yjs/up with message 3: seq and the whole document as one
//     update. The server checks that it covers every stored update up to seq,
//     then stores it as the new snapshot and drops those updates.

// ErrIncomplete is returned when a compacted snapshot lacks something the
// server stores.
var ErrIncomplete = errors.New("diagramhub: the snapshot does not cover every update up to seq")

// ErrCollabOff is returned when live co-editing is off.
var ErrCollabOff = errors.New("diagramhub: live editing is off")

const storeTimeout = 10 * time.Second

type pendingUpdate struct {
	data   []byte
	origin string
}

type yroom struct {
	file string

	flushMu sync.Mutex // one database write at a time, in order

	mu      sync.Mutex
	loaded  bool
	tr      *yjs.Tracker
	snapSeq int64
	pending []pendingUpdate
	fails   int
	hintAt  time.Time
	used    time.Time
	aw      map[uint64]yjs.AwarenessEntry // cursors seen by this instance
	owner   map[uint64]string             // awareness id -> the client that claimed it
	byOwner map[string]map[uint64]struct{}
}

func (r *yroom) idle(now time.Time, after time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending) == 0 && now.Sub(r.used) > after
}

// room returns the file's room, loaded from the database.
func (h *Hub) room(ctx context.Context, fileID string) (*yroom, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, context.Canceled
	}
	r := h.rooms[fileID]
	if r == nil {
		r = &yroom{file: fileID, tr: yjs.NewTracker(), aw: map[uint64]yjs.AwarenessEntry{}, owner: map[uint64]string{},
			byOwner: map[string]map[uint64]struct{}{}}
		h.rooms[fileID] = r
		yjsRooms.Set(float64(len(h.rooms)))
	}
	h.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.used = time.Now()
	if r.loaded {
		return r, nil
	}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	st, err := h.o.Y.YLoad(ctx, fileID)
	if err != nil {
		return nil, err
	}
	r.snapSeq = st.SnapshotSeq
	if len(st.Snapshot) > 0 {
		r.observe(st.Snapshot)
	}
	for _, u := range st.Updates {
		r.observe(u.Data)
	}
	r.loaded = true
	return r, nil
}

func (r *yroom) observe(update []byte) {
	if _, err := r.tr.Observe(update); err != nil {
		zap.L().Warn("diagramhub: stored yjs update does not parse", zap.String("file", r.file), zap.Error(err))
	}
}

// evict forgets a room whose tracker can no longer be trusted.
func (h *Hub) evict(r *yroom) {
	h.mu.Lock()
	if h.rooms[r.file] == r {
		delete(h.rooms, r.file)
		yjsRooms.Set(float64(len(h.rooms)))
	}
	h.mu.Unlock()
}

// handleUp processes a frame an editor published to yjs/up/{client}.
func (h *Hub) handleUp(ctx context.Context, t topic.Topic, frame []byte) {
	if !h.o.CollabEnabled {
		return
	}
	if len(frame) > h.o.MaxUpdateBytes {
		zap.L().Warn("diagramhub: yjs message too big", zap.String("file", t.FileID), zap.Int("bytes", len(frame)))
		return
	}
	a, ok := h.access(ctx, t.ClientID, t.FileID)
	if !ok {
		return
	}
	if !a.Role.Allows(diagram.RoleEditor) { // the broker already refused it; a client that raced a role change hears why
		h.ctl(ctx, t.ClientID, map[string]any{"type": "denied", "fileId": t.FileID, "reason": "read-only"})
		return
	}
	m, err := yjs.Decode(frame)
	if err != nil {
		return // not for us: a client that speaks something else
	}
	switch m.Type {
	case yjs.MsgSync:
		switch m.Sub {
		case yjs.SyncStep1:
			h.serveJoin(ctx, t.FileID, t.ClientID)
		default:
			h.write(ctx, t.FileID, t.ClientID, m.Payload)
		}
	case yjs.MsgCompact:
		if _, err := h.Compact(ctx, t.FileID, m.Seq, m.Payload); err != nil && !errors.Is(err, ErrIncomplete) {
			zap.L().Warn("diagramhub: compact", zap.String("file", t.FileID), zap.Error(err))
		}
	}
}

// handleReq processes sync step 1 published to yjs/req/{client} (any role).
func (h *Hub) handleReq(ctx context.Context, t topic.Topic, frame []byte) {
	if !h.o.CollabEnabled || len(frame) > h.o.MaxUpdateBytes {
		return
	}
	if _, ok := h.access(ctx, t.ClientID, t.FileID); !ok {
		return
	}
	if m, err := yjs.Decode(frame); err == nil && m.Type == yjs.MsgSync && m.Sub == yjs.SyncStep1 {
		h.serveJoin(ctx, t.FileID, t.ClientID)
	}
}

func (h *Hub) sync(ctx context.Context, fileID, clientID string, frame []byte) {
	h.publish(ctx, application.Message{Topic: topic.YjsSyncTopic(fileID, clientID), Payload: frame, QoS: 1, Expiry: syncExpiry})
}

// serveJoin answers sync step 1: everything stored, an empty step 2, the
// server's own step 1, and the cursors it knows.
func (h *Hub) serveJoin(ctx context.Context, fileID, clientID string) {
	r, err := h.room(ctx, fileID)
	if err != nil {
		zap.L().Error("diagramhub: load yjs state", zap.String("file", fileID), zap.Error(err))
		h.ctl(ctx, clientID, map[string]any{"type": "unavailable", "code": CtlUnavail, "fileId": fileID})
		return
	}
	h.flush(ctx, r) // what the database holds is what the tracker knows
	lctx, cancel := context.WithTimeout(ctx, storeTimeout)
	st, err := h.o.Y.YLoad(lctx, fileID)
	cancel()
	if err != nil {
		zap.L().Error("diagramhub: load yjs state", zap.String("file", fileID), zap.Error(err))
		h.ctl(ctx, clientID, map[string]any{"type": "unavailable", "code": CtlUnavail, "fileId": fileID})
		return
	}
	if len(st.Snapshot) > 0 {
		h.sync(ctx, fileID, clientID, yjs.Update(st.Snapshot))
	}
	for _, u := range st.Updates {
		h.sync(ctx, fileID, clientID, yjs.Update(u.Data))
	}
	h.sync(ctx, fileID, clientID, yjs.Step2(yjs.EmptyUpdate))
	r.mu.Lock()
	sv := r.tr.StateVector()
	var states []yjs.AwarenessEntry
	for _, e := range r.aw {
		if e.State != "null" {
			states = append(states, e)
		}
	}
	r.mu.Unlock()
	h.sync(ctx, fileID, clientID, yjs.Step1(sv))
	if len(states) > 0 {
		h.sync(ctx, fileID, clientID, yjs.Awareness(yjs.EncodeAwareness(states)))
	}
}

// write relays an update from an editor and queues it for the database.
func (h *Hub) write(ctx context.Context, fileID, clientID string, update []byte) {
	p, err := yjs.Parse(update)
	if err != nil {
		return // damaged: never stored, never relayed
	}
	r, err := h.room(ctx, fileID)
	if err != nil {
		zap.L().Error("diagramhub: load yjs state", zap.String("file", fileID), zap.Error(err))
		h.ctl(ctx, clientID, map[string]any{"type": "unavailable", "code": CtlUnavail, "fileId": fileID})
		return
	}
	// Judged and merged into the tracker under one lock, so two editors
	// sending the same news relay and store it once, in the order relayed.
	r.mu.Lock()
	if !r.tr.Novel(p) {
		r.mu.Unlock()
		return
	}
	r.tr.Merge(p)
	r.pending = append(r.pending, pendingUpdate{slices.Clone(update), clientID})
	r.used = time.Now()
	r.mu.Unlock()
	yjsIn.Inc()
	yjsOut.Inc()
	h.publish(ctx, application.Message{Topic: topic.YjsDownTopic(fileID), Payload: yjs.Update(update), QoS: 1, Origin: clientID})
}

func (h *Hub) flushAll() {
	h.mu.Lock()
	var rooms []*yroom
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()
	for _, r := range rooms {
		r.mu.Lock()
		n := len(r.pending)
		r.mu.Unlock()
		if n > 0 {
			h.flush(h.ctx, r)
		}
	}
}

const maxFlushFails = 3

// flush writes the room's pending updates to the database in one batch. A
// batch that cannot be written is tried again on the next tick; after
// maxFlushFails the room is dropped (its tracker knows updates the database
// does not) and the editors who sent them are told, so they sync again and
// send what the server lacks.
func (h *Hub) flush(ctx context.Context, r *yroom) {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	batch := r.pending
	r.pending = nil
	r.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	data := make([][]byte, len(batch))
	for i, b := range batch {
		data[i] = b.data
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	t0 := time.Now()
	seq, err := h.o.Y.YAppendMany(sctx, r.file, data)
	yjsFlush.Observe(time.Since(t0).Seconds())
	if err != nil && errors.Is(err, context.Canceled) && ctx.Err() != nil {
		// shutting down, not a store failure: keep the batch for Close's final flush
		r.mu.Lock()
		r.pending = append(batch, r.pending...)
		r.mu.Unlock()
		return
	}
	if err != nil {
		yjsFlushFail.Inc()
		gone := errors.Is(err, domain.ErrNotFound)
		r.mu.Lock()
		r.fails++
		giveUp := gone || r.fails >= maxFlushFails || ctx.Err() != nil
		if !giveUp {
			r.pending = append(batch, r.pending...)
		}
		r.mu.Unlock()
		zap.L().Error("diagramhub: store yjs updates", zap.String("file", r.file), zap.Int("updates", len(batch)), zap.Error(err))
		if giveUp {
			h.evict(r)
			if gone {
				return
			}
			seen := map[string]bool{}
			for _, b := range batch {
				if !seen[b.origin] {
					seen[b.origin] = true
					h.ctl(h.ctx, b.origin, map[string]any{"type": "unavailable", "code": CtlUnavail, "fileId": r.file})
				}
			}
		}
		return
	}
	r.mu.Lock()
	r.fails = 0
	yjsStored.Observe(float64(seq - r.snapSeq))
	hint := seq-r.snapSeq >= int64(h.o.CompactAfter) && time.Since(r.hintAt) > 30*time.Second
	r.mu.Unlock()
	if hint { // another instance may have compacted since: the database knows
		lctx, cancel := context.WithTimeout(ctx, storeTimeout)
		st, err := h.o.Y.YLoad(lctx, r.file)
		cancel()
		r.mu.Lock()
		if err == nil {
			r.snapSeq = max(r.snapSeq, st.SnapshotSeq)
		}
		hint = seq-r.snapSeq >= int64(h.o.CompactAfter) && time.Since(r.hintAt) > 30*time.Second
		if hint {
			r.hintAt = time.Now()
		}
		r.mu.Unlock()
	}
	if hint {
		h.sync(ctx, r.file, batch[len(batch)-1].origin, yjs.CompactHint(seq))
	}
}

// Compact makes snapshot, a client's whole document as one update, the stored
// state as of seq. It is refused (ErrIncomplete) unless the snapshot covers
// everything stored up to seq, so a client that had not yet seen an update
// cannot make the server forget it. It reports false when there was nothing
// to do (seq is not newer than the stored snapshot, or past the last update).
func (h *Hub) Compact(ctx context.Context, fileID string, seq int64, snapshot []byte) (bool, error) {
	if !h.o.CollabEnabled {
		return false, ErrCollabOff
	}
	cand := yjs.NewTracker()
	if _, err := cand.Observe(snapshot); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	st, err := h.o.Y.YLoad(ctx, fileID)
	if err != nil {
		return false, err
	}
	if seq <= st.SnapshotSeq || seq > st.LastSeq() {
		return false, nil
	}
	base := yjs.NewTracker()
	if len(st.Snapshot) > 0 {
		if _, err := base.Observe(st.Snapshot); err != nil {
			return false, err
		}
	}
	for _, u := range st.Updates {
		if u.Seq > seq {
			break
		}
		if _, err := base.Observe(u.Data); err != nil {
			return false, err
		}
	}
	if !cand.Covers(base) {
		yjsCompactRefused.Inc()
		return false, ErrIncomplete
	}
	ok, err := h.o.Y.YCompact(ctx, fileID, snapshot, seq)
	if ok {
		yjsCompactDone.Inc()
		h.mu.Lock()
		r := h.rooms[fileID]
		h.mu.Unlock()
		if r != nil {
			r.mu.Lock()
			r.snapSeq = max(r.snapSeq, seq)
			r.mu.Unlock()
		}
	}
	return ok, err
}

// Awareness

// handleAwareness takes a cursor update a client published to
// awareness/up/{client}, rewrites its identity to the one the server knows
// the client by, and publishes the result on awareness/out. The client
// cannot name another person: a client id is only usable by its owner, and
// the name and user id come from the account behind it. Awareness ids are
// claimed first come first served per instance.
func (h *Hub) handleAwareness(ctx context.Context, t topic.Topic, frame []byte) {
	if !h.o.CollabEnabled || len(frame) > 64<<10 {
		return
	}
	now := time.Now()
	h.mu.Lock()
	if last, ok := h.seen[t.ClientID]; ok && now.Sub(last) < 20*time.Millisecond { // ≤ 50 per second
		h.mu.Unlock()
		return
	}
	h.seen[t.ClientID] = now
	h.mu.Unlock()
	m, err := yjs.Decode(frame)
	if err != nil || m.Type != yjs.MsgAwareness {
		return
	}
	a, ok := h.access(ctx, t.ClientID, t.FileID)
	if !ok {
		return
	}
	r, err := h.room(ctx, t.FileID)
	if err != nil {
		return
	}
	r.mu.Lock()
	out, ids, err := yjs.SanitizeAwareness(m.Payload, yjs.Identity{ID: userID(a.UserID), Name: a.Name}, func(id uint64) bool {
		o, claimed := r.owner[id]
		return !claimed || o == t.ClientID // the first to claim an id owns it
	})
	if err != nil || len(ids) == 0 {
		r.mu.Unlock()
		return
	}
	es, _ := yjs.ParseAwareness(out)
	var keep []yjs.AwarenessEntry
	for _, e := range es {
		if old, ok := r.aw[e.Client]; ok && e.Clock < old.Clock {
			continue // stale
		}
		r.owner[e.Client] = t.ClientID
		if r.byOwner[t.ClientID] == nil {
			r.byOwner[t.ClientID] = map[uint64]struct{}{}
		}
		r.byOwner[t.ClientID][e.Client] = struct{}{}
		r.aw[e.Client] = e
		keep = append(keep, e)
	}
	r.mu.Unlock()
	if len(keep) > 0 {
		h.publishAwareness(ctx, t.FileID, yjs.EncodeAwareness(keep), t.ClientID)
	}
}

func (h *Hub) publishAwareness(ctx context.Context, fileID string, update []byte, origin string) {
	h.publish(ctx, application.Message{Topic: topic.AwarenessOutTopic(fileID), Payload: yjs.Awareness(update), QoS: 0, Origin: origin})
}

// dropAwareness tells the others that the cursors of a client that left are
// gone.
func (h *Hub) dropAwareness(ctx context.Context, fileID, clientID string) {
	h.mu.Lock()
	r := h.rooms[fileID]
	h.mu.Unlock()
	if r == nil {
		return
	}
	var gone []yjs.AwarenessEntry
	r.mu.Lock()
	for id := range r.byOwner[clientID] {
		delete(r.owner, id)
		if e, ok := r.aw[id]; ok {
			delete(r.aw, id)
			if e.State != "null" { // tell the others the cursor is gone
				gone = append(gone, yjs.AwarenessEntry{Client: id, Clock: e.Clock + 1, State: "null"})
			}
		}
	}
	delete(r.byOwner, clientID)
	r.mu.Unlock()
	if len(gone) > 0 {
		h.publishAwareness(ctx, fileID, yjs.EncodeAwareness(gone), clientID)
	}
}

func userID(id int) string {
	if id == 0 {
		return ""
	}
	return itoa(id)
}
func itoa(n int) string { return strconv.Itoa(n) }
