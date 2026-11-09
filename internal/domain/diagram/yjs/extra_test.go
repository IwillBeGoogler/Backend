package yjs

import (
	"bytes"
	"strings"
	"testing"
)

// u builds a lib0 varUint sequence.
func u(vs ...uint64) []byte {
	var b []byte
	for _, v := range vs {
		b = appendUint(b, v)
	}
	return b
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// oneStruct wraps a single struct (clock 0) of client 1 in an update with an
// empty delete set.
func oneStruct(s []byte) []byte { return cat(u(1, 1, 1, 0), s, u(0)) }

func TestParseStructKinds(t *testing.T) {
	root := cat(u(1), appendString(nil, "k")) // parent: root type key
	cases := []struct {
		name string
		s    []byte
		ok   bool
		n    uint64 // expected length covered
	}{
		{"gc", cat([]byte{0}, u(3)), true, 3},
		{"skip", cat([]byte{10}, u(3)), true, 0},
		{"deleted", cat([]byte{1}, root, u(4)), true, 4},
		{"deleted origin", cat([]byte{0x81}, u(7, 7), u(2)), true, 2},
		{"deleted right origin", cat([]byte{0x41}, u(7, 7), u(2)), true, 2},
		{"deleted both origins", cat([]byte{0xc1}, u(7, 7, 8, 8), u(2)), true, 2},
		{"parent id", cat([]byte{1}, u(0, 5, 5), u(2)), true, 2},
		{"parent sub", cat([]byte{0x21}, root, appendString(nil, "sub"), u(2)), true, 2},
		{"json", cat([]byte{2}, root, u(2), appendString(nil, "{}"), appendString(nil, "[]")), true, 2},
		{"binary", cat([]byte{3}, root, appendBytes(nil, []byte{1, 2})), true, 1},
		{"string", cat([]byte{4}, root, appendString(nil, "a\U0001F600b")), true, 4},
		{"embed", cat([]byte{5}, root, appendString(nil, "{}")), true, 1},
		{"format", cat([]byte{6}, root, appendString(nil, "bold"), appendString(nil, "true")), true, 1},
		{"type plain", cat([]byte{7}, root, u(0)), true, 1},
		{"type xml element", cat([]byte{7}, root, u(3), appendString(nil, "div")), true, 1},
		{"type xml hook", cat([]byte{7}, root, u(5), appendString(nil, "h")), true, 1},
		{"type bad", cat([]byte{7}, root, u(7)), false, 0},
		{"any", cat([]byte{8}, root, u(2), []byte{127}, []byte{120}), true, 2},
		{"doc", cat([]byte{9}, root, appendString(nil, "guid"), []byte{121}), true, 1},
		{"unknown ref", cat([]byte{11}, root), false, 0},
		{"truncated", []byte{4}, false, 0},
	}
	for _, c := range cases {
		p, err := Parse(oneStruct(c.s))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v", c.name, err)
			continue
		}
		if !c.ok {
			continue
		}
		got := uint64(0)
		for _, sp := range p.Structs[1] {
			got += sp.end - sp.start
		}
		if got != c.n {
			t.Errorf("%s: covered %d, want %d", c.name, got, c.n)
		}
		if (c.n == 0) != p.Empty() {
			t.Errorf("%s: Empty=%v", c.name, p.Empty())
		}
	}
}

func TestParseOverflowAndTrailing(t *testing.T) {
	// struct clock past 2^53
	if _, err := Parse(cat(u(1, 1, 1, 1<<53), []byte{0}, u(1), u(0))); err == nil {
		t.Error("struct clock overflow accepted")
	}
	// delete clock past 2^53
	if _, err := Parse(cat(u(0), u(1, 1, 1, 1<<53, 1))); err == nil {
		t.Error("delete clock overflow accepted")
	}
	// zero-length delete is ignored
	p, err := Parse(cat(u(0), u(1, 1, 1, 5, 0)))
	if err != nil || !p.Empty() {
		t.Errorf("zero delete: %+v %v", p, err)
	}
	// real delete
	p, err = Parse(cat(u(0), u(1, 1, 1, 5, 2)))
	if err != nil || p.Empty() || len(p.Deletes[1]) != 1 {
		t.Errorf("delete: %+v %v", p, err)
	}
	// trailing bytes
	if _, err := Parse(cat(u(0, 0), []byte{9})); err == nil {
		t.Error("trailing accepted")
	}
	// zero-count GC adds nothing
	p, err = Parse(oneStruct(cat([]byte{0}, u(0))))
	if err != nil || !p.Empty() {
		t.Errorf("zero gc: %+v %v", p, err)
	}
}

