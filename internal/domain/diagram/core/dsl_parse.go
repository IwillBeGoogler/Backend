package core

import (
	"regexp"
	"strconv"
	"strings"
)

// The ".sdd" text format (diagrams as code): core/dsl/{types,lex,parse}.ts.

// DSLVersion is the format version written in the "#!sdd" header.
const DSLVersion = 1

// Diagnostic is a parse or build message.
type Diagnostic struct {
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // error or warning
}

type DslNode struct {
	ID, Label, Kind string // Kind: node or group
	Icon, Sub       string
	Parent          string
	Attrs           map[string]string
	Line            int
}

type DslEdge struct {
	From, To, Label, Arrow string
	Attrs                  map[string]string
	Line                   int
}

type DslRefEnd struct{ Table, Column string }

type DslRefTo struct {
	Op string
	To DslRefEnd
}

type DslColumn struct {
	Name, Type                     string
	PK, Unique, NotNull, Increment bool
	Default, Note                  *string
	Ref                            *DslRefTo
	Line                           int
}

type DslIndex struct {
	Columns []string
	Unique  bool
	Name    string
}

type DslTable struct {
	Name, Kind string // table, view, enum
	Columns    []*DslColumn
	Indexes    []DslIndex
	Note       *string
	Line       int
}

type DslRef struct {
	From  DslRefEnd
	Op    string
	To    DslRefEnd
	Attrs map[string]string
	Line  int
}

type DslSeqParticipant struct{ ID, Name, Kind, ModelID string }

type DslSeqSection struct {
	Guard string
	Items []*DslSeqItem
}

// DslSeqItem is a message, a note or a fragment (Type).
type DslSeqItem struct {
	Type     string
	From, To string
	Text     string
	Kind     string
	Over     []string
	Side     string
	Op       string
	Sections []*DslSeqSection
}

type DslSeq struct {
	ID, Title    string
	Participants []*DslSeqParticipant
	Items        []*DslSeqItem
	Autonumber   bool
	Line         int
}

// DslDoc is the AST of a text.
type DslDoc struct {
	Header  bool
	Version int
	Title   *string
	Nodes   []*DslNode
	Edges   []*DslEdge
	Tables  []*DslTable
	Refs    []*DslRef
	Seqs    []*DslSeq

	Packages    []*DslPackage
	Classes     []*DslClass
	ClassRels   []*DslClassRel
	States      []*DslState
	Transitions []*DslTransition
	UcItems     []*DslUcItem
	UcRels      []*DslUcRel
	Comps       []*DslComp
	CompLinks   []*DslCompLink

	ActNodes []*DslActNode
	ActLanes []*DslActLane
	ActFlows []*DslActFlow
	DepNodes []*DslDepNode
	DepLinks []*DslDepLink
	PkgRels  []*DslPkgRel
	Objects  []*DslObject
	ObjLinks []*DslObjLink
	CommMsgs []*DslCommMsg
	Timings  []*DslTiming
}

type tok struct {
	k, v string // k: str, word, sym
	col  int
}

const symChars = "{}[](),=:"

func isSym(r rune) bool { return strings.ContainsRune(symChars, r) }

func stripComment(line string) string {
	r := []rune(line)
	q := false
	for i := 0; i < len(r); i++ {
		c := r[i]
		if c == '\\' && q {
			i++
		} else if c == '"' {
			q = !q
		} else if !q && c == '/' && i+1 < len(r) && r[i+1] == '/' {
			return string(r[:i])
		}
	}
	return line
}

type lexErr struct {
	col int
	msg string
}

