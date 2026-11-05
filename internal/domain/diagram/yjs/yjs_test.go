package yjs

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fixture is real Yjs output (testdata/updates.json, made with yjs 13):
// updates in the order a client produced them, the state vector Yjs itself
// computes for them, and their merge.
type fixture struct {
	Name    string   `json:"name"`
	Updates []string `json:"updates"`
	SV      [][]any  `json:"sv"`
	Merged  string   `json:"merged"`
}

func fixtures(t *testing.T) []fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/updates.json")
	if err != nil {
		t.Fatal(err)
	}
	var fs []fixture
	if err := json.Unmarshal(b, &fs); err != nil {
		t.Fatal(err)
	}
	return fs
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// decodeSV reads a state vector into client -> clock.
func decodeSV(t *testing.T, b []byte) map[uint64]uint64 {
	t.Helper()
	r := &reader{b: b}
	out := map[uint64]uint64{}
	for n := r.uint(); n > 0; n-- {
		c := r.uint()
		out[c] = r.uint()
	}
	if r.err != nil || r.left() != 0 {
		t.Fatalf("bad state vector %x", b)
	}
	return out
}

func TestTrackerMatchesYjsStateVectors(t *testing.T) {
	for _, f := range fixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			tr := NewTracker()
			for i, u := range f.Updates {
				novel, err := tr.Observe(unhex(t, u))
				if err != nil {
					t.Fatalf("update %d: %v", i, err)
				}
				if !novel && f.Name != "empty" {
					t.Errorf("update %d held nothing new", i)
				}
				if novel, _ := tr.Observe(unhex(t, u)); novel {
					t.Errorf("update %d is new twice", i)
				}
			}
			want := map[uint64]uint64{}
			for _, e := range f.SV {
				c, _ := strconv.ParseUint(e[0].(string), 10, 64)
				want[c] = uint64(e[1].(float64))
			}
			got := decodeSV(t, tr.StateVector())
			if len(got) != len(want) {
				t.Fatalf("state vector = %v, want %v", got, want)
			}
			for c, k := range want {
				if got[c] != k {
					t.Errorf("client %d: clock %d, want %d", c, got[c], k)
				}
			}
			// the merge (what a compacted snapshot is) covers the same history
			whole := NewTracker()
			if _, err := whole.Observe(unhex(t, f.Merged)); err != nil {
				t.Fatalf("merged update: %v", err)
			}
			if !whole.Covers(tr) || !tr.Covers(whole) {
				t.Error("the merged update and its parts disagree")
			}
		})
	}
}

func TestParseRefusesDamagedUpdates(t *testing.T) {
	for _, f := range fixtures(t) {
		u := unhex(t, f.Updates[0])
		if len(u) < 4 {
			continue
		}
		if _, err := Parse(append(slices.Clone(u), 0)); err == nil {
			t.Errorf("%s: trailing byte accepted", f.Name)
		}
		for cut := 1; cut < len(u); cut++ { // every truncation must fail or decode to something else, never panic
			_, _ = Parse(u[:cut])
		}
	}
	for _, bad := range []string{"", "ff", "01ffffffffffffffffff7f", "0101010101", "010101017f"} {
		if _, err := Parse(unhex(t, bad)); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestTrackerNovelty(t *testing.T) {
	fs := fixtures(t)
	var twoClients fixture
	for _, f := range fs {
		if f.Name == "two clients" {
			twoClients = f
		}
	}
	a, b := unhex(t, twoClients.Updates[0]), unhex(t, twoClients.Updates[1])
	tr := NewTracker()
	pa, _ := Parse(a)
	pb, _ := Parse(b)
	if !tr.Novel(pa) || !tr.Novel(pb) {
		t.Fatal("nothing is new to an empty tracker")
	}
	tr.Merge(pa)
	if tr.Novel(pa) || !tr.Novel(pb) {
		t.Error("novelty after merging a")
	}
	other := NewTracker()
	other.Merge(pa)
	other.Merge(pb)
	if tr.Covers(other) || !other.Covers(tr) {
		t.Error("Covers")
	}
}

func TestSpans(t *testing.T) {
	var s spans
	for _, c := range []struct {
		a, b uint64
		add  bool
	}{{0, 3, true}, {5, 8, true}, {3, 5, true}, {1, 2, false}, {7, 10, true}, {20, 21, true}, {0, 10, false}, {10, 11, true}} {
		if got := s.add(c.a, c.b); got != c.add {
			t.Errorf("add(%d,%d) = %v", c.a, c.b, got)
		}
	}
	if len(s) != 2 || s[0] != (span{0, 11}) || s[1] != (span{20, 21}) {
		t.Errorf("spans = %v", s)
	}
}

func TestFraming(t *testing.T) {
	sv := []byte{1, 5, 9}
	for _, c := range []struct {
		frame []byte
		want  Message
	}{
		{Step1(sv), Message{Type: MsgSync, Sub: SyncStep1, Payload: sv}},
		{Step2(EmptyUpdate), Message{Type: MsgSync, Sub: SyncStep2, Payload: EmptyUpdate}},
		{Update([]byte{7, 7}), Message{Type: MsgSync, Sub: SyncUpdate, Payload: []byte{7, 7}}},
		{Awareness([]byte{0}), Message{Type: MsgAwareness, Payload: []byte{0}}},
		{Denied("read-only"), Message{Type: MsgDenied, Reason: "read-only"}},
		{CompactHint(300), Message{Type: MsgCompactHint, Seq: 300}},
		{Compact(5, []byte{0, 0}), Message{Type: MsgCompact, Seq: 5, Payload: []byte{0, 0}}},
	} {
		got, err := Decode(c.frame)
		if err != nil || got.Type != c.want.Type || got.Sub != c.want.Sub || got.Seq != c.want.Seq ||
			got.Reason != c.want.Reason || !bytes.Equal(got.Payload, c.want.Payload) {
			t.Errorf("Decode(%x) = %+v, %v; want %+v", c.frame, got, err, c.want)
		}
	}
	// the bytes y-protocols writes: [0 sync][0 step1][len][state vector]
	if !bytes.Equal(Step1(sv), []byte{0, 0, 3, 1, 5, 9}) {
		t.Errorf("Step1 = %x", Step1(sv))
	}
	for _, bad := range [][]byte{nil, {9}, {0}, {0, 3, 0}, {0, 0, 5, 1}, {1, 4, 1}, {2, 3, 'a'}} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%x) accepted", bad)
		}
	}
}

