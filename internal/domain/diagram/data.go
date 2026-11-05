package diagram

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// File kinds: the dominant content of a document, an icon hint.
const (
	KindSDD   = "sdd"
	KindERD   = "erd"
	KindUML   = "uml"
	KindBoard = "board"
)

var fileKinds = []string{KindSDD, KindERD, KindUML, KindBoard}

// ValidKind reports whether k is a file kind.
func ValidKind(k string) bool {
	for _, v := range fileKinds {
		if v == k {
			return true
		}
	}
	return false
}

var (
	boardTypes = map[string]bool{"sticky": true, "draw": true, "text": true, "shape": true, "frame": true, "mindmap": true, "stamp": true}
	umlTypes   = map[string]bool{"class": true, "sequence": true, "state": true, "usecase": true, "component": true, "activity": true, "deployment": true, "object": true, "timing": true}
)

// Prepared is a validated, normalized document and what is derived from it.
type Prepared struct {
	Data      json.RawMessage
	PageCount int
	Kind      string
}

// EmptyData is a new document: one empty page.
func EmptyData(name string, now time.Time) json.RawMessage {
	var r [4]byte
	_, _ = rand.Read(r[:])
	page := "pg_" + hex.EncodeToString(r[:])
	b, _ := json.Marshal(map[string]any{
		"version": 2, "name": name, "createdAt": now.UnixMilli(), "updatedAt": now.UnixMilli(),
		"activePageId": page,
		"pages":        []any{map[string]any{"id": page, "name": "Page 1", "nodes": []any{}, "edges": []any{}}},
	})
	return b
}

// Prepare validates a document the way the Node server does (structure,
// ids, edges, group parents; node types are not checked) and normalizes
// it: the server owns identity and times, and transient UI state is
// stripped. Unknown fields are kept for forward compatibility.
func Prepare(raw json.RawMessage, id, name string, created, updated time.Time) (Prepared, error) {
	if bytes.Contains(raw, []byte(`\u0000`)) {
		return Prepared{}, Validation("document contains a NUL character")
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return Prepared{}, Validation("data: expected an object")
	}
	if string(bytes.TrimSpace(doc["version"])) != "2" {
		return Prepared{}, Validation("version: must be 2")
	}
	var active string
	if json.Unmarshal(doc["activePageId"], &active) != nil || active == "" {
		return Prepared{}, Validation("activePageId: required")
	}
	var pages []map[string]json.RawMessage
	if err := json.Unmarshal(doc["pages"], &pages); err != nil || len(pages) == 0 {
		return Prepared{}, Validation("pages: at least one page is required")
	}
	pageIDs := map[string]bool{}
	kinds := map[string]int{}
	for i, pg := range pages {
		pid, err := checkPage(pg, i, kinds)
		if err != nil {
			return Prepared{}, err
		}
		if pageIDs[pid] {
			return Prepared{}, Validation(fmt.Sprintf("pages.%d.id: Duplicate page id %q", i, pid))
		}
		pageIDs[pid] = true
	}
	if !pageIDs[active] {
		return Prepared{}, Validation("activePageId: does not reference a page")
	}
	var err error
	if doc["pages"], err = json.Marshal(pages); err != nil {
		return Prepared{}, err
	}
	doc["id"], _ = json.Marshal(id)
	doc["name"], _ = json.Marshal(name)
	doc["createdAt"] = json.RawMessage(strconv.FormatInt(created.UnixMilli(), 10))
	doc["updatedAt"] = json.RawMessage(strconv.FormatInt(updated.UnixMilli(), 10))
	out, err := json.Marshal(doc)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{Data: out, PageCount: len(pages), Kind: dominant(kinds)}, nil
}

func str(m map[string]json.RawMessage, key string) (string, bool) {
	var s string
	err := json.Unmarshal(m[key], &s)
	return s, err == nil
}

