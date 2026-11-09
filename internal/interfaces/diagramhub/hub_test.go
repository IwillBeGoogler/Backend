package diagramhub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/topic"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/yjs"
)

// broker stands in for EMQX: it records what the hub publishes, keeps the
// retained messages, and hands the hub what the "browsers" publish.
type broker struct {
	hub      *Hub
	mu       sync.Mutex
	msgs     []application.Message
	retained map[string][]byte
}

func (b *broker) Publish(_ context.Context, m application.Message) error {
	b.mu.Lock()
	b.msgs = append(b.msgs, m)
	if m.Retain {
		if len(m.Payload) == 0 {
			delete(b.retained, m.Topic)
		} else {
			b.retained[m.Topic] = m.Payload
		}
	}
	b.mu.Unlock()
	// the hub also subscribes to file events (plain subscription)
	if _, all := Subscriptions(); topic.Match(all[0], m.Topic) {
		b.hub.OnMessage(m)
	}
	return nil
}

// from is a client publishing.
func (b *broker) from(client, tp string, payload []byte) {
	b.hub.OnMessage(application.Message{Topic: tp, Payload: payload, QoS: 1, Origin: client})
}

// on are the messages published to a topic, in order.
func (b *broker) on(tp string) []application.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []application.Message
	for _, m := range b.msgs {
		if m.Topic == tp {
			out = append(out, m)
		}
	}
	return out
}

func (b *broker) jsonOn(t *testing.T, tp, typ string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range b.on(tp) {
		var v map[string]any
		if json.Unmarshal(m.Payload, &v) == nil && v["type"] == typ {
			out = append(out, v)
		}
	}
	return out
}

// access is the fake Authority: client id -> user, and per file a role.
type access struct {
	roles map[string]diagram.Role // client -> role (all files of project p)
	users map[string]int
	gone  map[string]bool // files that no longer exist
}

func (a access) FileAccess(_ context.Context, client, file string) (application.ChannelAccess, error) {
	r, ok := a.roles[client]
	if !ok || a.gone[file] {
		return application.ChannelAccess{}, domain.ErrNotFound
	}
	u := a.users[client]
	name := "Guest"
	if u != 0 {
		name = "U" + itoa(u)
	}
	return application.ChannelAccess{ProjectID: "p", FileID: file, Role: r, UserID: u, Name: name}, nil
}

const (
	alice  = "u1.aaaaaaaa"
	bob    = "u2.bbbbbbbb"
	carol  = "u3.cccccccc"
	viewer = "u4.vvvvvvvv"
)

func fixture(t *testing.T, mem *diagramtest.Memory, o Options) (*Hub, *broker) {
	t.Helper()
	if o.Authority == nil {
		o.Authority = access{
			roles: map[string]diagram.Role{alice: diagram.RoleEditor, bob: diagram.RoleEditor, carol: diagram.RoleOwner, viewer: diagram.RoleViewer},
			users: map[string]int{alice: 1, bob: 2, carol: 3, viewer: 4},
		}
	}
	if o.Y == nil && mem != nil {
		o.Y = mem
	}
	o.CollabEnabled = o.Y != nil
	if o.CompactAfter == 0 {
		o.CompactAfter = 3
	}
	o.FlushEvery = 10 * time.Millisecond
	o.Heartbeat = time.Hour
	h := New(o)
	b := &broker{hub: h, retained: map[string][]byte{}}
	h.Attach(b)
	t.Cleanup(h.Close)
	return h, b
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 400 {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func downFrames(b *broker, file string) [][]byte {
	var out [][]byte
	for _, m := range b.on(topic.YjsDownTopic(file)) {
		out = append(out, m.Payload)
	}
	return out
}

func stored(mem *diagramtest.Memory, file string) diagram.YState {
	st, _ := mem.YLoad(context.Background(), file)
	return st
}

func TestYjsRelaysOnceAndStoresInBatches(t *testing.T) {
	mem := store(t, "f", "g")
	_, b := fixture(t, mem, Options{})
	ups := updates(t, "two clients")

	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(ups[0]))
	b.from(bob, topic.YjsUpTopic("f", bob), yjs.Update(ups[0])) // the same news from a second editor
	b.from(bob, topic.YjsUpTopic("f", bob), yjs.Update(ups[1]))
	eventually(t, "the relay", func() bool { return len(downFrames(b, "f")) == 2 })
	eventually(t, "the batch write", func() bool { return len(stored(mem, "f").Updates) == 2 })

	down := b.on(topic.YjsDownTopic("f"))
	if !bytes.Equal(down[0].Payload, yjs.Update(ups[0])) || down[0].Origin != alice || down[1].Origin != bob || down[0].QoS != 1 {
		t.Errorf("down = %+v", down)
	}
	if n := len(downFrames(b, "g")); n != 0 {
		t.Error("another file heard it")
	}
	if st := stored(mem, "f"); st.Updates[0].Seq != 1 || st.Updates[1].Seq != 2 {
		t.Errorf("stored = %+v", st.Updates)
	}
	// damaged updates are neither stored nor relayed
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update([]byte{1, 2, 3}))
	time.Sleep(50 * time.Millisecond)
	if len(downFrames(b, "f")) != 2 {
		t.Error("a damaged update was relayed")
	}
}