func TestObserveError(t *testing.T) {
	tr := NewTracker()
	if _, err := tr.Observe([]byte{0xff}); err == nil {
		t.Fatal("bad update accepted")
	}
	novel, err := tr.Observe(oneStruct(cat([]byte{0}, u(3))))
	if err != nil || !novel {
		t.Fatal("first observe")
	}
	novel, _ = tr.Observe(oneStruct(cat([]byte{0}, u(3))))
	if novel {
		t.Fatal("repeat observe novel")
	}
}

func TestSpansAddBranches(t *testing.T) {
	var s spans
	if s.add(5, 5) || s.add(6, 5) {
		t.Fatal("empty range reported new")
	}
	s.add(10, 20)
	s.add(0, 5)   // before existing -> placed in front
	s.add(30, 40) // after
	s.add(20, 30) // touches both sides -> merged
	if len(s) != 2 || s[1] != (span{10, 40}) {
		t.Fatalf("%v", s)
	}
	if s.add(12, 15) {
		t.Fatal("contained range reported new")
	}
}

func TestLib0Edges(t *testing.T) {
	// multi-byte varint inside an "any" (tag 125)
	r := &reader{b: []byte{125, 0xc1, 0x01}}
	r.any(0)
	if r.err != nil || r.left() != 0 {
		t.Fatalf("varint any: %v", r.err)
	}
	// a negative multi-byte varint value
	r = &reader{b: []byte{0xc1, 0x01}}
	if v := r.varint(); v != -(1 | 1<<6) {
		t.Fatalf("varint = %d", v)
	}
	// truncated continuation
	r = &reader{b: []byte{0x80}}
	r.varint()
	if r.err == nil {
		t.Fatal("truncated varint accepted")
	}
	// overlong continuation chain stops at 63 bits
	r = &reader{b: bytes.Repeat([]byte{0xff}, 12)}
	r.varint()
	// int() reads a normal value
	r = &reader{b: u(42)}
	if r.int() != 42 {
		t.Fatal("int")
	}
	// varUint too long
	r = &reader{b: bytes.Repeat([]byte{0xff}, 12)}
	r.uint()
	if r.err == nil {
		t.Fatal("overlong uint accepted")
	}
	// varUint past 2^53
	r = &reader{b: u(1<<53 + 1)}
	r.uint()
	if r.err == nil {
		t.Fatal("huge uint accepted")
	}
	// invalid utf-8 string
	r = &reader{b: appendBytes(nil, []byte{0xff, 0xfe})}
	r.string()
	if r.err == nil {
		t.Fatal("bad utf8 accepted")
	}
}

func TestAnyKinds(t *testing.T) {
	ok := [][]byte{
		{127}, {126}, {121}, {120},
		cat([]byte{124}, make([]byte, 4)),
		cat([]byte{123}, make([]byte, 8)),
		cat([]byte{122}, make([]byte, 8)),
		cat([]byte{119}, appendString(nil, "s")),
		cat([]byte{116}, appendBytes(nil, []byte{1})),
		cat([]byte{118}, u(1), appendString(nil, "k"), []byte{127}),
		cat([]byte{117}, u(2), []byte{127}, []byte{126}),
	}
	for i, b := range ok {
		r := &reader{b: b}
		r.any(0)
		if r.err != nil || r.left() != 0 {
			t.Errorf("case %d: %v left=%d", i, r.err, r.left())
		}
	}
	for i, b := range [][]byte{{1}, {}, {124, 0}} {
		r := &reader{b: b}
		r.any(0)
		if r.err == nil {
			t.Errorf("bad case %d accepted", i)
		}
	}
	// nesting deeper than maxAnyDepth
	deep := bytes.Repeat(cat([]byte{117}, u(1)), maxAnyDepth+3)
	deep = append(deep, 127)
	r := &reader{b: deep}
	r.any(0)
	if r.err == nil {
		t.Error("deep nesting accepted")
	}
}

