// Package yjs is just enough of the Yjs wire formats for a server that
// relays and stores documents without merging them: the lib0 variable-length
// encodings, the y-websocket and y-protocols message framing, awareness
// updates, and a tracker of which structs and deletions an update stream
// already holds (so a joiner is asked only for what is missing and an update
// that carries nothing new is not stored).
//
// It never builds or applies a document.
package yjs

import (
	"errors"
	"math"
	"unicode/utf8"
)

// ErrMalformed is returned for bytes that are not what they claim to be.
var ErrMalformed = errors.New("yjs: malformed data")

// reader decodes lib0 values; the first failure sticks and later reads
// return zero values, so a decoder checks err once at the end.
type reader struct {
	b   []byte
	err error
}

func (r *reader) fail() { r.err = ErrMalformed }

func (r *reader) left() int { return len(r.b) }

func (r *reader) byte() byte {
	if r.err != nil || len(r.b) == 0 {
		r.fail()
		return 0
	}
	c := r.b[0]
	r.b = r.b[1:]
	return c
}

// uint reads a lib0 varUint (7 bits per byte, low first). Values past 2^53
// are refused: Yjs clocks and lengths are JavaScript numbers.
func (r *reader) uint() uint64 {
	var v uint64
	for shift := 0; shift < 63; shift += 7 {
		c := r.byte()
		if r.err != nil {
			return 0
		}
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			if v > 1<<53 {
				r.fail()
				return 0
			}
			return v
		}
	}
	r.fail()
	return 0
}

// int reads a varUint that fits an int64 (they all do: uint caps at 2^53).
func (r *reader) int() int64 {
	v := r.uint()
	if v > math.MaxInt64 {
		r.fail()
		return 0
	}
	return int64(v)
}

// varint reads a lib0 signed varInt (the first byte holds 6 bits and the sign).
func (r *reader) varint() int64 {
	c := r.byte()
	v := int64(c & 0x3f)
	neg := c&0x40 != 0
	if c&0x80 != 0 {
		for shift := 6; shift < 63; shift += 7 {
			c = r.byte()
			if r.err != nil {
				return 0
			}
			v |= int64(c&0x7f) << shift
			if c < 0x80 {
				break
			}
		}
	}
	if neg {
		return -v
	}
	return v
}

func (r *reader) skip(n uint64) {
	if r.err != nil || n > uint64(len(r.b)) {
		r.fail()
		return
	}
	r.b = r.b[n:]
}

func (r *reader) bytes() []byte {
	n := r.uint()
	if r.err != nil || n > uint64(len(r.b)) {
		r.fail()
		return nil
	}
	out := r.b[:n:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) string() string {
	b := r.bytes()
	if r.err == nil && !utf8.Valid(b) {
		r.fail()
	}
	return string(b)
}

// utf16Len is the length of a string in UTF-16 code units, which is how
// Yjs counts the characters of a text item.
func utf16Len(s string) uint64 {
	var n uint64
	for _, c := range s {
		if c >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

const maxAnyDepth = 64

// any skips one lib0 "any" value.
func (r *reader) any(depth int) {
	if depth > maxAnyDepth {
		r.fail()
		return
	}
	switch tag := r.byte(); tag {
	case 127, 126, 121, 120: // undefined, null, false, true
	case 125:
		r.varint()
	case 124:
		r.skip(4)
	case 123, 122:
		r.skip(8)
	case 119, 116:
		r.bytes()
	case 118:
		for n := r.count(); n > 0 && r.err == nil; n-- {
			r.bytes()
			r.any(depth + 1)
		}
	case 117:
		for n := r.count(); n > 0 && r.err == nil; n-- {
			r.any(depth + 1)
		}
	default:
		r.fail()
	}
}

// count reads a length that cannot exceed the bytes left (each element is at
// least one byte), so a lie cannot make a decoder loop for long.
func (r *reader) count() uint64 {
	n := r.uint()
	if n > uint64(len(r.b)) {
		r.fail()
		return 0
	}
	return n
}

// Encoding

func appendUint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendBytes(b, v []byte) []byte { return append(appendUint(b, uint64(len(v))), v...) }

func appendString(b []byte, s string) []byte { return appendBytes(b, []byte(s)) }