func tokenize(line string) ([]tok, *lexErr) {
	r := []rune(line)
	var toks []tok
	i := 0
	for i < len(r) {
		c := r[i]
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		if c == '"' {
			j := i + 1
			var sb strings.Builder
			for j < len(r) && r[j] != '"' {
				if r[j] == '\\' && j+1 < len(r) {
					if r[j+1] == 'n' {
						sb.WriteRune('\n')
					} else {
						sb.WriteRune(r[j+1])
					}
					j += 2
				} else {
					sb.WriteRune(r[j])
					j++
				}
			}
			if j >= len(r) {
				return toks, &lexErr{i + 1, "unterminated string"}
			}
			toks = append(toks, tok{"str", sb.String(), i + 1})
			i = j + 1
			continue
		}
		if isSym(c) {
			toks = append(toks, tok{"sym", string(c), i + 1})
			i++
			continue
		}
		j := i
		for j < len(r) && r[j] != ' ' && r[j] != '\t' && r[j] != '"' && !isSym(r[j]) {
			j++
		}
		toks = append(toks, tok{"word", string(r[i:j]), i + 1})
		i = j
	}
	return toks, nil
}

var (
	reQuoteBackslash = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	reWord           = regexp.MustCompile(`^[A-Za-z0-9_.:@#/+-]+$`)
	reAttrVal        = regexp.MustCompile(`^[A-Za-z0-9_.#/+-]+$`)
)

func quote(s string) string { return `"` + reQuoteBackslash.Replace(s) + `"` }

func word(s string) string {
	if reWord.MatchString(s) && !strings.Contains(s, ":") {
		return s
	}
	return quote(s)
}

func attrVal(s string) string {
	if reAttrVal.MatchString(s) {
		return s
	}
	return quote(s)
}

// ---------------------------------------------------------------- parse

var (
	arrows     = map[string]bool{"->": true, "<->": true, "--": true, "<-": true}
	refOps     = map[string]bool{">": true, "<": true, "-": true, "<>": true}
	seqKinds   = map[string]bool{"participant": true, "actor": true, "boundary": true, "control": true, "entity": true, "database": true}
	fragOps    = map[string]bool{"alt": true, "opt": true, "loop": true, "par": true, "break": true, "critical": true}
	sectionOps = map[string]string{"else": "alt", "and": "par", "option": "critical"}
	reSlugSeq  = regexp.MustCompile(`[^a-z0-9]+`)
	reNumLead  = regexp.MustCompile(`^-?\d`)
	reHeader   = regexp.MustCompile(`^#!sdd(?:\s+(\d+))?\s*$`)
)