// checkPage validates one page, strips transient state in place and counts
// the node kinds into kinds. It returns the page id.
func checkPage(pg map[string]json.RawMessage, i int, kinds map[string]int) (string, error) {
	at := func(f string, a ...any) error {
		return Validation(fmt.Sprintf("pages.%d."+f, append([]any{i}, a...)...))
	}
	pid, ok := str(pg, "id")
	if !ok || pid == "" {
		return "", at("id: required")
	}
	if _, ok := str(pg, "name"); !ok {
		return "", at("name: required")
	}
	var nodes, edges []map[string]json.RawMessage
	if json.Unmarshal(pg["nodes"], &nodes) != nil || nodes == nil {
		return "", at("nodes: expected an array")
	}
	if json.Unmarshal(pg["edges"], &edges) != nil || edges == nil {
		return "", at("edges: expected an array")
	}
	types := map[string]string{} // node id -> type
	for j, n := range nodes {
		nid, ok := str(n, "id")
		typ, tok := str(n, "type")
		switch {
		case !ok || nid == "":
			return "", at("nodes.%d.id: required", j)
		case !tok:
			return "", at("nodes.%d.type: required", j)
		}
		if _, dup := types[nid]; dup {
			return "", at("nodes.%d.id: Duplicate node id %q", j, nid)
		}
		types[nid] = typ
		kinds[kindOfNode(typ, n)]++
		if typ == "object" {
			if err := checkObject(n); err != "" {
				return "", at("nodes.%d.data: %s", j, err)
			}
		}
		if typ == "timing" {
			if err := checkTiming(n); err != "" {
				return "", at("nodes.%d.data: %s", j, err)
			}
		}
		if typ == "class" || typ == "object" || typ == "sequence" {
			if err := checkModelRefs(typ, n); err != "" {
				return "", at("nodes.%d.data: %s", j, err)
			}
		}
	}
	for j, n := range nodes {
		if _, has := n["parentId"]; has {
			parent, _ := str(n, "parentId")
			// groups and clusters contain anything; component and deployment nodes hold the nested nodes of those diagrams
			if t := types[parent]; t != "group" && t != "cluster" && t != "component" && t != "deployment" {
				return "", at("nodes.%d.parentId: must reference a group, cluster, component or deployment node", j)
			}
		}
		for _, k := range []string{"selected", "dragging", "measured", "resizing"} {
			delete(n, k)
		}
	}
	eids := map[string]bool{}
	for j, e := range edges {
		eid, ok := str(e, "id")
		src, sok := str(e, "source")
		dst, dok := str(e, "target")
		switch {
		case !ok || eid == "":
			return "", at("edges.%d.id: required", j)
		case eids[eid]:
			return "", at("edges.%d.id: Duplicate edge id %q", j, eid)
		case !sok || src == "":
			return "", at("edges.%d.source: required", j)
		case !dok || dst == "":
			return "", at("edges.%d.target: required", j)
		}
		eids[eid] = true
		if _, ok := types[src]; !ok {
			return "", at("edges.%d.source: references unknown node", j)
		}
		if _, ok := types[dst]; !ok {
			return "", at("edges.%d.target: references unknown node", j)
		}
		delete(e, "selected")
		if cn, _ := str(e, "className"); cn == "edge-new" {
			delete(e, "className")
		}
	}
	pg["nodes"], _ = json.Marshal(nodes)
	pg["edges"], _ = json.Marshal(edges)
	return pid, nil
}

// umlGroupVariants are the `group` variants that only UML diagrams use (package, state composite, use case boundary, activity lane):
// a package diagram is nothing but such groups, so they must count as UML, not as architecture groups.
var umlGroupVariants = map[string]bool{"package": true, "composite": true, "boundary": true, "lane": true}

// kindOfNode is kindOf for a node, which also looks at the variant of a group.
func kindOfNode(t string, n map[string]json.RawMessage) string {
	if t == "group" {
		var d struct {
			Variant string `json:"variant"`
		}
		if json.Unmarshal(n["data"], &d) == nil && umlGroupVariants[d.Variant] {
			return KindUML
		}
	}
	return kindOf(t)
}

func kindOf(t string) string {
	switch {
	case t == "table" || strings.HasPrefix(t, "erd"):
		return KindERD
	case umlTypes[t] || strings.HasPrefix(t, "uml"):
		return KindUML
	case boardTypes[t]:
		return KindBoard
	}
	return KindSDD
}

// dominant is the kind with the most nodes; ties and empty files are sdd.
func dominant(n map[string]int) string {
	best := KindSDD
	for _, k := range fileKinds {
		if n[k] > n[best] {
			best = k
		}
	}
	return best
}