func TestVarUint(t *testing.T) {
	for _, v := range []uint64{0, 1, 127, 128, 300, 1 << 32, 1<<53 - 1} {
		r := &reader{b: appendUint(nil, v)}
		if got := r.uint(); got != v || r.err != nil || r.left() != 0 {
			t.Errorf("uint %d round trip = %d, %v", v, got, r.err)
		}
	}
	r := &reader{b: appendUint(nil, 1<<60)}
	if r.uint(); r.err == nil {
		t.Error("a clock past 2^53 accepted")
	}
}

func TestSanitizeAwareness(t *testing.T) {
	huge := `{"x":"` + strings.Repeat("a", MaxAwarenessState) + `"}`
	in := EncodeAwareness([]AwarenessEntry{
		{1, 4, `{"cursor":{"x":1},"user":{"name":"Admin","id":"1","color":"#ff0000"}}`},
		{2, 1, `null`},
		{3, 1, `[1,2]`},
		{4, 1, huge},
		{5, 1, `{"user":{"name":"x"}}`},
	})
	out, ids, err := SanitizeAwareness(in, Identity{ID: "42", Name: "Mallory\x07 "}, func(c uint64) bool { return c != 5 })
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []uint64{1, 2, 3, 4}) {
		t.Errorf("ids = %v", ids)
	}
	es, err := ParseAwareness(out)
	if err != nil || len(es) != 4 {
		t.Fatalf("out = %+v, %v", es, err)
	}
	var s struct {
		Cursor map[string]float64
		User   map[string]string
	}
	if err := json.Unmarshal([]byte(es[0].State), &s); err != nil {
		t.Fatal(err)
	}
	if s.User["name"] != "Mallory " || s.User["id"] != "42" || s.User["color"] != "#ff0000" || s.Cursor["x"] != 1 {
		t.Errorf("state = %s", es[0].State)
	}
	for i := 1; i < 4; i++ {
		if es[i].State != "null" {
			t.Errorf("entry %d state = %s, want null", i, es[i].State)
		}
	}
	if es[0].Clock != 4 {
		t.Errorf("clock = %d", es[0].Clock)
	}
	guest, _, _ := SanitizeAwareness(EncodeAwareness([]AwarenessEntry{{1, 1, `{"user":{"id":"9"}}`}}), Identity{Name: ""}, nil)
	ge, _ := ParseAwareness(guest)
	if strings.Contains(ge[0].State, `"id"`) || !strings.Contains(ge[0].State, `"Guest"`) {
		t.Errorf("guest state = %s", ge[0].State)
	}
	if _, _, err := SanitizeAwareness([]byte{5, 1}, Identity{}, nil); err == nil {
		t.Error("truncated awareness accepted")
	}
}