func TestFramingEdges(t *testing.T) {
	if _, err := Decode(nil); err == nil {
		t.Error("empty frame accepted")
	}
	if _, err := Decode([]byte{0x80, 0}); err == nil {
		t.Error("multi-byte type accepted")
	}
	if _, err := Decode([]byte{MsgSync, SyncUpdate + 1, 0}); err == nil {
		t.Error("bad sync sub accepted")
	}
	if _, err := Decode([]byte{99}); err == nil {
		t.Error("unknown type accepted")
	}
	m, err := Decode(CompactHint(-5))
	if err != nil || m.Type != MsgCompactHint || m.Seq != 0 {
		t.Errorf("negative seq: %+v %v", m, err)
	}
	m, err = Decode(Compact(9, []byte("snap")))
	if err != nil || m.Seq != 9 || string(m.Payload) != "snap" {
		t.Errorf("%+v %v", m, err)
	}
	m, err = Decode(Denied("read-only"))
	if err != nil || m.Reason != "read-only" {
		t.Errorf("%+v %v", m, err)
	}
	m, err = Decode(Awareness([]byte{0}))
	if err != nil || m.Type != MsgAwareness {
		t.Errorf("%+v %v", m, err)
	}
}

func TestAwarenessEdges(t *testing.T) {
	// trailing bytes after the entries
	if _, err := ParseAwareness(append(EncodeAwareness(nil), 7)); err == nil {
		t.Error("trailing accepted")
	}
	if _, _, err := SanitizeAwareness([]byte{0xff}, Identity{}, nil); err == nil {
		t.Error("bad awareness accepted")
	}
	long := strings.Repeat("é", 150)
	st := `{"user":{"name":"evil","color":"` + strings.Repeat("c", 40) + `"},"x":1}`
	out, ids, err := SanitizeAwareness(EncodeAwareness([]AwarenessEntry{
		{Client: 1, Clock: 1, State: st},
		{Client: 2, Clock: 1, State: st},
	}), Identity{ID: "u1", Name: long + "\x00"}, func(c uint64) bool { return c == 1 })
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("%v %v", ids, err)
	}
	es, _ := ParseAwareness(out)
	if len(es) != 1 || strings.Contains(es[0].State, "evil") || !strings.Contains(es[0].State, `"id":"u1"`) ||
		strings.Contains(es[0].State, strings.Repeat("c", 21)) || strings.Contains(es[0].State, "\\u0000") {
		t.Fatalf("%s", es[0].State)
	}
	if strings.Count(es[0].State, "é") != 100 {
		t.Errorf("name not capped: %s", es[0].State)
	}
	// guest, no claim filter, unusable states become null
	out, _, _ = SanitizeAwareness(EncodeAwareness([]AwarenessEntry{
		{Client: 1, State: "null"}, {Client: 2, State: "[1]"}, {Client: 3, State: "nope"},
		{Client: 4, State: strings.Repeat("x", MaxAwarenessState+1)}, {Client: 5, State: `{}`},
	}), Identity{}, nil)
	es, _ = ParseAwareness(out)
	for i, e := range es[:4] {
		if e.State != "null" {
			t.Errorf("entry %d state %s", i, e.State)
		}
	}
	if !strings.Contains(es[4].State, `"name":"Guest"`) || strings.Contains(es[4].State, `"id"`) {
		t.Errorf("guest: %s", es[4].State)
	}
}