func slugOr(s, fallback string) string {
	out := strings.Trim(reSlugSeq.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if out == "" {
		return fallback
	}
	return out
}

type fragFrame struct {
	op       string
	sections []*DslSeqSection
}

type ctx struct {
	kind  string // group, table, indexes, seq
	id    string
	table *DslTable
	seq   *DslSeq
	stack []*fragFrame
	uml   *umlCtx
	tm    *DslTiming
}

func parseAttrs(toks []tok, from int, err func(t tok, m string)) map[string]string {
	out := map[string]string{}
	i := from
	for i < len(toks) {
		t := toks[i]
		if t.k == "sym" && t.v == "," {
			i++
			continue
		}
		if t.k != "word" && t.k != "str" {
			err(t, `unexpected "`+t.v+`"`)
			i++
			continue
		}
		if i+1 < len(toks) && toks[i+1].k == "sym" && toks[i+1].v == "=" {
			if i+2 >= len(toks) || toks[i+2].k == "sym" {
				err(t, `missing value for "`+t.v+`"`)
				i += 2
				continue
			}
			val := toks[i+2].v
			for i+4 < len(toks) && toks[i+3].k == "sym" && toks[i+3].v == "," && reNumLead.MatchString(toks[i+4].v) && reNumLead.MatchString(val) {
				val += "," + toks[i+4].v
				i += 2
			}
			out[t.v] = val
			i += 3
		} else {
			out[t.v] = "true"
			i++
		}
	}
	return out
}

func splitTop(s string) []string {
	var out []string
	depth := 0
	var q rune
	var cur strings.Builder
	for _, c := range s {
		if q != 0 {
			cur.WriteRune(c)
			if c == q {
				q = 0
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			q = c
			cur.WriteRune(c)
			continue
		}
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
		}
		if c == ',' && depth == 0 {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		} else {
			cur.WriteRune(c)
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

var reUnqEsc = regexp.MustCompile(`\\(.)`)

func unq(s string) string {
	t := strings.TrimSpace(s)
	if len(t) >= 1 {
		f, l := t[0], t[len(t)-1]
		if (f == '\'' && l == '\'') || (f == '"' && l == '"') || (f == '`' && l == '`') {
			if len(t) == 1 {
				return ""
			}
			return reUnqEsc.ReplaceAllString(t[1:len(t)-1], "$1")
		}
	}
	return t
}

var reEnd = regexp.MustCompile(`^\s*("[^"]+"|[^\s.]+)\s*\.\s*("[^"]+"|[^\s.]+)\s*$`)

func parseEnd(s string) (DslRefEnd, bool) {
	m := reEnd.FindStringSubmatch(s)
	if m == nil {
		return DslRefEnd{}, false
	}
	return DslRefEnd{unq(m[1]), unq(m[2])}, true
}

var (
	reIndexesOpen = regexp.MustCompile(`(?i)^indexes\s*\{$`)
	reNote        = regexp.MustCompile(`(?i)^note\s*:\s*(.+)$`)
	reEnumTail    = regexp.MustCompile(`\s*\[.*\]\s*$`)
	reIndexLine   = regexp.MustCompile(`^(\([^)]*\)|[^\s\[]+)\s*(?:\[(.*)\])?\s*$`)
	reRefPrefix   = regexp.MustCompile(`(?i)^ref\s*:?\s*`)
	reRefLine     = regexp.MustCompile(`^(.+?)\s+(<>|>|<|-)\s+(.+?)(?:\s*\[(.*)\])?\s*$`)
	reRefAttr     = regexp.MustCompile(`^(\w+)\s*:\s*(.+)$`)
	reIdxName     = regexp.MustCompile(`(?i)^name\s*:`)
	reIdxNameStr  = regexp.MustCompile(`(?i)^name\s*:\s*`)
	reColumn      = regexp.MustCompile("^(\"[^\"]+\"|`[^`]+`|[^\\s\\[]+)\\s+([^\\[]*?)\\s*(?:\\[(.*)\\])?\\s*$")
	reDefault     = regexp.MustCompile(`(?i)^default\s*:`)
	reDefaultTail = regexp.MustCompile(`(?i)^default\s*:\s*`)
	reColNote     = regexp.MustCompile(`(?i)^note\s*:`)
	reColNoteTail = regexp.MustCompile(`(?i)^note\s*:\s*`)
	reColRef      = regexp.MustCompile(`(?i)^ref\s*:`)
	reColRefFull  = regexp.MustCompile(`(?i)^ref\s*:\s*(<>|>|<|-)\s*(.+)$`)
)

// ParseResult is a parsed text and its diagnostics.
type ParseResult struct {
	Doc         *DslDoc
	Diagnostics []Diagnostic
}

func strp(s string) *string { return &s }

// ParseDSL parses text; errors never fail, they come back as diagnostics.
func ParseDSL(text string) ParseResult {
	doc := &DslDoc{Version: 1}
	var diags []Diagnostic
	diag := func(line, col int, msg, sev string) { diags = append(diags, Diagnostic{line, col, msg, sev}) }
	derr := func(line, col int, msg string) { diag(line, col, msg, "error") }
	var stack []*ctx
	ids := map[string]bool{}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")

	for idx, raw := range lines {
		ln := idx + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if hm := reHeader.FindStringSubmatch(trimmed); hm != nil {
			if ln != 1 && !doc.Header {
				diag(ln, 1, "the #!sdd header must be the first line", "warning")
			}
			doc.Header = true
			if hm[1] != "" {
				doc.Version, _ = strconv.Atoi(hm[1])
			}
			continue
		}
		var top *ctx
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if strings.HasPrefix(trimmed, "#") && !(top != nil && top.kind == "uml" && top.uml.inClassBody()) {
			continue
		}
		src := stripComment(raw)
		if strings.TrimSpace(src) == "" {
			continue
		}
		if top != nil && top.kind == "seq" {
			parseSeqLine(top, strings.TrimSpace(src), ln, diag, &stack)
			continue
		}
		if top != nil && top.kind == "uml" {
			parseUmlLine(top.uml, src, ln, doc, diag, ids, &stack)
			continue
		}
		if top != nil && top.kind == "timing" {
			if parseTimingLine(top.tm, src, ln, diag) {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		toks, lerr := tokenize(src)
		if lerr != nil {
			derr(ln, lerr.col, lerr.msg)
			continue
		}
		terr := func(t tok, m string) { derr(ln, t.col, m) }
		if len(toks) == 0 {
			continue
		}
		if len(toks) == 1 && toks[0].v == "}" && toks[0].k == "sym" {
			if len(stack) == 0 {
				derr(ln, toks[0].col, `unexpected "}"`)
			} else {
				stack = stack[:len(stack)-1]
			}
			continue
		}

		// ---- table body ----
		if top != nil && top.kind == "table" {
			body := strings.TrimSpace(src)
			if top.table.Kind == "enum" {
				v := unq(reEnumTail.ReplaceAllString(body, ""))
				if v != "" {
					top.table.Columns = append(top.table.Columns, &DslColumn{Name: v, NotNull: true, Line: ln})
				}
				continue
			}
			if reIndexesOpen.MatchString(body) {
				stack = append(stack, &ctx{kind: "indexes", table: top.table})
				continue
			}
			if nm := reNote.FindStringSubmatch(body); nm != nil {
				top.table.Note = strp(unq(nm[1]))
				continue
			}
			col := parseColumn(body, ln, func(c int, m string) { derr(ln, c, m) })
			if col != nil {
				dup := false
				for _, c := range top.table.Columns {
					if c.Name == col.Name {
						dup = true
					}
				}
				if dup {
					derr(ln, 1, `duplicate column "`+col.Name+`" in table `+top.table.Name)
				} else {
					top.table.Columns = append(top.table.Columns, col)
				}
			}
			continue
		}
		if top != nil && top.kind == "indexes" {
			m := reIndexLine.FindStringSubmatch(strings.TrimSpace(src))
			if m == nil {
				derr(ln, 1, `expected: column  or  (a, b) [unique, name: "x"]`)
				continue
			}
			var cols []string
			if strings.HasPrefix(m[1], "(") {
				for _, c := range splitTop(m[1][1 : len(m[1])-1]) {
					cols = append(cols, unq(c))
				}
			} else {
				cols = []string{unq(m[1])}
			}
			attrs := splitTop(m[2])
			ix := DslIndex{Columns: cols}
			for _, a := range attrs {
				if strings.EqualFold(a, "unique") {
					ix.Unique = true
				}
			}
			for _, a := range attrs {
				if reIdxName.MatchString(a) {
					ix.Name = unq(reIdxNameStr.ReplaceAllString(a, ""))
					break
				}
			}
			top.table.Indexes = append(top.table.Indexes, ix)
			continue
		}

		// ---- root / group statements ----
		first := toks[0]
		last := toks[len(toks)-1]
		opensBlock := last.k == "sym" && last.v == "{"
		kw := ""
		if first.k == "word" {
			kw = first.v
		}

		if kw == "diagram" || kw == "page" {
			if len(toks) < 2 || toks[1].k == "sym" {
				terr(first, kw+` needs a title, e.g. `+kw+` "My system"`)
				continue
			}
			doc.Title = strp(toks[1].v)
			continue
		}
		if kw == "node" || kw == "group" {
			if len(toks) < 2 || toks[1].k == "sym" {
				terr(first, kw+` needs an id, e.g. `+kw+` api "API"`)
				continue
			}
			idTok := toks[1]
			id := idTok.v
			i := 2
			label := id
			if i < len(toks) && toks[i].k == "str" {
				label = toks[i].v
				i++
			}
			end := len(toks)
			if opensBlock {
				end--
			}
			attrs := parseAttrs(toks[:end], i, terr)
			if opensBlock && kw == "node" {
				terr(last, "only groups can contain nodes")
			}
			if ids[id] {
				derr(ln, idTok.col, `duplicate id "`+id+`"`)
			} else {
				ids[id] = true
			}
			parent := ""
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].kind == "group" {
					parent = stack[j].id
					break
				}
			}
			icon, sub := attrs["icon"], attrs["sub"]
			delete(attrs, "icon")
			delete(attrs, "sub")
			doc.Nodes = append(doc.Nodes, &DslNode{ID: id, Label: label, Kind: kw, Icon: icon, Sub: sub, Parent: parent, Attrs: attrs, Line: ln})
			if opensBlock && kw == "group" {
				stack = append(stack, &ctx{kind: "group", id: id})
			}
			continue
		}
		if kw == "table" || kw == "enum" || kw == "view" {
			if top != nil {
				terr(first, kw+" blocks must be at the top level")
			}
			if len(toks) < 2 || toks[1].k == "sym" {
				terr(first, kw+` needs a name, e.g. `+kw+` users {`)
				continue
			}
			if !opensBlock {
				terr(last, `expected "{" after the `+kw+` name`)
				continue
			}
			nameTok := toks[1]
			table := &DslTable{Name: nameTok.v, Kind: kw, Line: ln}
			dup := false
			for _, t := range doc.Tables {
				if t.Name == table.Name {
					dup = true
				}
			}
			if dup {
				derr(ln, nameTok.col, `duplicate table "`+table.Name+`"`)
			} else {
				doc.Tables = append(doc.Tables, table)
			}
			stack = append(stack, &ctx{kind: "table", table: table})
			continue
		}
		if kw == "ref" {
			body := reRefPrefix.ReplaceAllString(strings.TrimSpace(src), "")
			m := reRefLine.FindStringSubmatch(body)
			var a, b DslRefEnd
			var aok, bok bool
			if m != nil {
				a, aok = parseEnd(m[1])
				b, bok = parseEnd(m[3])
			}
			if m == nil || !aok || !bok {
				terr(first, "expected: ref: orders.user_id > users.id")
				continue
			}
			attrs := map[string]string{}
			for _, p := range splitTop(m[4]) {
				if kv := reRefAttr.FindStringSubmatch(p); kv != nil {
					attrs[kv[1]] = unq(kv[2])
				}
			}
			doc.Refs = append(doc.Refs, &DslRef{From: a, Op: m[2], To: b, Attrs: attrs, Line: ln})
			continue
		}
		if umlBlocks[kw] && len(toks) == 2 && opensBlock {
			if top != nil {
				terr(first, kw+" blocks must be at the top level")
			}
			stack = append(stack, &ctx{kind: "uml", uml: &umlCtx{block: kw}})
			continue
		}
		if kw == "timing" {
			if !opensBlock {
				terr(first, `expected: timing id "Title" {`)
				continue
			}
			i := 1
			id, title := "", ""
			if i < len(toks) && toks[i].k == "word" {
				id = toks[i].v
				i++
			}
			if i < len(toks) && toks[i].k == "str" {
				title = toks[i].v
				i++
			}
			if title == "" {
				title = id
				if title == "" {
					title = "Timing"
				}
			}
			if id == "" {
				id = "timing-" + slugOr(title, "seq")
			}
			tm := &DslTiming{ID: id, Title: title, Line: ln}
			dup := false
			for _, x := range doc.Timings {
				if x.ID == id {
					dup = true
				}
			}
			if dup {
				derr(ln, first.col, `duplicate timing "`+id+`"`)
			} else {
				doc.Timings = append(doc.Timings, tm)
			}
			stack = append(stack, &ctx{kind: "timing", tm: tm})
			continue
		}
		if kw == "seq" {
			if !opensBlock {
				terr(first, `expected: seq "Title" {`)
				continue
			}
			i := 1
			id, title := "", ""
			if i < len(toks) && toks[i].k == "word" {
				id = toks[i].v
				i++
			}
			if i < len(toks) && toks[i].k == "str" {
				title = toks[i].v
				i++
			}
			if title == "" {
				title = id
				if title == "" {
					title = "Sequence"
				}
			}
			if id == "" {
				id = "seq-" + slugOr(title, "seq")
			}
			seq := &DslSeq{ID: id, Title: title, Line: ln}
			dup := false
			for _, s := range doc.Seqs {
				if s.ID == id {
					dup = true
				}
			}
			if dup {
				derr(ln, first.col, `duplicate sequence "`+id+`"`)
			} else {
				doc.Seqs = append(doc.Seqs, seq)
			}
			stack = append(stack, &ctx{kind: "seq", seq: seq})
			continue
		}

		// ---- edge:  a -> b "label" attrs ----
		if len(toks) >= 3 && arrows[toks[1].v] && toks[1].k == "word" {
			from, to := toks[0].v, toks[2].v
			i := 3
			label := ""
			if i < len(toks) && toks[i].k == "str" {
				label = toks[i].v
				i++
			}
			attrs := parseAttrs(toks, i, terr)
			doc.Edges = append(doc.Edges, &DslEdge{From: from, To: to, Label: label, Arrow: toks[1].v, Attrs: attrs, Line: ln})
			continue
		}
		if len(toks) > 1 && refOps[toks[1].v] && toks[0].k == "word" && strings.Contains(toks[0].v, ".") {
			terr(toks[0], "relations are written: ref: a.col > b.col")
			continue
		}
		terr(first, `unknown statement "`+first.v+`"`)
	}

	if len(stack) > 0 {
		c := stack[len(stack)-1]
		what := "block"
		switch c.kind {
		case "group":
			what = `group "` + c.id + `"`
		case "seq":
			what = `seq "` + c.seq.Title + `"`
		case "timing":
			what = `timing "` + c.tm.Title + `"`
		case "table", "indexes":
			what = `table "` + c.table.Name + `"`
		case "uml":
			what = describeFrame(c.uml)
		}
		derr(len(lines), 1, `missing "}" to close `+what)
	}
	validateRefs(doc, derr)
	validateUml(doc, func(l, c int, m string) { derr(l, c, m) })
	return ParseResult{doc, diags}
}

func parseColumn(body string, ln int, bad func(col int, m string)) *DslColumn {
	m := reColumn.FindStringSubmatch(body)
	if m == nil || m[2] == "" {
		bad(1, "expected a column:  name type [pk, not null, unique, default: x, ref: > table.col]")
		return nil
	}
	col := &DslColumn{Name: unq(m[1]), Type: strings.TrimSpace(m[2]), Line: ln}
	for _, a := range splitTop(m[3]) {
		low := strings.ToLower(a)
		switch {
		case low == "pk" || low == "primary key":
			col.PK = true
		case low == "unique":
			col.Unique = true
		case low == "not null":
			col.NotNull = true
		case low == "null":
			col.NotNull = false
		case low == "increment" || low == "autoincrement":
			col.Increment = true
		case reDefault.MatchString(a):
			col.Default = strp(unq(reDefaultTail.ReplaceAllString(a, "")))
		case reColNote.MatchString(a):
			col.Note = strp(unq(reColNoteTail.ReplaceAllString(a, "")))
		case reColRef.MatchString(a):
			r := reColRefFull.FindStringSubmatch(a)
			var to DslRefEnd
			ok := false
			if r != nil {
				to, ok = parseEnd(r[2])
			}
			if r == nil || !ok {
				bad(1, "expected ref: > table.column")
			} else {
				col.Ref = &DslRefTo{Op: r[1], To: to}
			}
		default:
			bad(1, `unknown column attribute "`+a+`"`)
		}
	}
	return col
}

func validateRefs(doc *DslDoc, derr func(l, c int, m string)) {
	ids := map[string]bool{}
	for _, n := range doc.Nodes {
		ids[n.ID] = true
	}
	var edges []*DslEdge
	for _, e := range doc.Edges {
		ok := true
		for _, end := range []string{e.From, e.To} {
			if !ids[end] {
				derr(e.Line, 1, `unknown node "`+end+`"`)
				ok = false
				break
			}
		}
		if ok {
			edges = append(edges, e)
		}
	}
	doc.Edges = edges
	cols := map[string]map[string]bool{}
	for _, t := range doc.Tables {
		m := map[string]bool{}
		for _, c := range t.Columns {
			m[c.Name] = true
		}
		cols[t.Name] = m
	}
	check := func(e DslRefEnd, line int) bool {
		c, ok := cols[e.Table]
		if !ok {
			derr(line, 1, `unknown table "`+e.Table+`"`)
			return false
		}
		if !c[e.Column] {
			derr(line, 1, `unknown column "`+e.Table+"."+e.Column+`"`)
			return false
		}
		return true
	}
	var refs []*DslRef
	for _, r := range doc.Refs {
		if check(r.From, r.Line) && check(r.To, r.Line) {
			refs = append(refs, r)
		}
	}
	doc.Refs = refs
	for _, t := range doc.Tables {
		for _, c := range t.Columns {
			if c.Ref != nil && !check(c.Ref.To, c.Line) {
				c.Ref = nil
			}
		}
	}
}

// ---------------------------------------------------------------- sequence

var (
	reSeqPart  = regexp.MustCompile(`(?i)^(participant|actor|boundary|control|entity|database)\s+("[^"]+"|\S+)(?:\s+as\s+(.+))?$`)
	reSeqModel = regexp.MustCompile(`(?i)^((?:participant|actor|boundary|control|entity|database)\s.*)\s+model=([A-Za-z0-9_.-]+)$`)
	reSeqNote  = regexp.MustCompile(`(?i)^note\s+(over|left of|right of)\s+([^:]+):\s*(.*)$`)
	reSeqWord  = regexp.MustCompile(`^(\w+)(?:\s+(.*))?$`)
	reSeqMsg   = regexp.MustCompile(`^("[^"]+"|[^\s:]+)\s*(-->>|-->|->>|->|-x)\s*("[^"]+"|[^\s:]+)\s*(?::\s*(.*))?$`)
	reSeqAuto  = regexp.MustCompile(`(?i)^autonumber$`)
	reSeqEnd   = regexp.MustCompile(`(?i)^end$`)
	reArrowish = regexp.MustCompile(`->|-x`)
)

func parseSeqLine(c *ctx, s string, ln int, diag func(l, c int, m, sev string), stack *[]*ctx) {
	seq := c.seq
	cur := func() *[]*DslSeqItem {
		if len(c.stack) > 0 {
			f := c.stack[len(c.stack)-1]
			return &f.sections[len(f.sections)-1].Items
		}
		return &seq.Items
	}
	ensure := func(id string) {
		for _, p := range seq.Participants {
			if p.ID == id {
				return
			}
		}
		seq.Participants = append(seq.Participants, &DslSeqParticipant{ID: id, Name: id, Kind: "participant"})
	}
	if s == "}" {
		if len(c.stack) > 0 {
			diag(ln, 1, `missing "end" for `+c.stack[len(c.stack)-1].op, "error")
		}
		*stack = (*stack)[:len(*stack)-1]
		return
	}
	if reSeqAuto.MatchString(s) {
		seq.Autonumber = true
		return
	}
	modelID := ""
	if mm := reSeqModel.FindStringSubmatch(s); mm != nil {
		s, modelID = strings.TrimRight(mm[1], " \t"), mm[2]
	}
	if m := reSeqPart.FindStringSubmatch(s); m != nil && seqKinds[strings.ToLower(m[1])] {
		id := unq(m[2])
		name := id
		if m[3] != "" {
			name = unq(m[3])
		}
		kind := strings.ToLower(m[1])
		for _, p := range seq.Participants {
			if p.ID == id {
				p.Name, p.Kind, p.ModelID = name, kind, modelID
				return
			}
		}
		seq.Participants = append(seq.Participants, &DslSeqParticipant{ID: id, Name: name, Kind: kind, ModelID: modelID})
		return
	}
	if m := reSeqNote.FindStringSubmatch(s); m != nil {
		var over []string
		for _, x := range strings.Split(m[2], ",") {
			if u := unq(strings.TrimSpace(x)); u != "" {
				over = append(over, u)
			}
		}
		if len(over) == 0 || len(over) > 2 {
			diag(ln, 1, "a note sits over one or two participants", "error")
			return
		}
		for _, o := range over {
			ensure(o)
		}
		side := ""
		l := strings.ToLower(m[1])
		if strings.HasPrefix(l, "left") {
			side = "left"
		} else if strings.HasPrefix(l, "right") {
			side = "right"
		}
		it := cur()
		*it = append(*it, &DslSeqItem{Type: "note", Over: over, Text: m[3], Side: side})
		return
	}
	m := reSeqWord.FindStringSubmatch(s)
	if m != nil && fragOps[strings.ToLower(m[1])] && !reArrowish.MatchString(strings.Split(s, ":")[0]) {
		c.stack = append(c.stack, &fragFrame{op: strings.ToLower(m[1]), sections: []*DslSeqSection{{Guard: strings.TrimSpace(m[2])}}})
		return
	}
	if m != nil && sectionOps[strings.ToLower(m[1])] != "" && len(c.stack) > 0 {
		f := c.stack[len(c.stack)-1]
		w := strings.ToLower(m[1])
		if f.op != sectionOps[w] && !(w == "else" && (f.op == "alt" || f.op == "opt" || f.op == "loop" || f.op == "break")) {
			diag(ln, 1, `"`+m[1]+`" does not belong in `+f.op, "warning")
		}
		f.sections = append(f.sections, &DslSeqSection{Guard: strings.TrimSpace(m[2])})
		return
	}
	if reSeqEnd.MatchString(s) {
		if len(c.stack) == 0 {
			diag(ln, 1, `"end" without an open alt/opt/loop/par/break/critical`, "error")
			return
		}
		f := c.stack[len(c.stack)-1]
		c.stack = c.stack[:len(c.stack)-1]
		it := cur()
		*it = append(*it, &DslSeqItem{Type: "fragment", Op: f.op, Sections: f.sections})
		return
	}
	if mm := reSeqMsg.FindStringSubmatch(s); mm != nil {
		from, to := unq(mm[1]), unq(mm[3])
		ensure(from)
		ensure(to)
		kind := "return"
		switch mm[2] {
		case "->":
			kind = "sync"
		case "->>":
			kind = "async"
		case "-x":
			kind = "destroy"
		case "-->>":
			kind = "create"
		}
		it := cur()
		*it = append(*it, &DslSeqItem{Type: "message", From: from, To: to, Text: mm[4], Kind: kind})
		return
	}
	snippet := []rune(s)
	if len(snippet) > 40 {
		snippet = snippet[:40]
	}
	diag(ln, 1, `cannot read sequence statement "`+string(snippet)+`"`, "error")
}
