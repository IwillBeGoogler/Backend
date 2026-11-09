package diagram_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

type m = map[string]any

func validLib() m {
	return m{
		"components": m{"c1": m{
			"id": "c1", "name": "C", "rev": 1.0, "width": 10.0, "height": 10.0, "mainFileId": "f", "mainNodeId": "n",
			"nodes": []any{m{"key": "k", "type": "box", "x": 1.0, "y": 2.0, "width": 3.0, "height": 4.0, "parentKey": "p", "data": m{}}},
			"edges": []any{m{"key": "e", "source": "a", "target": "b", "data": m{}, "label": "l"}},
		}},
		"styles": m{
			"colors":  m{"c": m{"id": "c", "name": "n", "value": "#fff", "dark": "#000"}},
			"texts":   m{"t": m{"id": "t", "name": "n", "fontSize": 12.0, "bold": true, "align": "left", "color": "#000"}},
			"effects": m{"e": m{"id": "e", "name": "n", "shadow": m{"x": 1.0, "y": 1.0, "blur": 2.0, "color": "#000"}}, "e2": m{"id": "e2", "name": "n", "shadow": nil}},
		},
		"tokens": m{"k": m{"id": "k", "name": "n", "light": "a", "dark": "b"}},
	}
}

// edit returns a deep copy of a valid library after mutate.
func lib(mutate func(d m)) json.RawMessage {
	b, _ := json.Marshal(validLib())
	var d m
	_ = json.Unmarshal(b, &d)
	if mutate != nil {
		mutate(d)
	}
	out, _ := json.Marshal(d)
	return out
}

func comp(d m) m   { return d["components"].(m)["c1"].(m) }
func styles(d m) m { return d["styles"].(m) }

