package diagramhub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/topic"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/yjs"
)

// Two API replicas share one database; the shared subscription splits one
// file's updates between them. Each replica has its own room.
func TestTwoReplicasOneFile(t *testing.T) {
	mem := store(t, "f")
	ha, ba := fixture(t, mem, Options{})
	_, bb := fixture(t, mem, Options{})
	ctx := context.Background()
	u := updates(t, "compaction")
	snap := prefixes
	up := func(b *broker, c string, i int) { b.from(c, topic.YjsUpTopic("f", c), yjs.Update(u[i])) }

	// B loads its room (a join), then A gets 0, B gets 1, A gets 2
	bb.from(bob, topic.YjsReqTopic("f", bob), yjs.Step1([]byte{0}))
	eventually(t, "B joined", func() bool { return len(bb.on(topic.YjsSyncTopic("f", bob))) >= 2 })
	up(ba, alice, 0)
	up(bb, bob, 1)
	up(ba, alice, 2)
	eventually(t, "three stored", func() bool { return len(stored(mem, "f").Updates) == 3 })
	st := stored(mem, "f")
	for i, x := range st.Updates {
		if x.Seq != int64(i+1) {
			t.Fatalf("seqs = %+v", st.Updates)
		}
	}
	if len(downFrames(ba, "f"))+len(downFrames(bb, "f")) != 3 {
		t.Error("every update must be relayed once")
	}
	// compaction checks the database, not the replica's tracker
	if ok, err := ha.Compact(ctx, "f", 3, snap[2]); !ok || err != nil {
		t.Fatalf("compact on A = %v, %v", ok, err)
	}
	// B never saw the compaction: its next flush must not hint as if nothing was compacted
	up(bb, bob, 3)
	eventually(t, "update 4", func() bool { st := stored(mem, "f"); return len(st.Updates) == 1 && st.Updates[0].Seq == 4 })
	time.Sleep(60 * time.Millisecond)
	for _, m := range bb.on(topic.YjsSyncTopic("f", bob)) {
		if d, err := yjs.Decode(m.Payload); err == nil && d.Type == yjs.MsgCompactHint && d.Seq == 4 {
			t.Errorf("replica B hinted at seq 4 with one update since the snapshot")
		}
	}
	if _, err := ha.Compact(ctx, "f", 4, snap[1]); !errors.Is(err, ErrIncomplete) {
		t.Errorf("incomplete snapshot = %v", err)
	}
}