func TestYjsJoinGetsHistoryThenStep2ThenStep1(t *testing.T) {
	mem := store(t, "f")
	_, b := fixture(t, mem, Options{})
	ups := updates(t, "two clients")
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(ups[0]))
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(ups[1]))

	// even a viewer may ask; the answer includes what is still queued for the database
	b.from(viewer, topic.YjsReqTopic("f", viewer), yjs.Step1([]byte{0}))
	eventually(t, "the join answer", func() bool { return len(b.on(topic.YjsSyncTopic("f", viewer))) >= 4 })
	var kinds []string
	var updatesSeen [][]byte
	for _, m := range b.on(topic.YjsSyncTopic("f", viewer)) {
		d, err := yjs.Decode(m.Payload)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case d.Type == yjs.MsgSync && d.Sub == yjs.SyncUpdate:
			kinds = append(kinds, "update")
			updatesSeen = append(updatesSeen, d.Payload)
		case d.Type == yjs.MsgSync && d.Sub == yjs.SyncStep2 && bytes.Equal(d.Payload, yjs.EmptyUpdate):
			kinds = append(kinds, "step2")
		case d.Type == yjs.MsgSync && d.Sub == yjs.SyncStep1:
			kinds = append(kinds, "step1")
			want := yjs.NewTracker()
			_, _ = want.Observe(ups[0])
			_, _ = want.Observe(ups[1])
			if !bytes.Equal(d.Payload, want.StateVector()) {
				t.Errorf("state vector = %x, want %x", d.Payload, want.StateVector())
			}
		}
	}
	if strings.Join(kinds, ",") != "update,update,step2,step1" || !bytes.Equal(updatesSeen[0], ups[0]) || !bytes.Equal(updatesSeen[1], ups[1]) {
		t.Errorf("join answer = %v", kinds)
	}
}

func TestYjsViewersNeverWrite(t *testing.T) {
	mem := store(t, "f")
	_, b := fixture(t, mem, Options{})
	ups := updates(t, "map values")
	// the broker's access rules refuse this; if one slips through (a role changed a moment ago) the server says why
	b.from(viewer, topic.YjsUpTopic("f", viewer), yjs.Update(ups[0]))
	b.from(viewer, topic.YjsUpTopic("f", viewer), yjs.Compact(1, ups[0]))
	eventually(t, "denied", func() bool { return len(b.jsonOn(t, topic.CtlTopic(viewer), "denied")) == 2 })
	time.Sleep(50 * time.Millisecond)
	if len(downFrames(b, "f")) != 0 || len(stored(mem, "f").Updates) != 0 || stored(mem, "f").Snapshot != nil {
		t.Error("a viewer's write was stored or relayed")
	}
	// a client that holds nothing is ignored altogether
	b.from("u9.zzzzzzzz", topic.YjsUpTopic("f", "u9.zzzzzzzz"), yjs.Update(ups[0]))
	time.Sleep(50 * time.Millisecond)
	if len(downFrames(b, "f")) != 0 {
		t.Error("a stranger's write was relayed")
	}
}

func hintSeq(b *broker, file, client string) int64 {
	for _, m := range b.on(topic.YjsSyncTopic(file, client)) {
		if d, err := yjs.Decode(m.Payload); err == nil && d.Type == yjs.MsgCompactHint {
			return d.Seq
		}
	}
	return 0
}