func TestCheckLibraryRules(t *testing.T) {
	if err := diagram.CheckLibrary(lib(nil)); err != nil {
		t.Fatal(err)
	}
	if err := diagram.CheckLibrary(diagram.EmptyLibrary); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(d m){
		"component not object": func(d m) { d["components"].(m)["c1"] = 5 },
		"comp no id":           func(d m) { delete(comp(d), "id") },
		"comp id type":         func(d m) { comp(d)["id"] = 5 },
		"comp id long":         func(d m) { comp(d)["id"] = strings.Repeat("a", 65) },
		"comp no rev":          func(d m) { delete(comp(d), "rev") },
		"comp rev str":         func(d m) { comp(d)["rev"] = "x" },
		"comp no nodes":        func(d m) { delete(comp(d), "nodes") },
		"nodes not array":      func(d m) { comp(d)["nodes"] = 5 },
		"nodes too many":       func(d m) { comp(d)["nodes"] = make([]any, 2001) },
		"node not object":      func(d m) { comp(d)["nodes"] = []any{5} },
		"node no data":         func(d m) { delete(comp(d)["nodes"].([]any)[0].(m), "data") },
		"node data not obj":    func(d m) { comp(d)["nodes"].([]any)[0].(m)["data"] = 5 },
		"edges not array":      func(d m) { comp(d)["edges"] = 5 },
		"edge not object":      func(d m) { comp(d)["edges"] = []any{5} },
		"edge no key":          func(d m) { delete(comp(d)["edges"].([]any)[0].(m), "key") },
		"edge label long":      func(d m) { comp(d)["edges"].([]any)[0].(m)["label"] = strings.Repeat("a", 2001) },
		"no components":        func(d m) { delete(d, "components") },
		"no styles":            func(d m) { delete(d, "styles") },
		"no colors":            func(d m) { delete(styles(d), "colors") },
		"color not object":     func(d m) { styles(d)["colors"].(m)["c"] = 5 },
		"color no value":       func(d m) { delete(styles(d)["colors"].(m)["c"].(m), "value") },
		"text bold":            func(d m) { styles(d)["texts"].(m)["t"].(m)["bold"] = "yes" },
		"text size":            func(d m) { styles(d)["texts"].(m)["t"].(m)["fontSize"] = "big" },
		"effect no shadow":     func(d m) { delete(styles(d)["effects"].(m)["e"].(m), "shadow") },
		"effect shadow type":   func(d m) { styles(d)["effects"].(m)["e"].(m)["shadow"] = 5 },
		"effect shadow no x":   func(d m) { delete(styles(d)["effects"].(m)["e"].(m)["shadow"].(m), "x") },
		"no tokens":            func(d m) { delete(d, "tokens") },
		"token not object":     func(d m) { d["tokens"].(m)["k"] = 5 },
		"token no light":       func(d m) { delete(d["tokens"].(m)["k"].(m), "light") },
	}
	for name, mut := range bad {
		if err := diagram.CheckLibrary(lib(mut)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, raw := range []string{`nope`, `[]`, `5`} {
		if err := diagram.CheckLibrary(json.RawMessage(raw)); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
}

func TestPreparePrivateRules(t *testing.T) {
	good := `{"nodes":[{"id":"a","type":"sticky","position":{"x":1,"y":2},"width":10,"height":20,"data":{"label":"hi","private":false,"authorId":"evil"}}],"edges":[{"id":"e","source":"a","target":"a"}]}`
	out, err := diagram.PreparePrivate(json.RawMessage(good), "u1")
	if err != nil || !strings.Contains(string(out), `"authorId":"u1"`) || !strings.Contains(string(out), `"private":true`) {
		t.Fatalf("%s %v", out, err)
	}
	node := func(extra string) string {
		return `{"nodes":[{"id":"a","type":"sticky","position":{"x":1,"y":2},"data":{"label":"x"}` + extra + `}],"edges":[]}`
	}
	bad := map[string]string{
		"not json":       `nope`,
		"nodes missing":  `{"edges":[]}`,
		"edges missing":  `{"nodes":[]}`,
		"too many nodes": `{"nodes":[` + strings.TrimSuffix(strings.Repeat("{},", 501), ",") + `],"edges":[]}`,
		"node not obj":   `{"nodes":[5],"edges":[]}`,
		"node no id":     `{"nodes":[{"type":"sticky"}],"edges":[]}`,
		"node type":      `{"nodes":[{"id":"a","type":"box","position":{"x":1,"y":1},"data":{"label":""}}],"edges":[]}`,
		"no position":    `{"nodes":[{"id":"a","type":"sticky","data":{"label":""}}],"edges":[]}`,
		"pos no x":       `{"nodes":[{"id":"a","type":"sticky","position":{"y":1},"data":{"label":""}}],"edges":[]}`,
		"width zero":     node(`,"width":0`),
		"width str":      node(`,"width":"a"`),
		"height huge":    node(`,"height":10001`),
		"no data":        `{"nodes":[{"id":"a","type":"sticky","position":{"x":1,"y":1}}],"edges":[]}`,
		"label missing":  `{"nodes":[{"id":"a","type":"sticky","position":{"x":1,"y":1},"data":{}}],"edges":[]}`,
		"edge not obj":   `{"nodes":[],"edges":[5]}`,
		"edge no src":    `{"nodes":[],"edges":[{"id":"e","target":"a"}]}`,
		"too big":        `{"nodes":[{"id":"a","type":"sticky","position":{"x":1,"y":1},"data":{"label":"` + strings.Repeat("x", 10_000) + `"}}` + strings.Repeat(`,{"id":"a","type":"sticky","position":{"x":1,"y":1},"data":{"label":"`+strings.Repeat("x", 10_000)+`"}}`, 110) + `],"edges":[]}`,
	}
	for name, raw := range bad {
		if _, err := diagram.PreparePrivate(json.RawMessage(raw), "u1"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSharingErrorConstructors(t *testing.T) {
	cases := []struct {
		err  error
		kind error
		code string
	}{
		{diagram.ShareExpired(), diagram.ErrGone, "SHARE_EXPIRED"},
		{diagram.Forbidden("no"), domain.ErrForbidden, "FORBIDDEN"},
		{diagram.ReadOnlyShare(), domain.ErrForbidden, "FORBIDDEN"},
		{diagram.UnsupportedMedia("x"), diagram.ErrUnsupportedMedia, "UNSUPPORTED_MEDIA_TYPE"},
		{diagram.UserNotFound(), domain.ErrNotFound, "USER_NOT_FOUND"},
		{diagram.OwnerImmutable("x"), nil, "OWNER_IMMUTABLE"},
		{diagram.FileNotInProject(), nil, "FILE_NOT_FOUND"},
		{diagram.MissingFile(), nil, "MISSING_FILE"},
		{diagram.AnchorRequired(), nil, "ANCHOR_REQUIRED"},
		{diagram.ReplyToReply(), nil, "REPLY_TO_REPLY"},
		{diagram.ResolveRootOnly(), nil, "RESOLVE_ROOT_ONLY"},
		{diagram.NothingToChange(), nil, "BAD_REQUEST"},
	}
	for _, c := range cases {
		var e *diagram.Error
		if !errors.As(c.err, &e) || e.Code != c.code || e.Error() == "" {
			t.Errorf("%s: %v", c.code, c.err)
			continue
		}
		if c.kind != nil && !errors.Is(c.err, c.kind) {
			t.Errorf("%s: not %v", c.code, c.kind)
		}
	}
}

func TestLibraryConflict(t *testing.T) {
	var err error = &diagram.LibraryConflict{}
	if !errors.Is(err, diagram.ErrLibraryConflict) || err.Error() == "" || err.(*diagram.LibraryConflict).Code() != "LIBRARY_CONFLICT" {
		t.Fatal("LibraryConflict")
	}
	if errors.Is(err, errors.New("other")) {
		t.Fatal("matches unrelated error")
	}
}

func TestSchemaErrorsAndCounts(t *testing.T) {
	if (&diagram.TargetBlockedError{Reason: "r"}).Error() != "r" || (&diagram.IntrospectFailedError{Msg: "m"}).Error() != "m" {
		t.Fatal("error text")
	}
	s := diagram.Schema{
		Tables: []diagram.SchemaTable{{Kind: "view", Columns: make([]diagram.SchemaColumn, 2)}, {Columns: make([]diagram.SchemaColumn, 3)}, {}},
		Enums:  []diagram.SchemaEnum{{}},
	}
	if c := s.Counts(); c.Views != 1 || c.Tables != 2 || c.Columns != 5 || c.Enums != 1 {
		t.Fatalf("%+v", c)
	}
}
