package yjs

import (
	"encoding/json"
	"strings"
	"unicode"
)

// MaxAwarenessState is the largest awareness state kept (bytes of JSON).
const MaxAwarenessState = 4096

// AwarenessEntry is one client's awareness state; State is the raw JSON
// ("null" once the client is gone).
type AwarenessEntry struct {
	Client uint64
	Clock  uint64
	State  string
}

// ParseAwareness reads an awareness update.
func ParseAwareness(update []byte) ([]AwarenessEntry, error) {
	r := &reader{b: update}
	n := r.count()
	out := make([]AwarenessEntry, 0, n)
	for ; n > 0 && r.err == nil; n-- {
		out = append(out, AwarenessEntry{Client: r.uint(), Clock: r.uint(), State: r.string()})
	}
	if r.err == nil && r.left() != 0 {
		r.fail()
	}
	if r.err != nil {
		return nil, r.err
	}
	return out, nil
}

// EncodeAwareness writes an awareness update.
func EncodeAwareness(es []AwarenessEntry) []byte {
	out := appendUint(nil, uint64(len(es)))
	for _, e := range es {
		out = appendString(appendUint(appendUint(out, e.Client), e.Clock), e.State)
	}
	return out
}

// Identity is who the server authenticated: awareness states are shown to
// other people, so a client may set any state but not another person's name.
type Identity struct {
	ID   string // "" for a guest
	Name string
}

// cleanName drops control characters and caps the length.
func cleanName(n string) string {
	n = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, n)
	if r := []rune(n); len(r) > 100 {
		n = string(r[:100])
	}
	if n == "" {
		return "Guest"
	}
	return n
}

// SanitizeAwareness rewrites an awareness update so its sender is who the
// server says: user.name and user.id are overwritten, a state that is not a
// JSON object or is too large becomes null (a removal). mayClaim decides
// whether this connection may speak for a client id; other ids are dropped.
// It returns the rewritten update and the ids kept.
func SanitizeAwareness(update []byte, ident Identity, mayClaim func(client uint64) bool) ([]byte, []uint64, error) {
	es, err := ParseAwareness(update)
	if err != nil {
		return nil, nil, err
	}
	var kept []AwarenessEntry
	var ids []uint64
	for _, e := range es {
		if mayClaim != nil && !mayClaim(e.Client) {
			continue
		}
		ids = append(ids, e.Client)
		e.State = sanitizeState(e.State, ident)
		kept = append(kept, e)
	}
	return EncodeAwareness(kept), ids, nil
}

func sanitizeState(raw string, ident Identity) string {
	if raw == "null" || len(raw) > MaxAwarenessState {
		return "null"
	}
	var state map[string]any
	if json.Unmarshal([]byte(raw), &state) != nil || state == nil {
		return "null"
	}
	user := map[string]any{"name": cleanName(ident.Name)}
	if old, ok := state["user"].(map[string]any); ok {
		if c, ok := old["color"].(string); ok {
			user["color"] = truncate(c, 20)
		}
	}
	if ident.ID != "" {
		user["id"] = ident.ID
	}
	state["user"] = user
	b, err := json.Marshal(state)
	if err != nil {
		return "null"
	}
	return string(b)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