func TestYjsCompaction(t *testing.T) {
	mem := store(t, "f")
	h, b := fixture(t, mem, Options{}) // CompactAfter is 3
	ctx := context.Background()
	u := updates(t, "compaction")
	snap := prefixes // snap[i] holds the first i+1 updates

	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(u[0]))
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(u[1]))
	eventually(t, "two stored", func() bool { return len(stored(mem, "f").Updates) == 2 })
	if hintSeq(b, "f", alice) != 0 {
		t.Fatal("hinted too early")
	}
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(u[2]))
	eventually(t, "the hint", func() bool { return hintSeq(b, "f", alice) == 3 })

	// the snapshot must cover everything up to seq: a client that missed an update is refused
	if ok, err := h.Compact(ctx, "f", 3, snap[1]); ok || !errors.Is(err, ErrIncomplete) {
		t.Errorf("incomplete snapshot: %v, %v", ok, err)
	}
	if _, err := h.Compact(ctx, "f", 3, []byte{1, 2, 3}); err == nil {
		t.Error("garbage accepted as a snapshot")
	}
	if st := stored(mem, "f"); len(st.Updates) != 3 || st.Snapshot != nil {
		t.Fatalf("a refused snapshot changed the store: %+v", st)
	}
	// news already known is not stored again
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(u[1]))
	time.Sleep(60 * time.Millisecond)
	if len(stored(mem, "f").Updates) != 3 {
		t.Fatalf("a repeat was stored")
	}
	// over the wire (message 3), as the client answers the hint
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Compact(3, snap[2]))
	eventually(t, "compaction", func() bool { return stored(mem, "f").SnapshotSeq == 3 })
	if st := stored(mem, "f"); !bytes.Equal(st.Snapshot, snap[2]) || len(st.Updates) != 0 {
		t.Errorf("after compaction: %+v", st)
	}
	if ok, err := h.Compact(ctx, "f", 3, snap[2]); ok || err != nil {
		t.Errorf("repeat = %v, %v", ok, err)
	}
	if ok, err := h.Compact(ctx, "f", 9, snap[2]); ok || err != nil {
		t.Errorf("future seq = %v, %v", ok, err)
	}
	// the next update is numbered after the snapshot
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(u[3]))
	eventually(t, "update 4", func() bool { st := stored(mem, "f"); return len(st.Updates) == 1 && st.Updates[0].Seq == 4 })
}

func TestYjsOffWithoutTheFlagOrStore(t *testing.T) {
	_, b := fixture(t, nil, Options{})
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(updates(t, "map values")[0]))
	b.from(alice, topic.YjsReqTopic("f", alice), yjs.Step1([]byte{0}))
	time.Sleep(50 * time.Millisecond)
	if len(downFrames(b, "f")) != 0 || len(b.on(topic.YjsSyncTopic("f", alice))) != 0 {
		t.Error("answered with collaboration off")
	}
	h := New(Options{Y: store(t, "f"), CollabEnabled: false})
	defer h.Close()
	if _, err := h.Compact(context.Background(), "f", 1, nil); !errors.Is(err, ErrCollabOff) {
		t.Errorf("compact with collab off: %v", err)
	}
}

// failing is a store whose writes fail.
type failing struct {
	*diagramtest.Memory
	mu sync.Mutex
	n  int
}

func (f *failing) YAppendMany(context.Context, string, [][]byte) (int64, error) {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	return 0, errors.New("db down")
}

func TestYjsFailedWritesDropTheRoomAndTellTheEditors(t *testing.T) {
	f := &failing{Memory: store(t, "f")}
	_, b := fixture(t, nil, Options{Y: f})
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(updates(t, "map values")[0]))
	eventually(t, "unavailable", func() bool { return len(b.jsonOn(t, topic.CtlTopic(alice), "unavailable")) == 1 })
	if c := b.jsonOn(t, topic.CtlTopic(alice), "unavailable")[0]["code"]; c != float64(CtlUnavail) {
		t.Errorf("code = %v", c)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n != maxFlushFails {
		t.Errorf("tried %d times", f.n)
	}
}

func awarenessOut(t *testing.T, b *broker, file string) []yjs.AwarenessEntry {
	t.Helper()
	var out []yjs.AwarenessEntry
	for _, m := range b.on(topic.AwarenessOutTopic(file)) {
		d, err := yjs.Decode(m.Payload)
		if err != nil || d.Type != yjs.MsgAwareness {
			t.Fatalf("awareness out = %x, %v", m.Payload, err)
		}
		es, _ := yjs.ParseAwareness(d.Payload)
		out = append(out, es...)
	}
	return out
}