// checkModelRefs validates the shared class model links (additive, schema unchanged): a class or object carries an optional
// data.modelId string, a sequence participant an optional modelId string. Same rule as the client.
func checkModelRefs(typ string, n map[string]json.RawMessage) string {
	var data map[string]json.RawMessage
	if json.Unmarshal(n["data"], &data) != nil {
		return "" // other checks report a non-object data
	}
	isStr := func(raw json.RawMessage) bool {
		var v string
		return json.Unmarshal(raw, &v) == nil
	}
	if raw, ok := data["modelId"]; ok && typ != "sequence" && !isStr(raw) {
		return "modelId must be a string"
	}
	if typ == "sequence" {
		var parts []map[string]json.RawMessage
		if json.Unmarshal(data["participants"], &parts) != nil {
			return ""
		}
		for k, p := range parts {
			if raw, ok := p["modelId"]; ok && !isStr(raw) {
				return fmt.Sprintf("participants[%d].modelId must be a string", k)
			}
		}
	}
	return ""
}

// checkObject validates the data of an object diagram node: slots is an array of {id, name, value} with unique ids (same rule as the client).
func checkObject(n map[string]json.RawMessage) string {
	var data map[string]json.RawMessage
	if json.Unmarshal(n["data"], &data) != nil {
		return "expected an object"
	}
	if raw, ok := data["className"]; ok {
		var c string
		if json.Unmarshal(raw, &c) != nil {
			return "className must be a string"
		}
	}
	var slots []map[string]json.RawMessage
	if json.Unmarshal(data["slots"], &slots) != nil || slots == nil {
		return "slots must be an array"
	}
	seen := map[string]bool{}
	for k, sl := range slots {
		id, ok := str(sl, "id")
		_, nok := str(sl, "name")
		_, vok := str(sl, "value")
		if !ok || id == "" || !nok || !vok {
			return fmt.Sprintf("slots[%d] needs id, name and value (strings)", k)
		}
		if seen[id] {
			return fmt.Sprintf("duplicate slot id %q", id)
		}
		seen[id] = true
	}
	return ""
}

// checkTiming validates the data of a timing diagram node (same rule as src/lib/timing/validate.ts): arrays of lifelines, changes,
// messages and durations with unique ids that refer to known lifelines, a positive end and a string unit.
func checkTiming(n map[string]json.RawMessage) string {
	var data map[string]json.RawMessage
	if json.Unmarshal(n["data"], &data) != nil {
		return "expected an object"
	}
	arr := func(k string) ([]map[string]json.RawMessage, bool) {
		var a []map[string]json.RawMessage
		return a, json.Unmarshal(data[k], &a) == nil && a != nil
	}
	var end float64
	if json.Unmarshal(data["end"], &end) != nil || end <= 0 {
		return "end must be a positive number"
	}
	var unit string
	if json.Unmarshal(data["unit"], &unit) != nil {
		return "unit must be a string"
	}
	life := map[string]bool{}
	lifes, ok := arr("lifelines")
	if !ok {
		return "lifelines must be an array"
	}
	for i, l := range lifes {
		id, iok := str(l, "id")
		_, nok := str(l, "name")
		kind, _ := str(l, "kind")
		if !iok || id == "" || !nok {
			return fmt.Sprintf("lifelines[%d] needs id and name", i)
		}
		if kind != "robust" && kind != "concise" && kind != "binary" && kind != "clock" {
			return fmt.Sprintf("lifelines[%d].kind must be robust|concise|binary|clock", i)
		}
		var states []string
		if json.Unmarshal(l["states"], &states) != nil {
			return fmt.Sprintf("lifelines[%d].states must be an array of strings", i)
		}
		if life[id] {
			return fmt.Sprintf("duplicate lifeline id %q", id)
		}
		life[id] = true
	}
	ids := map[string]bool{}
	for _, spec := range []struct{ key, refs string }{{"changes", "life"}, {"messages", "from"}, {"durations", "life"}} {
		list, ok := arr(spec.key)
		if !ok {
			return spec.key + " must be an array"
		}
		for i, it := range list {
			id, iok := str(it, "id")
			if !iok || id == "" {
				return fmt.Sprintf("%s[%d] needs an id", spec.key, i)
			}
			if ids[id] {
				return fmt.Sprintf("duplicate id %q", id)
			}
			ids[id] = true
			ref, _ := str(it, spec.refs)
			if !life[ref] {
				return fmt.Sprintf("%s[%d] refers to an unknown lifeline", spec.key, i)
			}
			if spec.key == "messages" {
				if to, _ := str(it, "to"); !life[to] {
					return fmt.Sprintf("%s[%d] refers to an unknown lifeline", spec.key, i)
				}
			}
		}
	}
	return ""
}
