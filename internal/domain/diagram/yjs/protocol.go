package yjs

// y-websocket message types. 0 to 2 are the standard ones; 3 and 4 are this
// server's compaction extension (see Compact and CompactHint).
const (
	MsgSync      = 0
	MsgAwareness = 1
	MsgDenied    = 2
	// MsgCompact is [3][varUint seq][varUint8Array snapshot]: a client that
	// holds every update up to seq sends the whole document as one update.
	MsgCompact = 3
	// MsgCompactHint is [4][varUint seq]: the server asks for that.
	MsgCompactHint = 4
)

// y-protocols sync sub-types.
const (
	SyncStep1  = 0
	SyncStep2  = 1
	SyncUpdate = 2
)

// EmptyUpdate is an update with no structs and no deletions; applying it
// does nothing. The server ends a join with it as sync step 2, so the
// client knows it has received everything.
var EmptyUpdate = []byte{0, 0}

func sync(sub byte, payload []byte) []byte {
	return appendBytes([]byte{MsgSync, sub}, payload)
}

// Step1 is sync step 1: "here is my state vector, send me what I lack".
func Step1(stateVector []byte) []byte { return sync(SyncStep1, stateVector) }

// Step2 is sync step 2: the answer, an update.
func Step2(update []byte) []byte { return sync(SyncStep2, update) }

// Update is a live update.
func Update(update []byte) []byte { return sync(SyncUpdate, update) }

// Denied tells a client its write was refused (a read-only role).
func Denied(reason string) []byte { return appendString([]byte{MsgDenied}, reason) }

// Awareness wraps an awareness update.
func Awareness(update []byte) []byte { return appendBytes([]byte{MsgAwareness}, update) }

// CompactHint asks a client to send a compacted snapshot as of seq.
func CompactHint(seq int64) []byte { return appendUint([]byte{MsgCompactHint}, unsigned(seq)) }

// Compact is a client's compacted snapshot as of seq.
func Compact(seq int64, snapshot []byte) []byte {
	return appendBytes(appendUint([]byte{MsgCompact}, unsigned(seq)), snapshot)
}

// unsigned is a sequence number as a varUint (never negative).
func unsigned(seq int64) uint64 {
	if seq < 0 {
		return 0
	}
	return uint64(seq)
}

// Message is a decoded frame. Type is a Msg* constant; for MsgSync, Sub is
// the sync sub-type and Payload the state vector or update; for MsgAwareness
// Payload is the awareness update; for MsgCompact Seq and Payload; for
// MsgDenied Reason.
type Message struct {
	Type    int
	Sub     int
	Payload []byte
	Seq     int64
	Reason  string
}

// Decode parses one binary frame.
func Decode(frame []byte) (Message, error) {
	r := &reader{b: frame}
	m := Message{Type: int(r.byte())}
	if r.err == nil && frame[0] >= 0x80 { // message types are one byte
		r.fail()
	}
	switch m.Type {
	case MsgSync:
		m.Sub = int(r.byte())
		if m.Sub > SyncUpdate {
			r.fail()
		}
		m.Payload = r.bytes()
	case MsgAwareness:
		m.Payload = r.bytes()
	case MsgDenied:
		m.Reason = r.string()
	case MsgCompact:
		m.Seq = r.int()
		m.Payload = r.bytes()
	case MsgCompactHint:
		m.Seq = r.int()
	default:
		r.fail()
	}
	if r.err != nil {
		return Message{}, r.err
	}
	return m, nil
}