func aw(client, clock uint64, state string) []byte {
	return yjs.Awareness(yjs.EncodeAwareness([]yjs.AwarenessEntry{{Client: client, Clock: clock, State: state}}))
}

func TestAwarenessIdentityIsOverwritten(t *testing.T) {
	mem := store(t, "f")
	_, b := fixture(t, mem, Options{})
	b.from(alice, topic.AwarenessUpTopic("f", alice), aw(77, 1, `{"cursor":1,"user":{"name":"Root","id":"0","color":"#fff"}}`))
	eventually(t, "awareness", func() bool { return len(awarenessOut(t, b, "f")) == 1 })
	got := awarenessOut(t, b, "f")
	if got[0].Client != 77 {
		t.Fatalf("got %+v", got)
	}
	var st struct{ User map[string]string }
	_ = json.Unmarshal([]byte(got[0].State), &st)
	if st.User["name"] != "U1" || st.User["id"] != "1" || st.User["color"] != "#fff" {
		t.Errorf("state = %s", got[0].State)
	}
	if o := b.on(topic.AwarenessOutTopic("f"))[0]; o.Origin != alice || o.QoS != 0 {
		t.Errorf("out = %+v", o)
	}

	// a viewer shows a cursor too, as itself; and cannot speak for alice's awareness id
	time.Sleep(25 * time.Millisecond)
	b.from(viewer, topic.AwarenessUpTopic("f", viewer), aw(88, 1, `{"user":{"name":"Mallory"}}`))
	b.from(viewer, topic.AwarenessUpTopic("f", viewer), aw(77, 9, `{"user":{"name":"Alice"}}`))
	eventually(t, "viewer cursor", func() bool { return len(awarenessOut(t, b, "f")) == 2 })
	time.Sleep(50 * time.Millisecond)
	out := awarenessOut(t, b, "f")
	if len(out) != 2 || out[1].Client != 88 || !strings.Contains(out[1].State, `"U4"`) {
		t.Errorf("out = %+v", out)
	}

	// when alice goes (presence cleared), the others are told her cursor is gone
	b.from(alice, topic.PresenceTopic("f", alice), nil)
	eventually(t, "cursor removal", func() bool {
		for _, e := range awarenessOut(t, b, "f") {
			if e.Client == 77 && e.State == "null" && e.Clock == 2 {
				return true
			}
		}
		return false
	})
}

func TestPresenceBecomesARetainedRoster(t *testing.T) {
	_, b := fixture(t, store(t, "f"), Options{})
	b.from(alice, topic.PresenceTopic("f", alice), []byte(`{"state":"online"}`))
	b.from(viewer, topic.PresenceTopic("f", viewer), []byte(`{}`))
	fr, pr := topic.FileRosterTopic("f", alice), topic.ProjectRosterTopic("p", "f", alice)
	eventually(t, "roster", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.retained[fr] != nil && b.retained[pr] != nil && b.retained[topic.FileRosterTopic("f", viewer)] != nil
	})
	var e rosterEntry
	b.mu.Lock()
	_ = json.Unmarshal(b.retained[fr], &e)
	b.mu.Unlock()
	if e.ClientID != alice || e.Name != "U1" || e.Role != "editor" || e.FileID != "f" || e.ProjectID != "p" || e.UserID == nil || *e.UserID != "1" {
		t.Errorf("roster entry = %+v", e)
	}
	if m := b.on(fr)[0]; !m.Retain || m.Expiry != rosterExpiry {
		t.Errorf("roster message = %+v", m)
	}
	// a heartbeat soon after is not republished
	b.from(alice, topic.PresenceTopic("f", alice), []byte(`{}`))
	time.Sleep(50 * time.Millisecond)
	if n := len(b.on(fr)); n != 1 {
		t.Errorf("republished %d times", n)
	}
	// the Last Will (empty payload) clears both entries
	b.from(alice, topic.PresenceTopic("f", alice), nil)
	eventually(t, "cleared", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.retained[fr] == nil && b.retained[pr] == nil
	})
	b.mu.Lock()
	if b.retained[topic.FileRosterTopic("f", viewer)] == nil {
		t.Error("another client's entry was cleared")
	}
	b.mu.Unlock()
	// a stranger gets no roster entry
	b.from("u9.zzzzzzzz", topic.PresenceTopic("f", "u9.zzzzzzzz"), []byte(`{}`))
	time.Sleep(30 * time.Millisecond)
	if len(b.on(topic.FileRosterTopic("f", "u9.zzzzzzzz"))) != 0 {
		t.Error("a stranger was put on the roster")
	}
}

