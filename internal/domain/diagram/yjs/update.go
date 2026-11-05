package yjs

import (
	"cmp"
	"slices"
	"sort"
)

// span is the clock range [start, end) of one client.
type span struct{ start, end uint64 }

// spans is a sorted list of disjoint, non-adjacent clock ranges.
type spans []span

// add merges [start, end) in and reports whether it covered anything new.
func (s *spans) add(start, end uint64) bool {
	if end <= start {
		return false
	}
	if s.contains(start, end) {
		return false
	}
	out := make(spans, 0, len(*s)+1)
	placed := false
	for _, x := range *s {
		switch {
		case x.end < start: // before
			out = append(out, x)
		case x.start > end: // after
			if !placed {
				out, placed = append(out, span{start, end}), true
			}
			out = append(out, x)
		default: // overlaps or touches: absorb
			start, end = min(start, x.start), max(end, x.end)
		}
	}
	if !placed {
		out = append(out, span{start, end})
	}
	*s = out
	return true
}

func (s spans) contains(start, end uint64) bool {
	i := sort.Search(len(s), func(i int) bool { return s[i].end >= end })
	return i < len(s) && s[i].start <= start
}

// Parsed is what an update holds: the clock ranges of its structs and of its
// delete set, per client.
type Parsed struct {
	Structs map[uint64]spans
	Deletes map[uint64]spans
}

// Empty reports whether the update carries nothing.
func (p Parsed) Empty() bool { return len(p.Structs) == 0 && len(p.Deletes) == 0 }

func (p *Parsed) addStruct(client, clock, n uint64) {
	if n == 0 {
		return
	}
	s := p.Structs[client]
	s.add(clock, clock+n)
	p.Structs[client] = s
}

// maxClock is where Yjs clocks stop being exact (JavaScript numbers).
const maxClock = 1 << 53

// Parse reads a Yjs update (format v1) far enough to learn what it covers.
// Anything that does not decode exactly, to the last byte, is an error.
func Parse(update []byte) (Parsed, error) {
	r := &reader{b: update}
	p := Parsed{Structs: map[uint64]spans{}, Deletes: map[uint64]spans{}}
	for clients := r.count(); clients > 0 && r.err == nil; clients-- {
		n, client, clock := r.count(), r.uint(), r.uint()
		for ; n > 0 && r.err == nil; n-- {
			length, covers := r.structLen()
			if clock+length > maxClock {
				r.fail()
			}
			if covers {
				p.addStruct(client, clock, length)
			}
			clock += length
		}
	}
	for clients := r.count(); clients > 0 && r.err == nil; clients-- {
		client := r.uint()
		for n := r.count(); n > 0 && r.err == nil; n-- {
			clock, length := r.uint(), r.uint()
			if clock+length > maxClock {
				r.fail()
			}
			if length > 0 {
				s := p.Deletes[client]
				s.add(clock, clock+length)
				p.Deletes[client] = s
			}
		}
	}
	if r.err == nil && r.left() != 0 {
		r.fail()
	}
	if r.err != nil {
		return Parsed{}, r.err
	}
	return p, nil
}

// structLen reads one struct and returns its length in clock ticks and
// whether it counts toward the client's state (a Skip does not).
func (r *reader) structLen() (length uint64, covers bool) {
	info := r.byte()
	switch ref := info & 0x1f; ref {
	case 0: // GC
		return r.uint(), true
	case 10: // Skip
		return r.uint(), false
	}
	if info&0xc0 != 0 { // origin and/or right origin: an ID each
		if info&0x80 != 0 {
			r.uint()
			r.uint()
		}
		if info&0x40 != 0 {
			r.uint()
			r.uint()
		}
	}
	if info&0xc0 == 0 { // no origins: the parent is named
		if r.uint() == 1 {
			r.bytes() // a root type's key
		} else {
			r.uint()
			r.uint() // the parent item's ID
		}
		if info&0x20 != 0 {
			r.bytes() // parentSub
		}
	}
	switch ref := info & 0x1f; ref {
	case 1: // Deleted
		return r.uint(), true
	case 2: // JSON
		n := r.count()
		for i := uint64(0); i < n && r.err == nil; i++ {
			r.bytes()
		}
		return n, true
	case 3: // Binary
		r.bytes()
		return 1, true
	case 4: // String
		return utf16Len(r.string()), true
	case 5: // Embed
		r.bytes()
		return 1, true
	case 6: // Format
		r.bytes()
		r.bytes()
		return 1, true
	case 7: // Type
		if t := r.uint(); t == 3 || t == 5 { // XmlElement, XmlHook: named
			r.bytes()
		} else if t > 6 {
			r.fail()
		}
		return 1, true
	case 8: // Any
		n := r.count()
		for i := uint64(0); i < n && r.err == nil; i++ {
			r.any(0)
		}
		return n, true
	case 9: // Doc
		r.bytes()
		r.any(0)
		return 1, true
	}
	r.fail()
	return 0, false
}

// Tracker remembers which structs and deletions the updates it has seen
// hold. It is the server's stand-in for a document: enough to compute a
// state vector, to tell whether an update has anything new, and to compare
// two histories, without ever applying an update.
type Tracker struct {
	structs map[uint64]spans
	deletes map[uint64]spans
}

func NewTracker() *Tracker {
	return &Tracker{structs: map[uint64]spans{}, deletes: map[uint64]spans{}}
}

// Novel reports whether p has a struct or a deletion the tracker lacks.
func (t *Tracker) Novel(p Parsed) bool {
	return novel(t.structs, p.Structs) || novel(t.deletes, p.Deletes)
}

func novel(have, got map[uint64]spans) bool {
	for client, ss := range got {
		h := have[client]
		for _, s := range ss {
			if !h.contains(s.start, s.end) {
				return true
			}
		}
	}
	return false
}

// Merge records p.
func (t *Tracker) Merge(p Parsed) {
	for client, ss := range p.Structs {
		h := t.structs[client]
		for _, s := range ss {
			h.add(s.start, s.end)
		}
		t.structs[client] = h
	}
	for client, ss := range p.Deletes {
		h := t.deletes[client]
		for _, s := range ss {
			h.add(s.start, s.end)
		}
		t.deletes[client] = h
	}
}

// Observe parses update, merges it and reports whether it had anything new.
func (t *Tracker) Observe(update []byte) (bool, error) {
	p, err := Parse(update)
	if err != nil {
		return false, err
	}
	n := t.Novel(p)
	t.Merge(p)
	return n, nil
}

// Covers reports whether everything o holds is also held here.
func (t *Tracker) Covers(o *Tracker) bool {
	return !novel(t.structs, o.structs) && !novel(t.deletes, o.deletes)
}

// StateVector encodes, per client, the clock up to which the structs are
// present from clock 0 without a gap (the Yjs state vector).
func (t *Tracker) StateVector() []byte {
	type entry struct{ client, clock uint64 }
	var es []entry
	for client, ss := range t.structs {
		if len(ss) > 0 && ss[0].start == 0 {
			es = append(es, entry{client, ss[0].end})
		}
	}
	slices.SortFunc(es, func(a, b entry) int { return cmp.Compare(b.client, a.client) })
	out := appendUint(nil, uint64(len(es)))
	for _, e := range es {
		out = appendUint(appendUint(out, e.client), e.clock)
	}
	return out
}
