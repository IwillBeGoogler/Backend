package diagram

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxNameLen is the longest name of a project, folder or file.
const MaxNameLen = 200

var (
	controlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	clientIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)
	ifMatchRe    = regexp.MustCompile(`^(?:W/)?"?(\d+)"?$`)
)

// CheckClientID accepts an id chosen by a client (offline creation): it
// must look like our own ids and cannot contain path characters. "" means
// the server picks one.
func CheckClientID(id string) error {
	if id != "" && !clientIDRe.MatchString(id) {
		return invalid("INVALID_ID", "id must be 6-64 characters of A-Z a-z 0-9 _ -")
	}
	return nil
}

// CheckNameSize rejects a raw name over MaxNameLen, or an empty one when
// required.
func CheckNameSize(raw string, required bool) error {
	switch {
	case required && raw == "":
		return Validation("name is required")
	case utf8.RuneCountInString(raw) > MaxNameLen:
		return Validation("name must be at most 200 characters")
	}
	return nil
}

// CleanName trims a name, refuses control characters and falls back when
// nothing is left.
func CleanName(raw, fallback string) (string, error) {
	n := strings.TrimSpace(raw)
	if controlChars.MatchString(n) {
		return "", invalid("INVALID_NAME", "Names cannot contain control characters")
	}
	if n == "" {
		n = fallback
	}
	if r := []rune(n); len(r) > MaxNameLen {
		n = string(r[:MaxNameLen])
	}
	return n, nil
}

// ParseIfMatch reads an If-Match header (`"3"`, `W/"3"` or `3`).
func ParseIfMatch(h string) (int, bool) {
	m := ifMatchRe.FindStringSubmatch(strings.TrimSpace(h))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// SubtreeIDs returns rootID and the ids of every folder below it.
func SubtreeIDs(folders []Folder, rootID string) map[string]bool {
	kids := map[string][]string{}
	for _, f := range folders {
		if f.ParentID != nil {
			kids[*f.ParentID] = append(kids[*f.ParentID], f.ID)
		}
	}
	out := map[string]bool{}
	stack := []string{rootID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[id] { // a corrupt cycle must not loop forever
			continue
		}
		out[id] = true
		stack = append(stack, kids[id]...)
	}
	return out
}

const dayMS = int64(24 * time.Hour / time.Millisecond)

// KeepVersions says which snapshots survive pruning: the newest keepLast,
// plus the newest snapshot of every UTC day inside the last keepDays days.
// The latest and every named version are always kept.
func KeepVersions(vs []Version, now time.Time, keepLast, keepDays int) map[int]bool {
	sorted := append([]Version(nil), vs...)
	for i := 1; i < len(sorted); i++ { // newest first; histories are short and nearly sorted
		for j := i; j > 0 && sorted[j].Version > sorted[j-1].Version; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	keep := map[int]bool{}
	for _, v := range vs {
		if v.Named {
			keep[v.Version] = true
		}
	}
	for i := 0; i < max(1, keepLast) && i < len(sorted); i++ {
		keep[sorted[i].Version] = true
	}
	if keepDays > 0 {
		cutoff := now.UnixMilli() - int64(keepDays)*dayMS
		seen := map[int64]bool{}
		for _, v := range sorted {
			at := v.CreatedAt.UnixMilli()
			day := at / dayMS
			if at < cutoff || seen[day] {
				continue
			}
			seen[day] = true
			keep[v.Version] = true
		}
	}
	return keep
}

// MaxPrefsBytes caps a user's preferences object.
const MaxPrefsBytes = 64 * 1024

// MergePrefs applies a patch to the current preferences: top-level keys
// are replaced, a null value deletes the key. A current value that is not
// an object counts as empty.
func MergePrefs(cur, patch json.RawMessage) (json.RawMessage, error) {
	var next map[string]json.RawMessage
	if json.Unmarshal(cur, &next) != nil || next == nil {
		next = map[string]json.RawMessage{}
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(patch, &p); err != nil || p == nil {
		return nil, Validation("body must be a JSON object")
	}
	for k, v := range p {
		if n := utf8.RuneCountInString(k); n < 1 || n > 100 {
			return nil, Validation("keys must be 1-100 characters")
		}
		switch {
		case k == "__proto__":
		case string(v) == "null":
			delete(next, k)
		default:
			next[k] = v
		}
	}
	out, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxPrefsBytes {
		return nil, TooLarge("Preferences are too large")
	}
	return out, nil
}