func rtc(b *broker, file, client string, v map[string]any) {
	p, _ := json.Marshal(v)
	b.from(client, topic.RTCUpTopic(file, client), p)
}

func TestAudioRoom(t *testing.T) {
	_, b := fixture(t, store(t, "f"), Options{MaxPeers: 2})
	rtc(b, "f", alice, map[string]any{"type": "rtc.join"})
	eventually(t, "alice joined", func() bool { return len(b.jsonOn(t, topic.RTCInboxTopic("f", alice), "rtc.joined")) == 1 })
	if p := b.jsonOn(t, topic.RTCInboxTopic("f", alice), "rtc.joined")[0]["peers"].([]any); len(p) != 0 {
		t.Errorf("alone: %v", p)
	}
	rtc(b, "f", bob, map[string]any{"type": "rtc.join"})
	eventually(t, "bob joined", func() bool { return len(b.jsonOn(t, topic.RTCInboxTopic("f", bob), "rtc.joined")) == 1 })
	if p := b.jsonOn(t, topic.RTCInboxTopic("f", bob), "rtc.joined")[0]["peers"].([]any); len(p) != 1 || p[0].(map[string]any)["name"] != "U1" {
		t.Errorf("bob sees %v", p)
	}
	eventually(t, "alice told", func() bool { return len(b.jsonOn(t, topic.RTCInboxTopic("f", alice), "rtc.peer-joined")) == 1 })
	if r := b.jsonOn(t, topic.FileEventsTopic("f"), "rtc.roster"); len(r) == 0 || len(r[len(r)-1]["peers"].([]any)) != 2 {
		t.Errorf("roster = %v", r)
	}

	// the room is full: the cap holds
	rtc(b, "f", carol, map[string]any{"type": "rtc.join"})
	eventually(t, "full", func() bool { return len(b.jsonOn(t, topic.RTCInboxTopic("f", carol), "rtc.full")) == 1 })
	if f := b.jsonOn(t, topic.RTCInboxTopic("f", carol), "rtc.full")[0]; f["code"] != "room_full" || f["max"] != float64(2) || f["count"] != float64(2) {
		t.Errorf("full = %v", f)
	}

	// signaling goes between members only
	rtc(b, "f", alice, map[string]any{"type": "rtc.signal", "to": bob, "kind": "offer", "data": map[string]any{"sdp": "x"}})
	rtc(b, "f", alice, map[string]any{"type": "rtc.signal", "to": carol, "kind": "offer", "data": map[string]any{"sdp": "x"}})
	rtc(b, "f", alice, map[string]any{"type": "rtc.signal", "to": bob, "kind": "bogus", "data": map[string]any{"sdp": "x"}})
	rtc(b, "f", carol, map[string]any{"type": "rtc.signal", "to": bob, "kind": "offer", "data": map[string]any{"sdp": "x"}})
	rtc(b, "f", alice, map[string]any{"type": "rtc.state", "muted": true})
	eventually(t, "signal", func() bool { return len(b.jsonOn(t, topic.RTCInboxTopic("f", bob), "rtc.signal")) == 1 })
	eventually(t, "state", func() bool { return len(b.jsonOn(t, topic.RTCInboxTopic("f", bob), "rtc.state")) == 1 })
	time.Sleep(50 * time.Millisecond)
	if s := b.jsonOn(t, topic.RTCInboxTopic("f", bob), "rtc.signal"); len(s) != 1 || s[0]["from"] != alice || s[0]["kind"] != "offer" {
		t.Errorf("bob's signals = %v", s)
	}
	if len(b.jsonOn(t, topic.RTCInboxTopic("f", carol), "rtc.signal")) != 0 {
		t.Error("a non-member got a signal")
	}

	// leaving (or the presence going) frees the place for carol
	b.from(bob, topic.PresenceTopic("f", bob), nil)
	eventually(t, "alice told bob left", func() bool { return len(b.jsonOn(t, topic.RTCInboxTopic("f", alice), "rtc.peer-left")) == 1 })
	rtc(b, "f", carol, map[string]any{"type": "rtc.join"})
	eventually(t, "carol joined", func() bool { return len(b.jsonOn(t, topic.RTCInboxTopic("f", carol), "rtc.joined")) == 1 })
}

