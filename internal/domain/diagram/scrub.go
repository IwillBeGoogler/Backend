package diagram

import (
	"bytes"
	"encoding/json"
)

// Private stickies never live in the shared document. Whatever a save
// carries is taken out of it first (ScrubPrivate) and filed under the user
// who wrote each sticky (MergePrivate into that user's own store).

// PrivateSet is private stickies (and the edges attached to them) taken out
// of a document, as the raw JSON of each node and edge.
type PrivateSet struct {
	Nodes []json.RawMessage
	Edges []json.RawMessage
}

func (s PrivateSet) empty() bool { return len(s.Nodes) == 0 && len(s.Edges) == 0 }

// object reads a JSON object; ok is false for anything else.
func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

func text(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

// privateData returns a sticky's data object when the node is a sticky
// marked "private": true (exactly the boolean true, as in the Node server).
func privateData(n map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	if text(n, "type") != "sticky" {
		return nil, false
	}
	data, ok := object(n["data"])
	if !ok || !bytes.Equal(bytes.TrimSpace(data["private"]), []byte("true")) {
		return nil, false
	}
	return data, true
}

// ScrubPrivate takes every private sticky out of a document (every page)
// together with the edges attached to it, and groups them by the user who
// wrote them: data.authorId when isUser accepts it, else fallback (the
// author stamped on the sticky then). It returns the document that may be
// stored and shown to others, the stickies by owner and how many stickies
// were removed. Without private stickies (or when raw is not a document,
// which validation reports) it returns raw itself.
func ScrubPrivate(raw json.RawMessage, isUser func(id string) bool, fallback string) (json.RawMessage, map[string]PrivateSet, int) {
	doc, ok := object(raw)
	if !ok {
		return raw, nil, 0
	}
	var pages []map[string]json.RawMessage
	if json.Unmarshal(doc["pages"], &pages) != nil {
		return raw, nil, 0
	}
	byUser := map[string]PrivateSet{}
	removed := 0
	for _, pg := range pages {
		var nodes, edges []json.RawMessage
		if json.Unmarshal(pg["nodes"], &nodes) != nil {
			continue
		}
		_ = json.Unmarshal(pg["edges"], &edges)
		owners := map[string]string{} // private node id -> owner
		shared := make([]json.RawMessage, 0, len(nodes))
		for _, rn := range nodes {
			n, isObj := object(rn)
			data, priv := privateData(n)
			if !isObj || !priv {
				shared = append(shared, rn)
				continue
			}
			author := text(data, "authorId")
			owner := fallback
			if author != "" && isUser(author) {
				owner = author
			}
			if owner != author { // stamp the owner as its author
				data["authorId"], _ = json.Marshal(owner)
				n["data"], _ = json.Marshal(data)
				rn, _ = json.Marshal(n)
			}
			owners[text(n, "id")] = owner
			set := byUser[owner]
			set.Nodes = append(set.Nodes, rn)
			byUser[owner] = set
			removed++
		}
		if len(owners) == 0 {
			continue
		}
		keep := make([]json.RawMessage, 0, len(edges))
		for _, re := range edges {
			e, isObj := object(re)
			owner, hit := owners[text(e, "source")]
			if !hit {
				owner, hit = owners[text(e, "target")]
			}
			if !isObj || !hit {
				keep = append(keep, re)
				continue
			}
			set := byUser[owner]
			set.Edges = append(set.Edges, re)
			byUser[owner] = set
		}
		pg["nodes"], _ = json.Marshal(shared)
		pg["edges"], _ = json.Marshal(keep)
	}
	if removed == 0 {
		return raw, nil, 0
	}
	var err error
	if doc["pages"], err = json.Marshal(pages); err != nil {
		return raw, nil, 0
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return raw, nil, 0
	}
	return out, byUser, removed
}

// MergePrivate adds stickies to what a person already has (stored
// {"nodes", "edges"}, or empty): an incoming node or edge replaces one with
// the same id in place. It returns the document to store.
func MergePrivate(cur json.RawMessage, add PrivateSet) json.RawMessage {
	var have struct {
		Nodes []json.RawMessage `json:"nodes"`
		Edges []json.RawMessage `json:"edges"`
	}
	_ = json.Unmarshal(cur, &have)
	merge := func(list, extra []json.RawMessage) []json.RawMessage {
		at := map[string]int{}
		out := make([]json.RawMessage, 0, len(list)+len(extra))
		for _, r := range list {
			if o, ok := object(r); ok {
				at[text(o, "id")] = len(out)
			}
			out = append(out, r)
		}
		for _, r := range extra {
			o, _ := object(r)
			if i, ok := at[text(o, "id")]; ok {
				out[i] = r
				continue
			}
			at[text(o, "id")] = len(out)
			out = append(out, r)
		}
		return out
	}
	b, _ := json.Marshal(map[string]any{"nodes": merge(have.Nodes, add.Nodes), "edges": merge(have.Edges, add.Edges)})
	return b
}