func TestEventsCarryTheOriginAndControlMessages(t *testing.T) {
	mem := store(t, "f")
	var gmu sync.Mutex
	var gone []string
	h, b := fixture(t, mem, Options{OnFileGone: func(id string) { gmu.Lock(); gone = append(gone, id); gmu.Unlock() }})
	h.BroadcastFile("p", "f", map[string]any{"type": "file.updated", "fileId": "f"}, alice)
	h.BroadcastTree("p", map[string]any{"type": "tree.updated"}, "")
	eventually(t, "events", func() bool {
		return len(b.on(topic.FileEventsTopic("f"))) == 1 && len(b.on(topic.ProjectEventsTopic("p"))) == 2
	})
	if m := b.on(topic.FileEventsTopic("f"))[0]; m.Origin != alice || m.QoS != 1 || m.Retain {
		t.Errorf("file event = %+v", m)
	}
	if m := b.on(topic.ProjectEventsTopic("p")); m[0].Origin != alice || m[1].Origin != "" {
		t.Errorf("project events = %+v", m)
	}

	// a restore replaces the document: 1012, and what the instance held is dropped
	u := updates(t, "map values")
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(u[0]))
	eventually(t, "stored", func() bool { return len(stored(mem, "f").Updates) == 1 })
	_ = mem.YReset(context.Background(), "f")
	h.ResetFile("p", "f")
	eventually(t, "1012", func() bool { return len(b.jsonOn(t, topic.FileEventsTopic("f"), "ctl")) == 1 })
	if c := b.jsonOn(t, topic.FileEventsTopic("f"), "ctl")[0]; c["code"] != float64(CtlRestart) {
		t.Errorf("ctl = %v", c)
	}
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(u[0])) // not "known news" any more
	eventually(t, "stored again", func() bool { return len(stored(mem, "f").Updates) == 1 })

	h.CloseFile("p", "f")
	h.CloseProject("p", []string{"f", "g"})
	eventually(t, "4404", func() bool { return len(b.jsonOn(t, topic.ProjectEventsTopic("p"), "ctl")) == 1 })
	gmu.Lock()
	if len(gone) < 3 { // restore, delete, project delete (twice)
		t.Errorf("OnFileGone calls = %v", gone)
	}
	gmu.Unlock()
	for _, tp := range []string{topic.FileEventsTopic("f"), topic.FileEventsTopic("g")} {
		eventually(t, "4404 on "+tp, func() bool {
			for _, c := range b.jsonOn(t, tp, "ctl") {
				if c["code"] == float64(CtlDeleted) {
					return true
				}
			}
			return false
		})
	}
}

func TestRTCConfig(t *testing.T) {
	h, _ := fixture(t, nil, Options{MaxPeers: 4, ICEServers: []string{"x"}})
	c := h.RTCConfig()
	if !c.Enabled || c.MaxPeers != 4 {
		t.Errorf("config = %+v", c)
	}
	h2, _ := fixture(t, nil, Options{})
	if h2.RTCConfig().ICEServers == nil {
		t.Error("ICE servers must be a list, not null")
	}
}

func TestDeniedNotifiesTheClient(t *testing.T) {
	h, b := fixture(t, nil, Options{})
	h.Denied(viewer, "f")
	eventually(t, "denied", func() bool { return len(b.jsonOn(t, topic.CtlTopic(viewer), "denied")) == 1 })
}

// stalling is a store whose first write waits for its context to end.
type stalling struct {
	*diagramtest.Memory
	once    sync.Once
	started chan struct{}
}

func (s *stalling) YAppendMany(ctx context.Context, f string, d [][]byte) (int64, error) {
	stall := false
	s.once.Do(func() { stall = true })
	if stall {
		close(s.started)
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return s.Memory.YAppendMany(ctx, f, d)
}

func TestYjsCloseKeepsAFlushCutShort(t *testing.T) {
	s := &stalling{Memory: store(t, "f"), started: make(chan struct{})}
	h, b := fixture(t, nil, Options{Y: s})
	b.from(alice, topic.YjsUpTopic("f", alice), yjs.Update(updates(t, "map values")[0]))
	select {
	case <-s.started:
	case <-time.After(5 * time.Second):
		t.Fatal("no flush")
	}
	h.Close()
	st, _ := s.YLoad(context.Background(), "f")
	if len(st.Updates) != 1 {
		t.Errorf("stored %d updates, want 1", len(st.Updates))
	}
}
